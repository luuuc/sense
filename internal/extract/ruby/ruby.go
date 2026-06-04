// Package ruby extracts Tier-Basic symbols and intra-file edges from
// Ruby source code via tree-sitter-ruby.
//
// Symbol kinds:
//   - class, module          → KindClass / KindModule
//   - def / def self.<name>  → KindMethod (qualified as Class#name / Class.name)
//   - CONST = …              → KindConstant (at any nesting level)
//
// Intra-file edges:
//   - class A < B            → inherits edge (A → B) when B is defined in
//     the same file. Cross-file inheritance is
//     dropped — 01-03 backfills it.
//   - include M / extend M   → includes edge (class → M) when M is
//     defined in the same file.
//
// Calls edges:
//   - Method / singleton-method bodies are walked for `call` nodes. The
//     target is the callee's surface text — `method`, `recv.method`, or
//     `A::B.method` — with no type inference beyond the syntax. Dynamic
//     dispatch via `send` / `public_send` / `__send__` is emitted with
//     confidence 0.7 only when the first argument is a literal symbol or
//     string; anything else is skipped (unresolvable).
//   - Bare receiverless Ruby method calls without parentheses
//     (`create_topic`, `save_post`) are parsed as `identifier` nodes
//     rather than `call` nodes by tree-sitter-ruby.
//     emitBareIdentifierCalls picks up identifiers in statement position
//     (direct children of body_statement/then/else/begin/ensure) and
//     emits them as calls edges with ConfidenceDynamic. The resolver
//     drops any that don't match a known symbol.
//
// Qualified names follow 05-languages.md: A::B::C for classes/modules,
// A::B#m for instance methods, A::B.m for singleton methods, A::B::CONST
// for constants. Top-level symbols carry no leading separator.
package ruby

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// Extractor is the Ruby implementation of extract.Extractor.
type Extractor struct{}

func (Extractor) Grammar() *sitter.Language { return grammars.Ruby() }
func (Extractor) Language() string          { return "ruby" }
func (Extractor) Extensions() []string      { return []string{".rb", ".rake", ".gemspec"} }
func (Extractor) Tier() extract.Tier        { return extract.TierBasic }

// HarvestsMentions reports that the Ruby extractor streams the broad mention set
// (see Extract's MentionEmitter block), so the scan records `ruby` as harvested
// even on a scan that yields zero mentions — the dead-code soundness gate then
// treats a Ruby symbol as proven-against-an-empty-set, not never-harvested.
func (Extractor) HarvestsMentions() bool { return true }

func (Extractor) Extract(tree *sitter.Tree, source []byte, filePath string, emit extract.Emitter) error {
	returnTypes := buildFileReturnTypeMap(tree.RootNode(), source)
	w := &walker{
		source:            source,
		emit:              emit,
		filePath:          filePath,
		classInstanceVars: make(map[string]map[string]string),
		returnTypes:       returnTypes,
		emittedCallbacks:  make(map[string]bool),
		emittedSynthetics: make(map[string]bool),
		pkgBindings:       make(map[string]string),
		methodVisibility:  make(map[string]string),
	}
	w.collectConstants(tree.RootNode(), nil)
	if err := w.walk(tree.RootNode(), nil); err != nil {
		return err
	}
	// Stream the file's reflective dispatch-target names to the emitter when
	// it accepts them. The names feed a project-global set in sense_meta so
	// the dead-code arbiter keeps reflectively-reachable symbols open-world.
	if de, ok := emit.(extract.DispatchEmitter); ok {
		for _, name := range collectDispatchNames(tree.RootNode(), source) {
			if err := de.DispatchName(name); err != nil {
				return err
			}
		}
	}
	// Stream the file's broad mention set (every identifier/symbol token except
	// definition names). The project-global union feeds the arbiter's soundness
	// gate so a private method earns `dead` only when its name is mentioned
	// nowhere a hidden caller could be — making `dead` sound even where the
	// resolver could not bind every call.
	if me, ok := emit.(extract.MentionEmitter); ok {
		for _, name := range collectMentionedNames(tree.RootNode(), source) {
			if err := me.MentionName(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func init() { extract.Register(Extractor{}) }

// ---- walker ----

type walker struct {
	source             []byte
	emit               extract.Emitter
	filePath           string
	routeNS            []string                     // namespace stack for route files (e.g. ["Admin"])
	routeNSRaw         []string                     // same stack in snake_case for route-helper names (e.g. ["admin"])
	routeResourceDepth int                          // resource-block nesting depth; >0 means a nested resource (helper name not derivable confidently)
	testScope          []string                     // nested RSpec block descriptions for synthetic scope
	classInstanceVars  map[string]map[string]string // @ivar type map per class
	returnTypes        map[string]string            // method_qualified → class_name (file-level)
	emittedCallbacks   map[string]bool              // dedup synthetic callback symbols by qualified name
	emittedSynthetics  map[string]bool              // dedup synthetic base symbols (ruby-core:Struct/Data) per file
	pkgBindings        map[string]string            // unqualified name → qualified name for file-level constants
	methodVisibility   map[string]string            // method_qualified → public/private/protected, from the per-class pre-pass
}

// walk visits node and its children under the given class/module scope.
// scope is the chain of enclosing class/module qualified-name segments —
// e.g. ["App", "Services"] inside `module App; module Services; …`.
func (w *walker) walk(n *sitter.Node, scope []string) error {
	if n == nil {
		return nil
	}

	switch n.Kind() {
	case "class":
		return w.handleClassOrModule(n, scope, model.KindClass)
	case "module":
		return w.handleClassOrModule(n, scope, model.KindModule)
	case "method":
		return w.handleMethod(n, scope, false)
	case "singleton_method":
		return w.handleMethod(n, scope, true)
	case "assignment":
		// `CONST = Struct.new(...) do … end` and friends define a nested
		// class, not a flat constant — the block's `def`s belong to the
		// struct, not the enclosing scope. handleClassBuilderAssignment
		// consumes those; ordinary `CONST = value` falls through.
		consumed, err := w.handleClassBuilderAssignment(n, scope)
		if err != nil {
			return err
		}
		if consumed {
			return nil
		}
		if err := w.handleConstantAssignment(n, scope); err != nil {
			return err
		}
		return w.walkChildren(n, scope)
	case "call":
		consumed, err := w.dispatchCall(n, scope)
		if err != nil {
			return err
		}
		if consumed {
			return nil
		}
		return w.walkChildren(n, scope)
	default:
		return w.walkChildren(n, scope)
	}
}

// dispatchCall extracts the method name from a call node once and routes
// to the appropriate handler. Returns true if the node was fully consumed
// (caller should not descend into children).
func (w *walker) dispatchCall(n *sitter.Node, scope []string) (bool, error) {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return false, nil
	}
	methodName := extract.Text(methodNode, w.source)
	if methodName == "" {
		return false, nil
	}

	// Class-body-level handlers (require enclosing class/module) and
	// top-level handlers (no enclosing class/module) are mutually exclusive
	// by scope. An unrecognized method falls through to the RSpec DSL check.
	if len(scope) > 0 {
		if handled, err := w.dispatchClassBodyCall(n, scope, methodName); handled {
			return false, err
		}
	} else if consumed, handled, err := w.dispatchTopLevelCall(n, methodName); handled {
		return consumed, err
	}

	// RSpec DSL blocks with a body — handled as test scopes.
	if rspecDSLMethods[methodName] {
		return w.handleTestBlock(n, scope, methodName)
	}

	return false, nil
}

// dispatchClassBodyCall routes a call inside a class/module body to its
// edge emitter (include/association/broadcast/scope/callback). handled
// reports whether the method name was recognized; class-body handlers never
// consume the node, so dispatchCall always descends into children.
func (w *walker) dispatchClassBodyCall(n *sitter.Node, scope []string, methodName string) (handled bool, err error) {
	switch methodName {
	case "include", "extend", "prepend":
		return true, w.emitIncludeEdge(n, scope)
	case "has_many", "has_and_belongs_to_many", "belongs_to", "has_one":
		return true, w.emitAssociationEdge(n, scope, methodName)
	case "broadcasts_to", "broadcasts":
		return true, w.emitBroadcastEdge(n, scope)
	case "scope":
		return true, w.emitScopeEdge(n, scope)
	}
	if model.RailsCallbackNames[methodName] {
		return true, w.emitCallbackEdges(n, scope, methodName)
	}
	return false, nil
}

// dispatchTopLevelCall routes a call at file scope (route DSL, importmap pin)
// to its handler. handled reports whether the method name was recognized;
// consumed reports whether the node was fully handled (route blocks consume,
// importmap pins do not).
func (w *walker) dispatchTopLevelCall(n *sitter.Node, methodName string) (consumed, handled bool, err error) {
	switch methodName {
	case "resources":
		return true, true, w.handleResources(n, false)
	case "resource":
		return true, true, w.handleResources(n, true)
	case "namespace":
		return true, true, w.handleRouteNamespace(n)
	case "pin":
		return false, true, w.emitImportmapPin(n)
	case "pin_all_from":
		return false, true, w.emitImportmapPinAll(n)
	}
	if routeVerbs[methodName] {
		return true, true, w.handleVerbRoute(n)
	}
	return false, false, nil
}

func (w *walker) walkChildren(n *sitter.Node, scope []string) error {
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if err := w.walk(n.NamedChild(i), scope); err != nil {
			return err
		}
	}
	return nil
}

// getBlockChild returns the block child (do_block or block) of a call node.
func getBlockChild(n *sitter.Node) *sitter.Node {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c != nil && (c.Kind() == "do_block" || c.Kind() == "block") {
			return c
		}
	}
	return nil
}

// getBlockBody returns the body child of a block node.
func getBlockBody(block *sitter.Node) *sitter.Node {
	for i := uint(0); i < block.NamedChildCount(); i++ {
		c := block.NamedChild(i)
		if c != nil && (c.Kind() == "body_statement" || c.Kind() == "block_body") {
			return c
		}
	}
	return nil
}

// isInsideBlock returns true if n is contained within a block or do_block.
func isInsideBlock(n *sitter.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "block" || p.Kind() == "do_block" {
			return true
		}
	}
	return false
}

// isInsideNestedBlock returns true if n is contained within a block that
// is a descendant of parent (i.e. a nested block). parent itself should be
// a block or do_block node.
func isInsideNestedBlock(n, parent *sitter.Node) bool {
	parentID := parent.Id()
	for p := n.Parent(); p != nil && p.Id() != parentID; p = p.Parent() {
		if p.Kind() == "block" || p.Kind() == "do_block" {
			return true
		}
	}
	return false
}

// statementParents is the set of tree-sitter-ruby node kinds whose
// direct identifier children are in statement position — bare method
// calls rather than variable references or subexpressions.
var statementParents = map[string]bool{
	"body_statement": true,
	"then":           true,
	"else":           true,
	"begin":          true,
	"ensure":         true,
	// program is the root of a parsed fragment (ExtractEmbeddedCalls). Whole-
	// file walks only ever pass a method/test body_statement to
	// emitBareIdentifierCalls, never the program root, so listing it here is
	// inert for file extraction and only catches top-level bare calls in an
	// embedded `<% current_user %>` fragment.
	"program": true,
}

// valueParents is the set of node kinds where a bare identifier sits in a
// value position — a method argument, string interpolation, conditional,
// or the right-hand side of an assignment. A receiverless identifier here is
// a method send (e.g. a controller-concern helper like `current_currency`
// used inside `redirect_to foo(current_currency)`), not a definition or a
// call receiver. We emit those as `self.name` so the resolver can bind them
// to the included-concern method; a name that resolves to no symbol is
// dropped, and an ambiguous one is clamped below the blast floor. Locals and
// parameters are filtered out before this set is consulted.
var valueParents = map[string]bool{
	"argument_list":   true,
	"interpolation":   true,
	"pair":            true,
	"array":           true,
	"binary":          true,
	"unary":           true,
	"return":          true,
	"if":              true,
	"unless":          true,
	"while":           true,
	"until":           true,
	"elsif":           true,
	"case":            true,
	"when":            true,
	"if_modifier":     true, // `render :ok if current_country`
	"unless_modifier": true,
	"while_modifier":  true,
	"until_modifier":  true,
	// Assignment RHS — `@currency = current_currency`, `@user ||= current_user`.
	// isAssignmentTarget excludes the left-hand-side identifier.
	"assignment":          true,
	"operator_assignment": true,
}

// emitBareIdentifierCalls walks a method body for identifier nodes that are
// bare (receiverless, parenless) method calls. Tree-sitter-ruby parses these
// as identifier nodes rather than call nodes; we emit them as `self.name` so
// the resolver rewrites them to the enclosing class or an included concern
// (e.g. `validate` → `Order#validate`, `current_currency` →
// `CurrencyContext#current_currency`).
//
// Statement-position identifiers (direct children of body_statement, then,
// else, begin, ensure) are always emitted. Value-position identifiers
// (arguments, interpolations, conditionals, assignment RHS) are emitted only
// when the name is not a known local variable or parameter — the signal that
// distinguishes a method send from a variable reference. params carries the
// enclosing definition's parameter names; body-local assignments and block
// parameters are discovered here.
func (w *walker) emitBareIdentifierCalls(body *sitter.Node, sourceQualified string, confidence float64, params map[string]bool) error {
	locals := collectLocalNames(body, w.source, params)
	return extract.WalkNamedDescendants(body, "identifier", func(ident *sitter.Node) error {
		if w.isInsideNestedTestBlock(ident, body) {
			return nil
		}
		parent := ident.Parent()
		if parent == nil {
			return nil
		}
		name := extract.Text(ident, w.source)
		if name == "" {
			return nil
		}
		switch {
		case statementParents[parent.Kind()]:
			// always a bare call in statement position
		case valueParents[parent.Kind()] && !locals[name] && !isAssignmentTarget(parent, ident):
			// a value-position send that isn't a local/parameter
		default:
			return nil
		}
		line := extract.Line(ident.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: sourceQualified,
			TargetQualified: "self." + name,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      confidence,
		})
	})
}

// isAssignmentTarget reports whether ident is the left-hand side of its
// parent assignment — `x` in `x = current_currency`, which is a variable
// being defined, not a method send. Right-hand-side identifiers fall through
// and are treated as value-position sends.
func isAssignmentTarget(parent, ident *sitter.Node) bool {
	switch parent.Kind() {
	case "assignment", "operator_assignment":
		left := parent.ChildByFieldName("left")
		return left != nil && left.Equals(*ident)
	}
	return false
}

// addIdentifierNames records every identifier name in n's subtree (including
// n itself when it is an identifier) into set. WalkNamedDescendants only
// visits children, so a simple `x = ...` target — where the left node is the
// identifier itself — needs the explicit self check.
func addIdentifierNames(n *sitter.Node, source []byte, set map[string]bool) {
	if n == nil {
		return
	}
	if n.Kind() == "identifier" {
		if name := extract.Text(n, source); name != "" {
			set[name] = true
		}
	}
	_ = extract.WalkNamedDescendants(n, "identifier", func(id *sitter.Node) error {
		if name := extract.Text(id, source); name != "" {
			set[name] = true
		}
		return nil
	})
}

// methodParamNames returns the parameter names of a method/singleton_method
// node so they can be excluded from value-position send detection. It collects
// every identifier under the parameters node, which over-approximates across
// optional/keyword/splat/block parameter shapes — a safe bias, since the only
// effect of an extra name is suppressing one would-be self-call edge.
func methodParamNames(method *sitter.Node, source []byte) map[string]bool {
	names := map[string]bool{}
	if method == nil {
		return names
	}
	addIdentifierNames(method.ChildByFieldName("parameters"), source, names)
	return names
}

// collectLocalNames returns the set of names that are local variables or
// parameters within body: the seed (enclosing parameters), assignment targets,
// and block parameters. A name in this set is a variable reference, not a
// method send, so value-position emission skips it.
func collectLocalNames(body *sitter.Node, source []byte, seed map[string]bool) map[string]bool {
	locals := map[string]bool{}
	for k := range seed {
		locals[k] = true
	}
	if body == nil {
		return locals
	}
	for _, kind := range []string{"assignment", "operator_assignment"} {
		_ = extract.WalkNamedDescendants(body, kind, func(n *sitter.Node) error {
			// `x = ...` (identifier) and `a, b = ...` (left_assignment_list)
			// both reduce to their identifier names. Attribute writes like
			// `obj.attr = ...` add their receiver too — harmless, it just
			// suppresses one would-be self-call.
			addIdentifierNames(n.ChildByFieldName("left"), source, locals)
			return nil
		})
	}
	_ = extract.WalkNamedDescendants(body, "block_parameters", func(n *sitter.Node) error {
		addIdentifierNames(n, source, locals)
		return nil
	})
	return locals
}

// collectConstants recursively pre-scans for constant assignments so
// method bodies can emit references edges. Descends into class/module
// bodies to find nested constants (e.g. MyClass::TIMEOUT).
//
//nolint:gocognit // 27-10: retired by the ruby extractor split
func (w *walker) collectConstants(n *sitter.Node, scope []string) {
	if n == nil {
		return
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "assignment":
			lhs := child.ChildByFieldName("left")
			if lhs != nil && lhs.Kind() == "constant" {
				name := extract.Text(lhs, w.source)
				if name != "" {
					qualified := name
					if parent := strings.Join(scope, "::"); parent != "" {
						qualified = parent + "::" + name
					}
					w.pkgBindings[name] = qualified
				}
			}
		case "class", "module":
			nameNode := child.ChildByFieldName("name")
			if nameNode == nil {
				continue
			}
			name := extract.Text(nameNode, w.source)
			if name == "" {
				continue
			}
			// Register the class/module name so references to it
			// (`PriceValue.new`, `rescue ApiError`, bare `Service`) resolve
			// to the type itself, not just its methods. Keyed by trailing
			// segment, matching how the name is written at reference sites.
			segments := strings.Split(name, "::")
			last := segments[len(segments)-1]
			if _, exists := w.pkgBindings[last]; !exists {
				w.pkgBindings[last] = strings.Join(append(slices.Clone(scope), segments...), "::")
			}
			newScope := append(slices.Clone(scope), segments...)
			if body := child.ChildByFieldName("body"); body != nil {
				w.collectConstants(body, newScope)
			}
		}
	}
}

// collectConstRefs walks a method or class body for constant and
// scope-resolution references and emits references edges to them.
// Same-file constants/classes resolve to their qualified name via
// pkgBindings; cross-file references emit their surface text for the
// resolver to match (exact-qualified first, unqualified fallback
// second). This is what makes a class show up as referenced — and
// therefore alive — when it is only ever reached through `Const.new`,
// `rescue Const`, `Const::CHILD`, or a bare constant mention.
//
// Skipped, because other edges already record them or they are
// definitions rather than references: a constant that is the inner
// segment of a scope resolution, a superclass, or the left-hand side of
// an assignment. When skipNestedDefs is set (class-body walks) anything
// inside a nested def/class/module is skipped too, so a method's
// references stay attributed to the method, not its enclosing class.
//
//nolint:gocyclo,gocognit // 27-10: retired by the ruby extractor split
func (w *walker) collectConstRefs(root *sitter.Node, sourceQualified string, skipNestedDefs bool) error {
	if root == nil {
		return nil
	}
	seen := map[string]bool{}
	emit := func(node *sitter.Node, target string) error {
		if target == "" || seen[target] {
			return nil
		}
		seen[target] = true
		line := extract.Line(node.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: sourceQualified,
			TargetQualified: target,
			Kind:            model.EdgeReferences,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		})
	}

	// Bare constant references (`Foo`, `BAR`).
	if err := extract.WalkNamedDescendants(root, "constant", func(cn *sitter.Node) error {
		if skipNestedDefs && (isInsideNestedDef(cn, root) || w.isStructuralDSLArg(cn, root)) {
			return nil
		}
		if p := cn.Parent(); p != nil {
			switch p.Kind() {
			case "scope_resolution", "superclass":
				return nil
			case "assignment", "operator_assignment":
				if left := p.ChildByFieldName("left"); left != nil && left.Id() == cn.Id() {
					return nil // constant definition, not a reference
				}
			}
		}
		name := extract.Text(cn, w.source)
		if name == "" {
			return nil
		}
		target := name
		if q, ok := w.pkgBindings[name]; ok {
			target = q
		}
		return emit(cn, target)
	}); err != nil {
		return err
	}

	// Namespaced references (`Foo::Bar`, `A::B::CONST`). Emit only the
	// outermost scope resolution; skip superclasses.
	return extract.WalkNamedDescendants(root, "scope_resolution", func(sr *sitter.Node) error {
		if skipNestedDefs && (isInsideNestedDef(sr, root) || w.isStructuralDSLArg(sr, root)) {
			return nil
		}
		if p := sr.Parent(); p != nil {
			switch p.Kind() {
			case "scope_resolution", "superclass":
				return nil
			}
		}
		return emit(sr, strings.TrimSpace(extract.Text(sr, w.source)))
	})
}

// structuralRefDSLs are class-body DSL calls whose constant arguments
// already produce a more specific edge (includes / composes / calls).
// The class-body reference walk skips their arguments so a single
// `include Foo` does not also emit a redundant references edge.
// Exception DSLs (rescue_from / retry_on / discard_on) are deliberately
// absent — their class arguments have no other edge and must be captured.
var structuralRefDSLs = map[string]bool{
	"include": true, "extend": true, "prepend": true,
	"has_many": true, "has_one": true, "belongs_to": true,
	"has_and_belongs_to_many": true,
	"broadcasts":              true, "broadcasts_to": true,
}

// isStructuralDSLArg reports whether n is an argument to a structural DSL
// call (see structuralRefDSLs) nested below root.
func (w *walker) isStructuralDSLArg(n, root *sitter.Node) bool {
	rootID := root.Id()
	for p := n.Parent(); p != nil && p.Id() != rootID; p = p.Parent() {
		if p.Kind() == "call" {
			if mn := p.ChildByFieldName("method"); mn != nil && structuralRefDSLs[extract.Text(mn, w.source)] {
				return true
			}
		}
	}
	return false
}

// isInsideNestedDef reports whether n sits inside a method/class/module
// nested below root (root excluded). Used by class-body reference walks
// to avoid re-attributing a method's references to its enclosing class.
func isInsideNestedDef(n, root *sitter.Node) bool {
	rootID := root.Id()
	for p := n.Parent(); p != nil && p.Id() != rootID; p = p.Parent() {
		switch p.Kind() {
		case "method", "singleton_method", "class", "module":
			return true
		}
	}
	return false
}

// classBuilder describes a `CONST = <builder>` right-hand side that
// defines a class rather than a plain value: `Struct.new`, `Data.define`,
// or `Class.new`. baseTarget is the qualified name for the constant's
// `inherits` edge ("" when there is no statically-known superclass, e.g.
// a bare `Class.new`). valueObject is true only for Struct/Data — the
// duck-typed value-object idiom dead-code recognition keys on. block is
// the do…end / {} body, or nil.
type classBuilder struct {
	baseTarget  string
	valueObject bool
	block       *sitter.Node
}

// detectClassBuilder classifies the RHS of a constant assignment. It
// returns ok=false for anything that is not a recognised class builder,
// so the caller falls back to plain-constant handling.
func detectClassBuilder(rhs *sitter.Node, source []byte) (classBuilder, bool) {
	if rhs == nil || rhs.Kind() != "call" {
		return classBuilder{}, false
	}
	recv := rhs.ChildByFieldName("receiver")
	method := rhs.ChildByFieldName("method")
	if recv == nil || method == nil {
		return classBuilder{}, false
	}
	recvText := extract.Text(recv, source)
	methodText := extract.Text(method, source)
	cb := classBuilder{block: getBlockChild(rhs)}
	switch {
	case recvText == "Struct" && methodText == "new":
		cb.baseTarget = extract.RubyCoreStruct
		cb.valueObject = true
	case recvText == "Data" && methodText == "define":
		cb.baseTarget = extract.RubyCoreData
		cb.valueObject = true
	case recvText == "Class" && methodText == "new":
		// The superclass is the first constant argument, if any.
		// `Class.new` with no constant argument has no known parent.
		cb.baseTarget = firstConstantArg(rhs, source)
	default:
		return classBuilder{}, false
	}
	return cb, true
}

// firstConstantArg returns the surface text of the first constant /
// scope-resolution argument of a call, or "" when there is none.
func firstConstantArg(call *sitter.Node, source []byte) string {
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	for i := uint(0); i < args.NamedChildCount(); i++ {
		c := args.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "constant", "scope_resolution":
			return extract.Text(c, source)
		}
	}
	return ""
}

// handleClassBuilderAssignment handles `CONST = Struct.new(...)`,
// `CONST = Data.define(...)`, and `CONST = Class.new(Super)` — with or
// without a do…end / {} block. The constant becomes a nested CLASS
// symbol (qualified scope::CONST), the builder's base class becomes an
// `inherits` edge, and any block body is walked with CONST pushed onto
// the scope so its members qualify as `…::CONST#method` rather than
// inheriting the enclosing class scope. Returns true when the node was
// consumed (caller must not fall through to constant/child handling).
func (w *walker) handleClassBuilderAssignment(n *sitter.Node, scope []string) (bool, error) {
	lhs := n.ChildByFieldName("left")
	if lhs == nil || lhs.Kind() != "constant" {
		return false, nil
	}
	cb, ok := detectClassBuilder(n.ChildByFieldName("right"), w.source)
	if !ok {
		return false, nil
	}
	name := extract.Text(lhs, w.source)
	if name == "" {
		return false, nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	line := extract.Line(n.StartPosition())

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindClass,
		ParentQualified: parent,
		LineStart:       line,
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return false, err
	}

	if cb.baseTarget != "" {
		if cb.valueObject {
			if err := w.emitSyntheticBase(cb.baseTarget, line); err != nil {
				return false, err
			}
		}
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: cb.baseTarget,
			Kind:            model.EdgeInherits,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		}); err != nil {
			return false, err
		}
	}

	if cb.block != nil {
		body := getBlockBody(cb.block)
		if body != nil {
			newScope := append(slices.Clone(scope), name)
			if err := w.walkChildren(body, newScope); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}

// emitSyntheticBase emits a synthetic stand-in symbol for a Ruby core
// class (ruby-core:Struct / ruby-core:Data) so that `inherits` edges
// pointing at it resolve and persist (target_id is NOT NULL). Deduped
// per file: many value objects in one file share a single base symbol.
func (w *walker) emitSyntheticBase(qualified string, line int) error {
	if w.emittedSynthetics[qualified] {
		return nil
	}
	w.emittedSynthetics[qualified] = true
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:       strings.TrimPrefix(qualified, extract.PrefixRubyCore),
		Qualified:  qualified,
		Kind:       model.KindClass,
		Visibility: "public",
		LineStart:  line,
		LineEnd:    line,
	})
}

// handleConstantAssignment emits a KindConstant symbol when the LHS of
// an assignment is a single `constant` node (CAPS name). Nested scope
// resolutions on the LHS (A::B = …) and non-constant LHS are skipped
// — not wrong to record, just not what "constant" means structurally.
func (w *walker) handleConstantAssignment(n *sitter.Node, scope []string) error {
	lhs := n.ChildByFieldName("left")
	if lhs == nil || lhs.Kind() != "constant" {
		return nil
	}
	name := extract.Text(lhs, w.source)
	if name == "" {
		return nil
	}
	parent := strings.Join(scope, "::")
	qualified := name
	if parent != "" {
		qualified = parent + "::" + name
	}
	return w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindConstant,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	})
}

// emitIncludeEdge emits includes edges for `include M`, `extend M`, `prepend M`.
func (w *walker) emitIncludeEdge(n *sitter.Node, scope []string) error {
	args := n.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}
	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	// Each argument becomes a separate edge. Emit edges for static
	// module references (constant and scope_resolution nodes); the
	// resolver handles cross-file lookup. Skip dynamic expressions
	// where the target cannot be determined (target == "").
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		target := ""
		switch arg.Kind() {
		case "constant":
			target = extract.Text(arg, w.source)
		case "scope_resolution":
			target = extract.Text(arg, w.source)
		}
		if target == "" {
			continue
		}
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: source,
			TargetQualified: target,
			Kind:            model.EdgeIncludes,
			Line:            &line,
			Confidence:      1.0,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitBroadcastEdge emits calls edges for Turbo Streams broadcasts_to/broadcasts.
// The target is a synthetic turbo-channel name that matches what the ERB extractor
// emits for turbo_stream_from.
func (w *walker) emitBroadcastEdge(n *sitter.Node, scope []string) error {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil {
		return nil
	}

	var channelName string
	switch first.Kind() {
	case "simple_symbol":
		channelName = strings.TrimPrefix(extract.Text(first, w.source), ":")
	case "string":
		channelName = extractStringValue(first, w.source)
	default:
		return nil
	}
	if channelName == "" {
		return nil
	}

	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: extract.PrefixTurboChannel + channelName,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      0.8,
	})
}

// emitImportmapPin handles `pin "name"` and `pin "name", to: "path"` in importmap.rb.
func (w *walker) emitImportmapPin(n *sitter.Node) error {
	return w.emitImportmapEdge(n, true)
}

// emitImportmapPinAll handles `pin_all_from "dir", under: "prefix"` in importmap.rb.
func (w *walker) emitImportmapPinAll(n *sitter.Node) error {
	return w.emitImportmapEdge(n, false)
}

func (w *walker) emitImportmapEdge(n *sitter.Node, checkToOverride bool) error {
	if !w.isImportmap() {
		return nil
	}
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "string" {
		return nil
	}
	target := extractStringValue(first, w.source)
	if target == "" {
		return nil
	}

	if checkToOverride {
		if toPath := findKeywordArg(args, "to", w.source); toPath != "" {
			target = toPath
		}
	}

	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: w.filePath,
		TargetQualified: extract.PrefixImportmap + target,
		Kind:            model.EdgeImports,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

func (w *walker) isImportmap() bool {
	base := w.filePath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return base == "importmap.rb"
}

// emitAssociationEdge emits composes edges for Rails association macros.
func (w *walker) emitAssociationEdge(n *sitter.Node, scope []string, methodName string) error {
	needsSingularize := methodName == "has_many" || methodName == "has_and_belongs_to_many"

	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "simple_symbol" {
		return nil
	}
	assocName := strings.TrimPrefix(extract.Text(first, w.source), ":")

	// Check for serializer: override (AMS pattern — value is a constant).
	// Check for class_name: override (ActiveRecord pattern — value is a string).
	target := ""
	if st := findKeywordConstantArg(args, "serializer", w.source); st != "" {
		target = st
	} else if cn := findKeywordArg(args, "class_name", w.source); cn != "" {
		target = cn
	} else if needsSingularize {
		target = classify(assocName)
	} else {
		target = pascalCase(assocName)
	}

	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeComposes,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// emitCallbackEdges emits calls edges for Rails lifecycle callbacks and
// a symbol for the callback declaration so convention detection can find it.
// Duplicate symbols for the same callback name on the same class are suppressed.
func (w *walker) emitCallbackEdges(n *sitter.Node, scope []string, callbackName string) error {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}

	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())

	qualified := source + "." + callbackName
	if !w.emittedCallbacks[qualified] {
		w.emittedCallbacks[qualified] = true
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:            callbackName,
			Qualified:       qualified,
			Kind:            model.KindMethod,
			ParentQualified: source,
			LineStart:       line,
			LineEnd:         line,
			Docstring:       docstringFor(n, w.source),
		}); err != nil {
			return err
		}
	}

	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "simple_symbol" {
			continue
		}
		target := strings.TrimPrefix(extract.Text(arg, w.source), ":")
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: source,
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitScopeEdge handles `scope :name, -> { ... }` declarations.
// It emits the scope name as a class method symbol and a calls edge
// from the class to the scope name.
func (w *walker) emitScopeEdge(n *sitter.Node, scope []string) error {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "simple_symbol" {
		return nil
	}
	name := strings.TrimPrefix(extract.Text(first, w.source), ":")
	if name == "" {
		return nil
	}
	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       source + "." + name,
		Kind:            model.KindMethod,
		ParentQualified: source,
		LineStart:       line,
		LineEnd:         line,
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: source + "." + name,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

// resourceActions is the set of RESTful actions for `resources`.
var resourceActions = []string{"index", "show", "new", "create", "edit", "update", "destroy"}

// singularResourceActions is the set for `resource` (no index).
var singularResourceActions = []string{"show", "new", "create", "edit", "update", "destroy"}

// routeVerbs maps HTTP verb DSL methods to themselves (for detection).
var routeVerbs = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
}

// handleResources emits calls edges for a `resources :orders` or
// `resource :session` declaration.
func (w *walker) handleResources(n *sitter.Node, singular bool) error {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "simple_symbol" {
		return nil
	}
	name := strings.TrimPrefix(extract.Text(first, w.source), ":")

	// Build controller name: namespace prefix + name + "Controller"
	// `resources :orders` → OrdersController (plural name kept as-is)
	// `resource :session` → SessionsController (Rails pluralizes for controller lookup)
	var controller string
	if singular {
		controller = pascalCase(name) + "sController"
	} else {
		controller = pascalCase(name) + "Controller"
	}
	if len(w.routeNS) > 0 {
		controller = strings.Join(w.routeNS, "::") + "::" + controller
	}

	actions := resourceActions
	if singular {
		actions = singularResourceActions
	}

	line := extract.Line(n.StartPosition())
	for _, action := range actions {
		target := controller + "#" + action
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: "routes",
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}

	// Route-helper symbols (orders_path → OrdersController#index, …). Only for
	// top-level / namespaced resources: a nested resource's helper name is
	// prefixed by its parent's singular (order_items_path), which we can't
	// reconstruct from the inner declaration alone, so we emit no helper rather
	// than a wrong one. The controller edges above are still emitted for nested
	// resources — only the helper naming is skipped.
	if w.routeResourceDepth == 0 {
		if err := w.emitResourceRouteHelpers(name, singular, controller, line); err != nil {
			return err
		}
	}

	// If the resource has a block, walk it for nested routes.
	if block := n.ChildByFieldName("block"); block != nil {
		w.routeResourceDepth++
		defer func() { w.routeResourceDepth-- }()
		return w.walkChildren(block, nil)
	}
	return nil
}

// routeHelper pairs a generated path/url helper base name (without the _path /
// _url suffix) with the controller action it routes to.
type routeHelper struct {
	base   string
	action string
}

// emitResourceRouteHelpers emits the synthetic route:* symbols and their
// helper → controller#action edges for a resources/resource declaration. Each
// base produces both a _path and a _url helper. The namespace (snake_case)
// prefixes the resource segment; new_/edit_ prefix the whole helper, matching
// Rails' generated names (new_admin_order_path).
func (w *walker) emitResourceRouteHelpers(name string, singular bool, controller string, line int) error {
	nsPrefix := ""
	if len(w.routeNSRaw) > 0 {
		nsPrefix = strings.Join(w.routeNSRaw, "_") + "_"
	}

	var sing string
	if singular {
		sing = name // `resource :profile` — name is already singular
	} else {
		sing = singularize(name)
	}

	helpers := []routeHelper{
		{nsPrefix + sing, "show"}, // member
		{"new_" + nsPrefix + sing, "new"},
		{"edit_" + nsPrefix + sing, "edit"},
	}
	if !singular {
		// Plural resources also generate a collection helper (orders_path).
		helpers = append([]routeHelper{{nsPrefix + name, "index"}}, helpers...)
	}

	for _, h := range helpers {
		for _, suffix := range [...]string{"_path", "_url"} {
			if err := w.emitRouteHelper(h.base+suffix, controller+"#"+h.action, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitRouteHelper emits one synthetic route:* symbol (deduped per file) and an
// edge from it to the controller action it routes to.
//
// Note: like any symbol, a route:* symbol enters the resolver's by-name index,
// so bare unqualified calls elsewhere in the routes file can fall back onto it,
// giving it spurious sub-floor (<0.5) outgoing edges. Those are filtered from
// graph/blast output at query time (the confidence floor); the one real,
// convention-confidence edge is the controller-action edge emitted here.
func (w *walker) emitRouteHelper(helperName, actionTarget string, line int) error {
	qualified := extract.PrefixRoute + helperName
	if !w.emittedSynthetics[qualified] {
		w.emittedSynthetics[qualified] = true
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       helperName,
			Qualified:  qualified,
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  line,
			LineEnd:    line,
		}); err != nil {
			return err
		}
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: qualified,
		TargetQualified: actionTarget,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// handleVerbRoute handles `get "/path", to: "controller#action"` style routes.
func (w *walker) handleVerbRoute(n *sitter.Node) error {
	args := n.ChildByFieldName("arguments")
	if args == nil {
		return nil
	}

	target := findKeywordArg(args, "to", w.source)
	if target == "" || !strings.Contains(target, "#") {
		return nil
	}

	// "pages#home" → "PagesController#home"
	parts := strings.SplitN(target, "#", 2)
	controller := pascalCase(parts[0]) + "Controller"
	if len(w.routeNS) > 0 {
		controller = strings.Join(w.routeNS, "::") + "::" + controller
	}
	resolved := controller + "#" + parts[1]

	line := extract.Line(n.StartPosition())
	if err := w.emit.Edge(extract.EmittedEdge{
		SourceQualified: "routes",
		TargetQualified: resolved,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	}); err != nil {
		return err
	}

	// `as: :foo` names a route helper (foo_path / foo_url) pointing at the same
	// action. Without `as:`, a verb route's helper name is derived from the
	// path and is too irregular to reconstruct safely, so we emit none.
	if as := keywordSymbolOrString(args, "as", w.source); as != "" {
		nsPrefix := ""
		if len(w.routeNSRaw) > 0 {
			nsPrefix = strings.Join(w.routeNSRaw, "_") + "_"
		}
		for _, suffix := range [...]string{"_path", "_url"} {
			if err := w.emitRouteHelper(nsPrefix+as+suffix, resolved, line); err != nil {
				return err
			}
		}
	}
	return nil
}

// keywordSymbolOrString reads a keyword argument whose value is a simple symbol
// (`as: :foo`) or a string (`as: "foo"`), returning the bare name. Unlike
// findKeywordArg it accepts symbol values, which is the common form for `as:`.
func keywordSymbolOrString(args *sitter.Node, key string, source []byte) string {
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "pair" {
			continue
		}
		k := arg.ChildByFieldName("key")
		v := arg.ChildByFieldName("value")
		if k == nil || v == nil || extract.Text(k, source) != key {
			continue
		}
		if v.Kind() == "simple_symbol" {
			return strings.TrimPrefix(extract.Text(v, source), ":")
		}
		return extractStringValue(v, source)
	}
	return ""
}

// handleRouteNamespace processes `namespace :admin do ... end` by pushing
// the namespace onto routeNS, walking the block, then popping.
func (w *walker) handleRouteNamespace(n *sitter.Node) error {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "simple_symbol" {
		return nil
	}
	raw := strings.TrimPrefix(extract.Text(first, w.source), ":")
	ns := pascalCase(raw)

	w.routeNS = append(w.routeNS, ns)
	w.routeNSRaw = append(w.routeNSRaw, raw)
	defer func() {
		w.routeNS = w.routeNS[:len(w.routeNS)-1]
		w.routeNSRaw = w.routeNSRaw[:len(w.routeNSRaw)-1]
	}()

	if block := n.ChildByFieldName("block"); block != nil {
		return w.walkChildren(block, nil)
	}
	return nil
}

// findKeywordArg scans an argument list for a hash pair with the given
// key name and returns the string value. Tree-sitter-ruby represents
// hash_key_symbol keys as bare text (e.g. "class_name" for `class_name:`).
func findKeywordArg(args *sitter.Node, key string, source []byte) string {
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "pair" {
			continue
		}
		k := arg.ChildByFieldName("key")
		v := arg.ChildByFieldName("value")
		if k == nil || v == nil {
			continue
		}
		if extract.Text(k, source) == key {
			return extractStringValue(v, source)
		}
	}
	return ""
}

// findKeywordConstantArg scans an argument list for a hash pair with
// the given key and returns the value when it's a constant or scope
// resolution (e.g. `serializer: TopicViewDetailsSerializer`).
func findKeywordConstantArg(args *sitter.Node, key string, source []byte) string {
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg == nil || arg.Kind() != "pair" {
			continue
		}
		k := arg.ChildByFieldName("key")
		v := arg.ChildByFieldName("value")
		if k == nil || v == nil {
			continue
		}
		if extract.Text(k, source) != key {
			continue
		}
		switch v.Kind() {
		case "constant", "scope_resolution":
			return extract.Text(v, source)
		}
	}
	return ""
}

// extractStringValue pulls the text from a string literal node.
func extractStringValue(n *sitter.Node, source []byte) string {
	if n.Kind() == "string" {
		count := n.NamedChildCount()
		for i := uint(0); i < count; i++ {
			c := n.NamedChild(i)
			if c != nil && c.Kind() == "string_content" {
				return extract.Text(c, source)
			}
		}
	}
	return ""
}
