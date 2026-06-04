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
