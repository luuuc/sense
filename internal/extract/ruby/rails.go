package ruby

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

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

// isActsAsMacro reports whether a class-body method call is an acts_as_*
// plugin mixin macro (acts_as_attachable, acts_as_watchable, acts_as_list, …).
func isActsAsMacro(methodName string) bool {
	return strings.HasPrefix(methodName, "acts_as_")
}

// handleBareActsAsMacro emits the model → macro edge for a no-argument acts_as_*
// macro, which tree-sitter parses as a bare identifier rather than a call
// (`acts_as_watchable` with no options). It fires only for an acts_as_* identifier
// that is a direct statement of a class/module body — never one nested in a method
// body or block, where a same-named local reference could otherwise match.
func (w *walker) handleBareActsAsMacro(n *sitter.Node, scope []string) error {
	if len(scope) == 0 {
		return nil
	}
	name := extract.Text(n, w.source)
	if !isActsAsMacro(name) {
		return nil
	}
	// Require the identifier to be a direct child of a class/module body, so a
	// `def`-local reference with an acts_as_* name does not spuriously match.
	parent := n.Parent()
	if parent == nil || parent.Kind() != "body_statement" {
		return nil
	}
	if gp := parent.Parent(); gp == nil || (gp.Kind() != "class" && gp.Kind() != "module") {
		return nil
	}
	return w.emitActsAsEdge(n, scope, name)
}

// emitActsAsEdge connects a model to an acts_as_* mixin macro it invokes.
//
// Rails plugins expose behavior through class-body macros named acts_as_<thing>.
// The macro's own body establishes the associations and includes that wire the
// calling model to its collaborator classes — acts_as_attachable declares
// `has_many :attachments, as: :container`, so the macro method already carries an
// edge to Attachment; acts_as_watchable reaches Watcher the same way. But the
// model that *calls* the macro had no edge to it, so the collaborator was
// unreachable from the model: a grep-invisible dependency (the model never names
// Attachment) the index could not follow either. Emitting the model → macro call
// edge completes the path model → acts_as_attachable → Attachment, so blast and
// graph surface the model as a (two-hop) dependent of the collaborator.
//
// The target is the macro method's surface name; the resolver binds it to the
// macro definition when one is indexed. No collaborator class is named here, so
// the rule is fully general across plugins — any acts_as_* macro, no per-framework
// table. When the macro is defined in an unindexed gem the edge drops (unresolved
// target), which is the correct no-op.
func (w *walker) emitActsAsEdge(n *sitter.Node, scope []string, methodName string) error {
	source := strings.Join(scope, "::")
	line := extract.Line(n.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: methodName,
		Kind:            model.EdgeCalls,
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

// classNameAttributeMacros are class-body DSL macros that declare a
// configurable class accessor whose shipped default names a class via a
// string literal. solidus/Spree's `class_name_attribute` is the canonical
// case:
//
//	class_name_attribute :order_adjuster_class,
//	  default: "Spree::Promotion::OrderAdjustmentsRecalculator"
//
// The macro defines a reader/writer (so the applier is reconfigurable at
// runtime) whose default is the named class. Recognising it as a declarative
// idiom — a table entry plus emitClassNameAttribute, sibling to the scope /
// callback / association handlers — keeps the rule narrow and extensible: add
// a sibling macro name here as one surfaces, never a per-framework branch.
var classNameAttributeMacros = map[string]bool{
	"class_name_attribute": true,
}

// emitClassNameAttribute handles a `class_name_attribute :name, default: "A::B"`
// declaration. It emits the accessor as a KindMethod symbol (so
// `graph <name>` resolves instead of "No symbol matches") and, when `default:`
// is a static string literal that parses as a Ruby constant path, a calls edge
// from the accessor to that constant. The edge target is the constant's
// surface text; the resolver's existing exact-qualified match binds it to the
// real class symbol when one is indexed (no new resolver machinery — a static
// literal needs none; 29-03 generalizes the dynamic string-built case).
//
// Precision over recall: no edge is emitted when the default is absent,
// dynamic (interpolated/computed), a lambda, or not a constant path. A missing
// edge is cheaper than a wrong high-confidence one (see staticConstantKeywordArg).
// Only the first accessor symbol is handled — solidus declares one per call.
func (w *walker) emitClassNameAttribute(n *sitter.Node, scope []string) error {
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
	accessor := source + "." + name
	line := extract.Line(n.StartPosition())

	if err := w.emit.Symbol(extract.EmittedSymbol{
		Name:            name,
		Qualified:       accessor,
		Kind:            model.KindMethod,
		ParentQualified: source,
		LineStart:       line,
		LineEnd:         line,
		Docstring:       docstringFor(n, w.source),
	}); err != nil {
		return err
	}

	target, ok := staticConstantKeywordArg(args, "default", w.source)
	if !ok {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: accessor,
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceConvention,
	})
}

// staticConstantKeywordArg reads a keyword argument whose value is a static
// string literal naming a Ruby constant (`default: "A::B::C"`) and returns the
// constant path. It returns ok=false — emit no edge — when the key is absent,
// the value is not a plain string literal (a lambda, a bare constant, an
// interpolated or computed string), or the string does not parse as a constant
// path. This is the precision gate: only a statically-known class name
// produces an edge.
//
// A wrapped literal — `default: "A::B".freeze`, `default: ("A::B")` — parses as
// a call/parenthesized node rather than a bare `string`, so it is treated as
// dynamic and yields no edge. That under-recalls rather than risk a wrong edge;
// solidus writes the bare literal.
func staticConstantKeywordArg(args *sitter.Node, key string, source []byte) (string, bool) {
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
		s, ok := staticStringLiteral(v, source)
		if !ok {
			return "", false
		}
		path := strings.TrimPrefix(s, "::")
		if !looksLikeConstantPath(path) {
			return "", false
		}
		return path, true
	}
	return "", false
}

// staticStringLiteral returns the content of a plain string literal node, with
// ok=true only when the string is fully static: a single `string_content`
// named child with no interpolation or escape sequence. An interpolated string
// (`"Spree::#{x}"`) carries an `interpolation` child and returns ok=false — its
// runtime value is not statically known, so it must not produce an edge.
func staticStringLiteral(n *sitter.Node, source []byte) (string, bool) {
	if n == nil || n.Kind() != "string" {
		return "", false
	}
	content := ""
	seen := false
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := n.NamedChild(i)
		if c == nil {
			continue
		}
		if c.Kind() != "string_content" || seen {
			// interpolation, escape_sequence, or a second fragment — not a
			// simple static literal.
			return "", false
		}
		content = extract.Text(c, source)
		seen = true
	}
	return content, seen
}

// looksLikeConstantPath reports whether s is a Ruby constant path: one or more
// `::`-separated segments, each starting with an uppercase letter and otherwise
// containing only identifier characters (`Spree::Promotion::OrderAdjuster`). It
// rejects method names, file paths, and anything lowercase-leading so a
// non-class default string never produces a spurious constant edge.
func looksLikeConstantPath(s string) bool {
	if s == "" {
		return false
	}
	for _, seg := range strings.Split(s, "::") {
		if !isConstantSegment(seg) {
			return false
		}
	}
	return true
}

// isConstantSegment reports whether seg is a single Ruby constant name:
// uppercase first letter, identifier characters thereafter.
func isConstantSegment(seg string) bool {
	if seg == "" {
		return false
	}
	for i, r := range seg {
		switch {
		case i == 0:
			if r < 'A' || r > 'Z' {
				return false
			}
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
		default:
			return false
		}
	}
	return true
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
