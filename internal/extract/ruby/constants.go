package ruby

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

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
			w.registerConstantAssignment(child, scope)
		case "class", "module":
			w.collectNestedConstants(child, scope)
		}
	}
}

// registerConstantAssignment records a `CONST = value` binding so references
// to CONST resolve to its qualified name. Non-constant assignments are ignored.
func (w *walker) registerConstantAssignment(child *sitter.Node, scope []string) {
	lhs := child.ChildByFieldName("left")
	if lhs == nil || lhs.Kind() != "constant" {
		return
	}
	name := extract.Text(lhs, w.source)
	if name == "" {
		return
	}
	qualified := name
	if parent := strings.Join(scope, "::"); parent != "" {
		qualified = parent + "::" + name
	}
	w.pkgBindings[name] = qualified
}

// collectNestedConstants registers a class/module name binding and recurses
// into its body so nested constants (e.g. MyClass::TIMEOUT) are collected.
func (w *walker) collectNestedConstants(child *sitter.Node, scope []string) {
	nameNode := child.ChildByFieldName("name")
	if nameNode == nil {
		return
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return
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
		return w.emitConstantRef(cn, root, skipNestedDefs, emit)
	}); err != nil {
		return err
	}

	// Namespaced references (`Foo::Bar`, `A::B::CONST`). Emit only the
	// outermost scope resolution; skip superclasses.
	return extract.WalkNamedDescendants(root, "scope_resolution", func(sr *sitter.Node) error {
		return w.emitScopeResolutionRef(sr, root, skipNestedDefs, emit)
	})
}

// constRefSkipped reports whether a constant or scope_resolution reference
// node should be skipped: it sits inside a nested def or a structural DSL
// argument (class-body walks only), or it is the inner segment of a scope
// resolution or a superclass — both of which other edges already record.
func (w *walker) constRefSkipped(node, root *sitter.Node, skipNestedDefs bool) bool {
	if skipNestedDefs && (isInsideNestedDef(node, root) || w.isStructuralDSLArg(node, root)) {
		return true
	}
	if p := node.Parent(); p != nil {
		switch p.Kind() {
		case "scope_resolution", "superclass":
			return true
		}
	}
	return false
}

// emitConstantRef emits a references edge for a bare constant node, skipping
// references filtered by constRefSkipped or that are themselves a constant
// definition (the left-hand side of an assignment). Same-file constants
// resolve through pkgBindings; others emit their surface text.
func (w *walker) emitConstantRef(cn, root *sitter.Node, skipNestedDefs bool, emit func(*sitter.Node, string) error) error {
	if w.constRefSkipped(cn, root, skipNestedDefs) {
		return nil
	}
	if p := cn.Parent(); p != nil {
		switch p.Kind() {
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
}

// emitScopeResolutionRef emits a references edge for a `Foo::Bar` scope
// resolution, skipping references filtered by constRefSkipped.
func (w *walker) emitScopeResolutionRef(sr, root *sitter.Node, skipNestedDefs bool, emit func(*sitter.Node, string) error) error {
	if w.constRefSkipped(sr, root, skipNestedDefs) {
		return nil
	}
	return emit(sr, strings.TrimSpace(extract.Text(sr, w.source)))
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
