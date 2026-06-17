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
		classSuperclass:   make(map[string]string),
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
	classSuperclass    map[string]string            // class_qualified → superclass name as written, for `super` edges
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
	case "identifier":
		// A bare class-body macro with no arguments (`acts_as_watchable`)
		// parses as an identifier, not a call. Handle the acts_as_* form here;
		// every other identifier falls through unchanged.
		if err := w.handleBareActsAsMacro(n, scope); err != nil {
			return err
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
	if classNameAttributeMacros[methodName] {
		return true, w.emitClassNameAttribute(n, scope)
	}
	if model.RailsCallbackNames[methodName] {
		return true, w.emitCallbackEdges(n, scope, methodName)
	}
	if isActsAsMacro(methodName) {
		return true, w.emitActsAsEdge(n, scope, methodName)
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
