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
	"fmt"
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
	return nil
}

func init() { extract.Register(Extractor{}) }

// collectionMethods is the set of method names for which block parameter
// type inference is supported. When a call with a block uses one of these
// methods, the block parameter inherits the receiver's element type.
var collectionMethods = map[string]bool{
	"each": true, "map": true, "select": true,
	"reject": true, "find": true, "detect": true,
	"flat_map": true, "collect": true, "filter": true,
}

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

	inScope := len(scope) > 0

	// Class-body-level handlers (require enclosing class/module)
	if inScope {
		switch methodName {
		case "include", "extend", "prepend":
			return false, w.emitIncludeEdge(n, scope)
		case "has_many", "has_and_belongs_to_many", "belongs_to", "has_one":
			return false, w.emitAssociationEdge(n, scope, methodName)
		case "broadcasts_to", "broadcasts":
			return false, w.emitBroadcastEdge(n, scope)
		case "scope":
			return false, w.emitScopeEdge(n, scope)
		}
		if model.RailsCallbackNames[methodName] {
			return false, w.emitCallbackEdges(n, scope, methodName)
		}
	}

	// Top-level handlers (no enclosing class/module)
	if !inScope {
		switch methodName {
		case "resources":
			return true, w.handleResources(n, false)
		case "resource":
			return true, w.handleResources(n, true)
		case "namespace":
			return true, w.handleRouteNamespace(n)
		case "pin":
			return false, w.emitImportmapPin(n)
		case "pin_all_from":
			return false, w.emitImportmapPinAll(n)
		}
		if routeVerbs[methodName] {
			return true, w.handleVerbRoute(n)
		}
	}

	// RSpec DSL blocks with a body — handled as test scopes.
	if rspecDSLMethods[methodName] {
		return w.handleTestBlock(n, scope, methodName)
	}

	return false, nil
}

// rspecDSLMethods is the set of RSpec DSL method names that create
// test scopes when called with a block.
var rspecDSLMethods = map[string]bool{
	"it": true, "describe": true, "context": true,
	"before": true, "after": true, "around": true,
	"let": true, "expect": true,
}

// rspecMatcherMethods is the set of RSpec matcher chain methods that
// should not be emitted as calls edges — they are DSL sugar, not
// application method invocations.
var rspecMatcherMethods = map[string]bool{
	"to": true, "not_to": true, "to_not": true,
	"eq": true, "be": true, "be_nil": true, "be_empty": true,
	"be_valid": true, "be_present": true, "be_a": true,
	"raise_error": true, "change": true, "receive": true,
	"have_received": true, "match": true, "include": true,
	"contain_exactly": true, "start_with": true, "end_with": true,
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

// handleTestBlock processes an RSpec DSL call that has a block body.
// It builds a synthetic scope name, walks the block for calls/identifiers,
// and recurses into nested test blocks.  Returns true to signal the node
// was consumed.
func (w *walker) handleTestBlock(n *sitter.Node, scope []string, methodName string) (bool, error) {
	// Emit the conventional tests edge for top-level describe with a
	// constant argument (e.g. `describe Order do … end`).
	if methodName == "describe" && len(scope) == 0 {
		if err := w.emitDescribeEdge(n); err != nil {
			return true, err
		}
	}

	block := getBlockChild(n)
	if block == nil {
		// No block — but the arguments may contain calls we still want to
		// capture (e.g. `expect(TopicCreator.create(...))`).
		synthetic := w.buildSyntheticSource(scope)
		return true, extract.WalkNamedDescendants(n, "call", func(c *sitter.Node) error {
			if w.isInsideNestedTestBlock(c, n) {
				return nil
			}
			methodNode := c.ChildByFieldName("method")
			if methodNode != nil && rspecDSLMethods[extract.Text(methodNode, w.source)] {
				return nil
			}
			return w.emitTestCall(c, synthetic, scope)
		})
	}

	segment := w.buildTestScopeSegment(n, methodName)
	if segment == "" {
		// Unnamed / unresolvable block — fall back to file-level scope.
		return true, w.walkTestBlockWithFallback(block, scope)
	}

	// Push segment and cap depth at 3.
	w.testScope = append(w.testScope, segment)
	if len(w.testScope) > 3 {
		w.testScope = w.testScope[:len(w.testScope)-1]
		return true, w.walkTestBlockWithFallback(block, scope)
	}
	defer func() {
		w.testScope = w.testScope[:len(w.testScope)-1]
	}()

	synthetic := w.buildSyntheticSource(scope)
	body := getBlockBody(block)
	if body == nil {
		return true, nil
	}
	return true, w.walkTestBody(body, scope, synthetic)
}

// buildTestScopeSegment extracts a scope segment from a test DSL call.
// Returns "" when the block should fall back to file-level scope.
func (w *walker) buildTestScopeSegment(n *sitter.Node, methodName string) string {
	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	first := args.NamedChild(0)
	if first == nil {
		return ""
	}

	switch methodName {
	case "describe", "context":
		switch first.Kind() {
		case "constant", "scope_resolution":
			return extract.Text(first, w.source)
		case "string":
			if hasInterpolation(first) {
				return ""
			}
			desc := extractStringValue(first, w.source)
			if desc == "" {
				return ""
			}
			if strings.HasPrefix(desc, "#") || strings.HasPrefix(desc, ".") {
				return desc
			}
			return methodName + "_" + sanitizeDesc(desc)
		default:
			return ""
		}
	case "it":
		if first.Kind() == "string" {
			if hasInterpolation(first) {
				return ""
			}
			desc := extractStringValue(first, w.source)
			if desc == "" {
				return ""
			}
			return "#it_" + sanitizeDesc(desc)
		}
		return ""
	case "before", "after", "around", "let", "expect":
		// These DSL nodes rarely carry a descriptive string arg;
		// fall back to file-level scope.
		return ""
	default:
		return ""
	}
}

// buildSyntheticSource joins the class/module scope with the test-scope
// stack into a single synthetic qualified name.
func (w *walker) buildSyntheticSource(scope []string) string {
	classScope := strings.Join(scope, "::")
	testScope := strings.Join(w.testScope, "#")

	if classScope == "" && testScope == "" {
		return ""
	}
	if classScope == "" {
		return testScope
	}
	if testScope == "" {
		return classScope
	}
	return classScope + "#" + testScope
}

// walkTestBody walks a test block body emitting calls edges with the
// given synthetic source.  It also recurses into nested test blocks.
// A single tree walk handles both emission and recursion.
func (w *walker) walkTestBody(body *sitter.Node, scope []string, source string) error {
	if err := extract.WalkNamedDescendants(body, "call", func(c *sitter.Node) error {
		if w.isInsideNestedTestBlock(c, body) {
			return nil
		}
		methodNode := c.ChildByFieldName("method")
		if methodNode == nil {
			return nil
		}
		methodName := extract.Text(methodNode, w.source)
		if rspecDSLMethods[methodName] {
			// Recurse into nested test block.
			_, err := w.handleTestBlock(c, scope, methodName)
			return err
		}
		return w.emitTestCall(c, source, scope)
	}); err != nil {
		return err
	}
	// Emit edges for bare identifiers in statement and value positions.
	return w.emitBareIdentifierCalls(body, source, extract.ConfidenceTests, nil)
}

// isInsideNestedTestBlock returns true if n sits inside a test DSL call
// that is a descendant of body (i.e., a nested test block).
func (w *walker) isInsideNestedTestBlock(n, body *sitter.Node) bool {
	bodyID := body.Id()
	for p := n.Parent(); p != nil && p.Id() != bodyID; p = p.Parent() {
		if p.Kind() == "call" {
			methodNode := p.ChildByFieldName("method")
			if methodNode != nil && rspecDSLMethods[extract.Text(methodNode, w.source)] {
				return true
			}
		}
	}
	return false
}

// walkTestBlockWithFallback walks a test block body using the file path
// as the source scope (file-level fallback).
func (w *walker) walkTestBlockWithFallback(block *sitter.Node, scope []string) error {
	body := getBlockBody(block)
	if body == nil {
		return nil
	}
	source := w.fileLevelScope(block)
	return w.walkTestBody(body, scope, source)
}

// fileLevelScope returns a file-level synthetic scope like "test.rb#L42".
func (w *walker) fileLevelScope(n *sitter.Node) string {
	base := w.filePath
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	line := extract.Line(n.StartPosition())
	return fmt.Sprintf("%s#L%d", base, line)
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

// extractBlockParams pulls simple identifier parameter names from a
// block_parameters node.
//
// Returns nil when:
//   - the block has no parameters node (e.g. bare block without |...|)
//   - any parameter is not a simple identifier (destructuring, splat,
//     optional, etc.)
//
// In both cases the caller should skip block parameter type inference
// and fall back to walking the block body with the original local type
// map.
func extractBlockParams(block *sitter.Node, source []byte) []string {
	paramsNode := block.ChildByFieldName("parameters")
	if paramsNode == nil {
		return nil
	}
	var params []string
	count := paramsNode.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := paramsNode.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "identifier":
			params = append(params, extract.Text(c, source))
		default:
			// Destructuring, splat, optional parameters — skip the whole block.
			return nil
		}
	}
	return params
}

// extractElementType extracts the element type from a collection type string.
// Handles Array[Type] syntax and falls back to a plural→singular heuristic.
func extractElementType(collectionType string) string {
	if collectionType == "" {
		return ""
	}
	// Array[Order] → Order
	if strings.HasPrefix(collectionType, "Array[") && strings.HasSuffix(collectionType, "]") {
		return collectionType[6 : len(collectionType)-1]
	}
	// Plural → singular heuristic (e.g. orders → Order, users → User)
	singular := singularize(collectionType)
	if singular != collectionType {
		return pascalCase(singular)
	}
	return collectionType
}

// mergeMaps returns a new map containing all entries from base, overlaid
// with entries from overlay. Neither input map is modified.
func mergeMaps(base, overlay map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overlay {
		result[k] = v
	}
	return result
}

// hasInterpolation returns true if a string node contains interpolation.
func hasInterpolation(n *sitter.Node) bool {
	if n.Kind() != "string" {
		return false
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c != nil && c.Kind() == "interpolation" {
			return true
		}
	}
	return false
}

// sanitizeDesc turns a human-readable block description into a safe
// scope segment: spaces → underscores, strip non-alphanumerics.
func sanitizeDesc(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		default:
			// Drop punctuation.
		}
	}
	return b.String()
}

// handleClassOrModule emits the symbol, records inheritance (class only),
// and descends into the body with the class/module pushed onto the scope.
func (w *walker) handleClassOrModule(n *sitter.Node, scope []string, kind model.SymbolKind) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return w.walkChildren(n, scope) // malformed, keep walking
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return w.walkChildren(n, scope)
	}

	// A::B as a class name pushes both segments; the last segment is the
	// "name" (what grep will find), the full chain is the qualified name.
	segments := strings.Split(name, "::")
	newScope := append(slices.Clone(scope), segments...)
	qualified := strings.Join(newScope, "::")
	parent := strings.Join(scope, "::")

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            segments[len(segments)-1],
		Qualified:       qualified,
		Kind:            kind,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	// Inheritance: emit an edge when the superclass is a simple constant.
	// Target resolution to a symbol_id happens at write time — here we
	// just record the target's qualified name.
	if kind == model.KindClass {
		if sup := n.ChildByFieldName("superclass"); sup != nil {
			if target := superclassName(sup, w.source); target != "" {
				line := extract.Line(sup.StartPosition())
				if err := w.emit.Edge(extract.EmittedEdge{
					SourceQualified: qualified,
					TargetQualified: target,
					Kind:            model.EdgeInherits,
					Line:            &line,
					Confidence:      1.0,
				}); err != nil {
					return err
				}
				if isTestSuperclass(target) {
					if testedClass := inferTestedClass(qualified); testedClass != "" {
						if err := w.emit.Edge(extract.EmittedEdge{
							SourceQualified: qualified,
							TargetQualified: testedClass,
							Kind:            model.EdgeTests,
							Line:            &line,
							Confidence:      extract.ConfidenceConvention,
						}); err != nil {
							return err
						}
					}
				}
			}
		}
	}

	if body := n.ChildByFieldName("body"); body != nil {
		ivarTypes := buildInstanceVarTypeMap(body, w.source)
		w.classInstanceVars[qualified] = ivarTypes
		// Pre-pass: record each direct instance method's visibility before the
		// methods are emitted, so handleMethod can attach it. Mirrors the
		// ivar-type pre-pass above.
		w.recordBodyVisibility(body, qualified)
		if err := w.walkChildren(body, newScope); err != nil {
			return err
		}
		// Capture references that live at class-body level rather than
		// inside a method: constant RHS values, and exception/handler
		// classes named by DSLs like `rescue_from Foo` / `retry_on Bar`.
		// Method-body references are attributed to their method, so they
		// are skipped here via the nested-def guard.
		return w.collectConstRefs(body, qualified, true)
	}
	return nil
}

// handleMethod emits a method symbol qualified either as Class#name
// (instance) or Class.name (singleton). For top-level methods the
// separator and parent are both empty — they become KindMethod with
// qualified=name, which matches how Ruby treats top-level defs (they
// get attached to Object at runtime, but we don't model Object here).
//
// After emitting, the body is walked for call nodes so intra-body
// calls land as calls edges.
func (w *walker) handleMethod(n *sitter.Node, scope []string, singleton bool) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, w.source)
	if name == "" {
		return nil
	}

	parent := strings.Join(scope, "::")
	var qualified, receiver string
	switch {
	case parent == "":
		// Top-level def: attaches to Object at runtime; no class-vs-instance
		// dispatch distinction to record.
		qualified = name
	case singleton:
		qualified = parent + "." + name
		receiver = extract.ReceiverSingleton
	default:
		qualified = parent + "#" + name
		receiver = extract.ReceiverInstance
	}

	// Visibility comes from the per-class pre-pass (instance methods only).
	// Anything not recorded — singleton methods, top-level defs, methods the
	// pre-pass could not classify — defaults to public, which is the safe
	// direction: a public method can never earn a `dead` verdict.
	visibility := "public"
	if v, ok := w.methodVisibility[qualified]; ok {
		visibility = v
	}

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       qualified,
		Kind:            model.KindMethod,
		Receiver:        receiver,
		Visibility:      visibility,
		ParentQualified: parent,
		LineStart:       extract.Line(n.StartPosition()),
		LineEnd:         extract.Line(n.EndPosition()),
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}
	// Include / extend / prepend calls inside a method body come through
	// here too and are emitted as regular calls — the `includes` edges
	// are produced separately at the class-body level in handleIncludeCall,
	// and a dynamic include at runtime is rare enough that a bare calls
	// edge is an accurate record of what was written.
	body := n.ChildByFieldName("body")
	localTypes := buildLocalTypeMap(body, w.source)
	w.addRescueBindings(body, localTypes)
	ivarTypes := w.classInstanceVars[strings.Join(scope, "::")]
	if err := extract.WalkNamedDescendants(body, "call", func(c *sitter.Node) error {
		if isInsideBlock(c) {
			return nil
		}
		return w.emitCall(c, qualified, scope, localTypes, ivarTypes)
	}); err != nil {
		return err
	}
	if err := w.emitBareIdentifierCalls(body, qualified, extract.ConfidenceDynamic, methodParamNames(n, w.source)); err != nil {
		return err
	}
	return w.collectConstRefs(body, qualified, false)
}

// emitCall produces a calls edge for one `call` node. The target is
// resolved from the receiver when possible: `self` and implicit calls
// are emitted as `self.name` so the resolver rewrites them to the
// enclosing class; constant receivers are emitted as `Const.name` for
// exact matching; local-variable receivers are resolved via a lightweight
// intra-method type map built from `X = Class.new` assignments; method
// chains are stripped to the trailing method name. `send` /
// `public_send` / `__send__` with a literal symbol or string first
// argument is emitted with confidence 0.7; anything else in that family
// is skipped.
func (w *walker) emitCall(n *sitter.Node, source string, scope []string, localTypes, ivarTypes map[string]string) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	methodName := extract.Text(methodNode, w.source)
	if methodName == "" {
		return nil
	}

	switch methodName {
	case "send", "public_send", "__send__":
		target, ok := literalSendTarget(n, w.source)
		if ok {
			line := extract.Line(n.StartPosition())
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: source,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &line,
				Confidence:      extract.ConfidenceDynamic,
			})
		}
		// Heuristic: variable-based dynamic dispatch on self.
		recv := n.ChildByFieldName("receiver")
		if recv == nil || recv.Kind() == "self" {
			if target, conf, ok := inferSendTargetFromVariable(n, w.source); ok {
				line := extract.Line(n.StartPosition())
				return w.emit.Edge(extract.EmittedEdge{
					SourceQualified: source,
					TargetQualified: target,
					Kind:            model.EdgeCalls,
					Line:            &line,
					Confidence:      conf,
				})
			}
		}
		return nil
	}

	recv := n.ChildByFieldName("receiver")
	target, confidence := w.resolveCallTarget(recv, methodName, scope, localTypes, ivarTypes)
	if target == "" {
		return nil
	}
	return w.emitCallWithConfidence(n, source, scope, localTypes, ivarTypes, confidence)
}

// emitTestCall delegates to emitCall but substitutes ConfidenceTests and
// skips RSpec matcher noise (eq, be_valid, raise_error, etc.).
func (w *walker) emitTestCall(n *sitter.Node, source string, scope []string) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	if rspecMatcherMethods[extract.Text(methodNode, w.source)] {
		return nil
	}
	return w.emitCallWithConfidence(n, source, scope, nil, nil, extract.ConfidenceTests)
}

// emitCallWithConfidence is emitCall's core logic with an injectable
// confidence value. Used by both production-method emission (1.0 / 0.7)
// and test-block emission (0.8).
func (w *walker) emitCallWithConfidence(n *sitter.Node, source string, scope []string, localTypes, ivarTypes map[string]string, confidence float64) error {
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	methodName := extract.Text(methodNode, w.source)
	if methodName == "" {
		return nil
	}
	line := extract.Line(n.StartPosition())

	switch methodName {
	case "send", "public_send", "__send__":
		target, ok := literalSendTarget(n, w.source)
		if ok {
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: source,
				TargetQualified: target,
				Kind:            model.EdgeCalls,
				Line:            &line,
				Confidence:      confidence,
			})
		}
		// Heuristic: variable-based dynamic dispatch on self.
		recv := n.ChildByFieldName("receiver")
		if recv == nil || recv.Kind() == "self" {
			if target, conf, ok := inferSendTargetFromVariable(n, w.source); ok {
				return w.emit.Edge(extract.EmittedEdge{
					SourceQualified: source,
					TargetQualified: target,
					Kind:            model.EdgeCalls,
					Line:            &line,
					Confidence:      conf,
				})
			}
		}
		return nil
	}

	recv := n.ChildByFieldName("receiver")
	target, _ := w.resolveCallTarget(recv, methodName, scope, localTypes, ivarTypes)
	if target == "" {
		return nil
	}
	if err := w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      confidence,
	}); err != nil {
		return err
	}

	// If this call has a block, infer block parameter types from the
	// receiver's collection type and walk the block body with an
	// augmented local type map. When inference is not possible (non-
	// collection method, destructuring params, unknown receiver type),
	// we still walk the block so calls inside are not lost.
	if block := n.ChildByFieldName("block"); block != nil {
		paramTypes := w.inferBlockParamTypes(n, scope, localTypes, ivarTypes)
		blockTypes := localTypes
		if paramTypes != nil {
			blockTypes = mergeMaps(localTypes, paramTypes)
		}
		if err := extract.WalkNamedDescendants(block, "call", func(c *sitter.Node) error {
			if isInsideNestedBlock(c, block) {
				return nil
			}
			return w.emitCall(c, source, scope, blockTypes, ivarTypes)
		}); err != nil {
			return err
		}
	}
	return nil
}

// coreNoiseMethods are ubiquitous core-Ruby / ActiveSupport methods
// whose bare name (receiver type unknown) matches far too many unrelated
// symbols. When the receiver type cannot be inferred we drop the call
// edge rather than let the resolver's unqualified fallback bind it to an
// arbitrary same-named method elsewhere — e.g. `count.zero?` resolving
// to a `Money.zero` singleton. A missing low-confidence edge is cheaper
// than a false caller that inflates blast radius. The ERB analogue, for
// framework context accessors referenced bare in a template, is
// erbHelperSkip in internal/extract/erb/erb.go.
var coreNoiseMethods = map[string]bool{
	"zero?": true, "positive?": true, "negative?": true, "nonzero?": true,
	"present?": true, "blank?": true, "nil?": true, "empty?": true,
	"to_s": true, "to_i": true, "to_a": true, "to_h": true, "to_sym": true,
	"freeze": true, "dup": true, "clone": true, "presence": true, "call": true,
	// Object/reflection methods: a bare `x.is_a?` / `x.respond_to?` on an
	// unknown receiver otherwise binds to a coincidental same-named app symbol
	// (e.g. a test fake's `#is_a?`). Never a meaningful caller edge.
	"is_a?": true, "kind_of?": true, "instance_of?": true, "respond_to?": true,
	"frozen?": true, "itself": true, "inspect": true, "object_id": true,
}

// unresolvedCall is the emit decision for a call whose receiver type is
// unknown: drop ubiquitous core-method names, otherwise emit the bare
// name at ConfidenceUnresolved for the resolver's unqualified fallback.
func unresolvedCall(methodName string) (string, float64) {
	if coreNoiseMethods[methodName] {
		return "", 0
	}
	return methodName, extract.ConfidenceUnresolved
}

// resolveCallTarget decides what target string to emit for a call node.
// It returns the target string and the confidence level.
func (w *walker) resolveCallTarget(recv *sitter.Node, methodName string, scope []string, localTypes, ivarTypes map[string]string) (string, float64) {
	if recv == nil {
		return "self." + methodName, 1.0
	}

	switch recv.Kind() {
	case "self":
		return "self." + methodName, 1.0
	case "constant", "scope_resolution":
		if recvText := strings.TrimSpace(extract.Text(recv, w.source)); recvText != "" {
			return recvText + "." + methodName, 1.0
		}
	case "identifier":
		name := extract.Text(recv, w.source)
		if typ, ok := localTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	case "instance_variable":
		name := extract.Text(recv, w.source)
		if typ, ok := localTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		if typ, ok := ivarTypes[name]; ok {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	case "call":
		// Method chain — strip to the trailing method unless the inner
		// call is `.new` on a constant or `self`, in which case we can
		// infer the result type is an instance of that class.
		if typ := typeFromNewCall(recv, w.source, scope); typ != "" {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		// Try to resolve multi-hop chains via return-type map.
		if typ := w.resolveChainReceiver(recv, scope, localTypes, 1); typ != "" {
			return typ + "#" + methodName, extract.ConfidenceDynamic
		}
		return unresolvedCall(methodName)
	}

	return unresolvedCall(methodName)
}

// resolveChainReceiver recursively resolves a call-chain receiver to a type
// by looking up local-variable types and method return types. It caps at 3
// hops to avoid exponential lookups and absurd qualified names.
func (w *walker) resolveChainReceiver(recv *sitter.Node, scope []string, localTypes map[string]string, depth int) string {
	if depth > 3 || recv == nil || recv.Kind() != "call" {
		return ""
	}
	methodNode := recv.ChildByFieldName("method")
	if methodNode == nil {
		return ""
	}
	methodName := extract.Text(methodNode, w.source)
	innerRecv := recv.ChildByFieldName("receiver")

	// Case 1: receiver is a local variable with known type.
	if innerRecv != nil && innerRecv.Kind() == "identifier" {
		if typ, ok := localTypes[extract.Text(innerRecv, w.source)]; ok {
			qualified := typ + "#" + methodName
			if ret, ok := w.returnTypes[qualified]; ok {
				return ret
			}
		}
	}

	// Case 2: receiver is self (implicit or explicit).
	if innerRecv == nil || innerRecv.Kind() == "self" {
		parent := strings.Join(scope, "::")
		qualified := parent + "#" + methodName
		if ret, ok := w.returnTypes[qualified]; ok {
			return ret
		}
	}

	// Case 3: receiver is another chain (recursive).
	if innerRecv != nil && innerRecv.Kind() == "call" {
		innerType := w.resolveChainReceiver(innerRecv, scope, localTypes, depth+1)
		if innerType != "" {
			qualified := innerType + "#" + methodName
			if ret, ok := w.returnTypes[qualified]; ok {
				return ret
			}
		}
	}

	return ""
}

// inferBlockParamTypes determines the element type for block parameters
// when a call with a block uses a known collection method (each, map, etc.).
// It looks up the receiver's collection type from local variables, instance
// variables, or method chains, then extracts the singular element type.
// Returns nil when the method is not in the whitelist or the type cannot
// be inferred.
func (w *walker) inferBlockParamTypes(callNode *sitter.Node, scope []string, localTypes, ivarTypes map[string]string) map[string]string {
	methodNode := callNode.ChildByFieldName("method")
	if methodNode == nil {
		return nil
	}
	methodName := extract.Text(methodNode, w.source)
	if !collectionMethods[methodName] {
		return nil
	}

	recv := callNode.ChildByFieldName("receiver")
	if recv == nil {
		return nil
	}

	var collectionType string
	switch recv.Kind() {
	case "identifier":
		collectionType = localTypes[extract.Text(recv, w.source)]
	case "instance_variable":
		name := extract.Text(recv, w.source)
		if typ, ok := localTypes[name]; ok {
			collectionType = typ
		} else if typ, ok := ivarTypes[name]; ok {
			collectionType = typ
		}
	case "call":
		// For chain resolution, e.g. user.orders.each { |order| ... }
		collectionType = w.resolveChainReceiver(recv, scope, localTypes, 1)
	}

	if collectionType == "" {
		return nil
	}

	elementType := extractElementType(collectionType)
	if elementType == "" {
		return nil
	}

	block := callNode.ChildByFieldName("block")
	if block == nil {
		return nil
	}
	params := extractBlockParams(block, w.source)
	if params == nil {
		return nil
	}

	result := make(map[string]string)
	for _, param := range params {
		result[param] = elementType
	}
	return result
}

// buildLocalTypeMap scans a method body for local-variable assignments
// whose RHS is `ClassName.new(...)` and returns a map from variable name
// to class name. This enables lightweight receiver resolution for the
// most common object-creation pattern in Ruby.
func buildLocalTypeMap(body *sitter.Node, source []byte) map[string]string {
	types := make(map[string]string)
	if body == nil {
		return types
	}
	for _, kind := range []string{"assignment", "operator_assignment"} {
		_ = extract.WalkNamedDescendants(body, kind, func(n *sitter.Node) error {
			lhs := n.ChildByFieldName("left")
			rhs := n.ChildByFieldName("right")
			if lhs == nil || rhs == nil || lhs.Kind() != "identifier" {
				return nil
			}
			if typ := typeFromNewCall(rhs, source, nil); typ != "" {
				types[extract.Text(lhs, source)] = typ
			}
			return nil
		})
	}
	return types
}

// addRescueBindings records the type of each `rescue ExceptionType => var`
// clause in the method body into localTypes, so a call on the bound variable
// (`raise if e.retriable?`) resolves to ExceptionType#method instead of the
// resolver's bare-name fallback, which would otherwise bind it to an arbitrary
// same-named method on an unrelated class.
//
// Only single-type rescues with an identifier variable are recorded:
// `rescue A, B => e` leaves the variable's type ambiguous, and a bare
// `rescue => e` has no type. An existing entry (e.g. an explicit
// `X = Class.new`) is never overwritten. The map is method-wide rather than
// rescue-scoped, so two rescues binding the same name to different types are
// last-write-wins — rare, and a missed/loose bind only costs one weak edge.
func (w *walker) addRescueBindings(body *sitter.Node, localTypes map[string]string) {
	if body == nil {
		return
	}
	_ = extract.WalkNamedDescendants(body, "rescue", func(r *sitter.Node) error {
		typ := w.singleRescueType(r)
		if typ == "" {
			return nil
		}
		varName := rescueVariableName(r, w.source)
		if varName == "" {
			return nil
		}
		if _, exists := localTypes[varName]; !exists {
			localTypes[varName] = typ
		}
		return nil
	})
}

// singleRescueType returns the fully-qualified exception type of a rescue
// clause when it names exactly one constant/scope_resolution, resolving the
// trailing segment through pkgBindings so `rescue ApiError` inside its own
// namespace still yields the qualified name. Returns "" for multi-type or
// typeless rescues.
func (w *walker) singleRescueType(rescueNode *sitter.Node) string {
	var exceptions *sitter.Node
	for i := uint(0); i < rescueNode.NamedChildCount(); i++ {
		if c := rescueNode.NamedChild(i); c.Kind() == "exceptions" {
			exceptions = c
			break
		}
	}
	if exceptions == nil || exceptions.NamedChildCount() != 1 {
		return ""
	}
	typeNode := exceptions.NamedChild(0)
	switch typeNode.Kind() {
	case "constant", "scope_resolution":
	default:
		return ""
	}
	text := strings.TrimSpace(extract.Text(typeNode, w.source))
	if text == "" {
		return ""
	}
	if q, ok := w.pkgBindings[lastConstSegment(text)]; ok {
		return q
	}
	return text
}

// rescueVariableName returns the identifier bound by a rescue clause's
// `=> var`, or "" when the binding is absent or not a simple identifier.
func rescueVariableName(rescueNode *sitter.Node, source []byte) string {
	for i := uint(0); i < rescueNode.NamedChildCount(); i++ {
		c := rescueNode.NamedChild(i)
		if c.Kind() != "exception_variable" {
			continue
		}
		for j := uint(0); j < c.NamedChildCount(); j++ {
			if v := c.NamedChild(j); v.Kind() == "identifier" {
				return extract.Text(v, source)
			}
		}
	}
	return ""
}

// lastConstSegment returns the trailing `::`-separated segment of a constant
// reference: "SocialCommerce::Meta::ApiError" → "ApiError", "ApiError" → "ApiError".
func lastConstSegment(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		return name[i+len("::"):]
	}
	return name
}

// buildInstanceVarTypeMap scans a class body for `initialize` methods and
// looks for instance-variable assignments whose RHS is `ClassName.new(...)`.
// Returns a map from @ivar_name to class_name.
func buildInstanceVarTypeMap(body *sitter.Node, source []byte) map[string]string {
	types := make(map[string]string)
	if body == nil {
		return types
	}
	_ = extract.WalkNamedDescendants(body, "method", func(n *sitter.Node) error {
		nameNode := n.ChildByFieldName("name")
		if nameNode == nil || extract.Text(nameNode, source) != "initialize" {
			return nil
		}
		initBody := n.ChildByFieldName("body")
		if initBody == nil {
			return nil
		}
		for _, kind := range []string{"assignment", "operator_assignment"} {
			_ = extract.WalkNamedDescendants(initBody, kind, func(a *sitter.Node) error {
				lhs := a.ChildByFieldName("left")
				rhs := a.ChildByFieldName("right")
				if lhs == nil || rhs == nil || lhs.Kind() != "instance_variable" {
					return nil
				}
				if typ := typeFromNewCall(rhs, source, nil); typ != "" {
					types[extract.Text(lhs, source)] = typ
				}
				return nil
			})
		}
		return nil
	})
	return types
}

// typeFromNewCall returns the receiver class name when the node is a
// call to `.new`, e.g. `TopicCreator.new(...)` → `"TopicCreator"`.
// When the receiver is `self`, the enclosing class scope is used.
func typeFromNewCall(n *sitter.Node, source []byte, scope []string) string {
	if n == nil || n.Kind() != "call" {
		return ""
	}
	methodNode := n.ChildByFieldName("method")
	if methodNode == nil || extract.Text(methodNode, source) != "new" {
		return ""
	}
	recv := n.ChildByFieldName("receiver")
	if recv == nil {
		return ""
	}
	switch recv.Kind() {
	case "constant", "scope_resolution":
		return extract.Text(recv, source)
	case "self":
		return strings.Join(scope, "::")
	}
	return ""
}

// buildReturnTypeMap scans a class/module body for methods whose body is
// a single expression or a single `return` statement that calls `Class.new(...)`.
// It returns a map from qualified method name to the inferred class name.
func buildReturnTypeMap(body *sitter.Node, source []byte, scope []string) map[string]string {
	types := make(map[string]string)
	if body == nil {
		return types
	}
	parent := strings.Join(scope, "::")
	_ = extract.WalkNamedDescendants(body, "method", func(n *sitter.Node) error {
		return recordMethodReturnType(n, source, parent, types, false)
	})
	_ = extract.WalkNamedDescendants(body, "singleton_method", func(n *sitter.Node) error {
		return recordMethodReturnType(n, source, parent, types, true)
	})
	return types
}

// recordMethodReturnType extracts the return type from a single method
// and records it in the map if it matches the simple factory pattern.
func recordMethodReturnType(n *sitter.Node, source []byte, parent string, types map[string]string, singleton bool) error {
	nameNode := n.ChildByFieldName("name")
	if nameNode == nil {
		return nil
	}
	name := extract.Text(nameNode, source)
	if name == "" {
		return nil
	}
	var qualified string
	switch {
	case parent == "":
		qualified = name
	case singleton:
		qualified = parent + "." + name
	default:
		qualified = parent + "#" + name
	}
	body := n.ChildByFieldName("body")
	if body == nil {
		return nil
	}
	// Look for a single expression or single return statement.
	var returnExpr *sitter.Node
	childCount := body.NamedChildCount()
	if childCount == 1 {
		child := body.NamedChild(0)
		if child.Kind() == "return" {
			// return node has an argument_list as its first named child
			if argList := child.NamedChild(0); argList != nil && argList.Kind() == "argument_list" {
				if argList.NamedChildCount() == 1 {
					returnExpr = argList.NamedChild(0)
				}
			}
		} else {
			returnExpr = child
		}
	}
	if returnExpr != nil {
		if typ := typeFromNewCall(returnExpr, source, nil); typ != "" {
			types[qualified] = typ
		}
	}
	return nil
}

// buildFileReturnTypeMap walks the entire AST and builds a map of all
// method qualified names → return types for simple factory methods.
func buildFileReturnTypeMap(root *sitter.Node, source []byte) map[string]string {
	types := make(map[string]string)
	var walk func(n *sitter.Node, scope []string)
	walk = func(n *sitter.Node, scope []string) {
		if n == nil {
			return
		}
		switch n.Kind() {
		case "class", "module":
			nameNode := n.ChildByFieldName("name")
			if nameNode != nil {
				name := extract.Text(nameNode, source)
				if name != "" {
					segments := strings.Split(name, "::")
					newScope := append(slices.Clone(scope), segments...)
					body := n.ChildByFieldName("body")
					if body != nil {
						classTypes := buildReturnTypeMap(body, source, newScope)
						for k, v := range classTypes {
							types[k] = v
						}
						// Continue walking children with new scope.
						count := body.NamedChildCount()
						for i := uint(0); i < count; i++ {
							walk(body.NamedChild(i), newScope)
						}
					}
					return
				}
			}
		}
		// For non-class/module nodes, walk children with current scope.
		count := n.NamedChildCount()
		for i := uint(0); i < count; i++ {
			walk(n.NamedChild(i), scope)
		}
	}
	walk(root, nil)
	return types
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

// literalSendTarget extracts the target name from `send(:name)` /
// `public_send("name")` / `__send__(:name)` when the first argument is
// a bare symbol or string literal. Everything else is unresolvable.
// The tree-sitter-ruby grammar exposes a string's payload as a named
// `string_content` child (not a named field), and a symbol node
// carries a leading colon; both are looked up structurally. If the
// grammar shape drifts, we return false visibly rather than falling
// back to quote stripping — explicit failure beats degraded output.
func literalSendTarget(call *sitter.Node, source []byte) (string, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return "", false
	}
	first := args.NamedChild(0)
	if first == nil {
		return "", false
	}
	switch first.Kind() {
	case "simple_symbol":
		return strings.TrimPrefix(extract.Text(first, source), ":"), true
	case "string":
		count := first.NamedChildCount()
		for i := uint(0); i < count; i++ {
			c := first.NamedChild(i)
			if c != nil && c.Kind() == "string_content" {
				return extract.Text(c, source), true
			}
		}
	}
	return "", false
}

// methodNamePatterns lists variable-name substrings that suggest the
// variable holds a method name. Used by the dynamic-dispatch heuristic
// to avoid emitting edges for every variable-based send() call.
var methodNamePatterns = []string{
	"callback", "handler", "method", "action", "hook",
	"event", "listener", "processor", "task", "job",
	"name", "attr",
}

// ConfidenceHeuristicDispatch is the confidence for variable-inferred
// dynamic dispatch edges. Very low — we're guessing the method name from
// a variable assignment, which could be wrong.
const ConfidenceHeuristicDispatch = extract.ConfidenceUnresolved / 2

// findEnclosingMethodBody walks up from n to the nearest "method" node
// and returns its "body" child, or nil if none is found.
func findEnclosingMethodBody(n *sitter.Node) *sitter.Node {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if p.Kind() == "method" || p.Kind() == "singleton_method" {
			return p.ChildByFieldName("body")
		}
	}
	return nil
}

// traceVariableAssignment scans body for assignments to varName that
// appear before the given call node, and returns the literal symbol or
// string value from the RHS of the last such assignment. It only looks
// at direct children (not nested blocks or methods) to keep the heuristic
// simple and fast.
func traceVariableAssignment(body *sitter.Node, varName string, source []byte, call *sitter.Node) (string, bool) {
	if body == nil || call == nil {
		return "", false
	}
	callRow := call.StartPosition().Row
	var result string
	found := false
	for _, kind := range []string{"assignment", "operator_assignment"} {
		_ = extract.WalkNamedDescendants(body, kind, func(n *sitter.Node) error {
			// Skip assignments that appear after the send call.
			if n.StartPosition().Row > callRow {
				return nil
			}
			lhs := n.ChildByFieldName("left")
			rhs := n.ChildByFieldName("right")
			if lhs == nil || rhs == nil {
				return nil
			}
			if lhs.Kind() != "identifier" || extract.Text(lhs, source) != varName {
				return nil
			}
			switch rhs.Kind() {
			case "simple_symbol":
				result = strings.TrimPrefix(extract.Text(rhs, source), ":")
				found = true
			case "string":
				count := rhs.NamedChildCount()
				for i := uint(0); i < count; i++ {
					c := rhs.NamedChild(i)
					if c != nil && c.Kind() == "string_content" {
						result = extract.Text(c, source)
						found = true
						break
					}
				}
			}
			return nil
		})
	}
	return result, found
}

// inferSendTargetFromVariable applies a heuristic to variable-based
// dynamic dispatch: if the first argument to send/public_send/__send__
// is an identifier whose name suggests a method name, and we can trace
// the variable back to a literal symbol or string assignment, return
// the inferred target with very low confidence.
func inferSendTargetFromVariable(call *sitter.Node, source []byte) (string, float64, bool) {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return "", 0, false
	}
	first := args.NamedChild(0)
	if first == nil || first.Kind() != "identifier" {
		return "", 0, false
	}
	varName := extract.Text(first, source)

	// Only apply heuristic when the variable name suggests a method name.
	lowerVar := strings.ToLower(varName)
	matchesPattern := false
	for _, pat := range methodNamePatterns {
		if strings.Contains(lowerVar, pat) {
			matchesPattern = true
			break
		}
	}
	if !matchesPattern {
		return "", 0, false
	}

	body := findEnclosingMethodBody(call)
	if body == nil {
		return "", 0, false
	}
	if target, ok := traceVariableAssignment(body, varName, source, call); ok {
		return target, ConfidenceHeuristicDispatch, true
	}
	return "", 0, false
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

// emitDescribeEdge detects RSpec.describe/describe with a constant
// argument and emits a tests edge to the referenced class.
func (w *walker) emitDescribeEdge(n *sitter.Node) error {
	// For RSpec.describe, the receiver is "RSpec". For bare describe, no receiver.
	// Both are valid — just need the first arg to be a constant.
	if recv := n.ChildByFieldName("receiver"); recv != nil {
		if extract.Text(recv, w.source) != "RSpec" {
			return nil
		}
	}

	args := n.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := args.NamedChild(0)
	if first == nil {
		return nil
	}

	var target string
	switch first.Kind() {
	case "constant":
		target = extract.Text(first, w.source)
	case "scope_resolution":
		target = extract.Text(first, w.source)
	default:
		return nil
	}
	if target == "" {
		return nil
	}

	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: target + "Test",
		TargetQualified: target,
		Kind:            model.EdgeTests,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
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

// isTestSuperclass returns true if the superclass name indicates a test base class.
func isTestSuperclass(name string) bool {
	return strings.Contains(name, "TestCase") || strings.Contains(name, "IntegrationTest") || strings.Contains(name, "SystemTest")
}

// inferTestedClass strips "Test" suffix from a class name to find what it tests.
// "UserTest" → "User", "Admin::DashboardControllerTest" → "Admin::DashboardController"
func inferTestedClass(qualified string) string {
	if strings.HasSuffix(qualified, "Test") {
		return strings.TrimSuffix(qualified, "Test")
	}
	return ""
}

// superclassName pulls the target class name from a `superclass` node.
// The node wraps its target (usually `constant` or `scope_resolution`).
func superclassName(sup *sitter.Node, source []byte) string {
	count := sup.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := sup.NamedChild(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "constant", "scope_resolution":
			return c.Utf8Text(source)
		}
	}
	return ""
}
