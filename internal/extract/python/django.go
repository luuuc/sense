package python

import (
	"slices"
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// This file holds Django-specific edge extraction: ORM model relations
// (`ForeignKey(User)` → composes) and URL routing (`path("", views.index)` →
// calls, `include("app.urls")` → imports). Both read idioms unique to Django; the
// framework-agnostic type-annotation and decorator machinery lives elsewhere.

// djangoRelationFields maps Django ORM field names to whether they emit
// a composes edge. Only relational fields are tracked — value fields
// like CharField, IntegerField are ignored.
var djangoRelationFields = map[string]bool{
	"ForeignKey":      true,
	"OneToOneField":   true,
	"ManyToManyField": true,
}

// querySetTypeName is the receiver type assigned to Django manager/builder
// chains. Manager methods are generated from QuerySet (Manager.from_queryset),
// so `Model.objects.filter` resolves to the real definition, QuerySet.filter.
const querySetTypeName = "QuerySet"

// querySetChainMethods are the QuerySet methods that RETURN a QuerySet, so a
// chain may continue past them. Terminal methods (get, first, create, count,
// aggregate, …) return instances or scalars — a chain hop after one of those
// proves nothing, so they are deliberately absent (closed-world: emit only
// what is provable).
var querySetChainMethods = map[string]bool{
	"filter": true, "exclude": true, "all": true, "none": true,
	"order_by": true, "reverse": true, "distinct": true,
	"annotate": true, "alias": true, "values": true, "values_list": true,
	"select_related": true, "prefetch_related": true,
	"only": true, "defer": true, "using": true,
	"union": true, "intersection": true, "difference": true,
	"select_for_update": true, "complex_filter": true,
}

// maxQuerySetChainDepth bounds the chain recursion (the Ruby receiver walk
// caps at 3 hops; ORM chains legitimately run longer, so allow more but stay
// bounded against pathological generated code).
const maxQuerySetChainDepth = 10

// querySetReturningMethods are method NAMES that return a QuerySet by Django
// convention regardless of receiver: the overridable get_queryset hook (and
// its pre-1.6 spelling) and the ORM-internal _chain. `_clone` is deliberately
// absent — GIS geometries and sql.Query define their own.
var querySetReturningMethods = map[string]bool{
	"get_queryset":  true,
	"get_query_set": true,
	"_chain":        true,
}

// querySetTerminalMethods are the QuerySet methods that do NOT return a
// QuerySet (instances, scalars, containers). They complete the method key:
// a call is recognized as QuerySet API when it is either a chain method or
// one of these.
var querySetTerminalMethods = map[string]bool{
	"create": true, "acreate": true,
	"get": true, "aget": true, "first": true, "afirst": true,
	"last": true, "alast": true, "earliest": true, "aearliest": true,
	"latest": true, "alatest": true, "count": true, "acount": true,
	"exists": true, "aexists": true, "contains": true, "acontains": true,
	"delete": true, "adelete": true, "update": true, "aupdate": true,
	"aggregate": true, "aaggregate": true, "explain": true, "aexplain": true,
	"iterator": true, "aiterator": true, "in_bulk": true, "ain_bulk": true,
	"get_or_create": true, "aget_or_create": true,
	"update_or_create": true, "aupdate_or_create": true,
	"bulk_create": true, "abulk_create": true,
	"bulk_update": true, "abulk_update": true,
	"dates": true, "datetimes": true,
}

// isQuerySetMethodName reports whether a method name belongs to the QuerySet
// API (chainable or terminal). It is the METHOD key of the double-keyed
// receiver-name convention: a receiver named qs/queryset types as QuerySet
// only for calls that are QuerySet API — either key alone proves nothing.
func isQuerySetMethodName(name string) bool {
	return querySetChainMethods[name] || querySetTerminalMethods[name]
}

// isQuerySetNameConvention is the NAME key: locals and parameters literally
// named qs/queryset are querysets by overwhelming Django convention. Only
// meaningful combined with isQuerySetMethodName (see typedReceiverTarget).
//
// Exposure bound: a wrong guess (a list named qs calling .count()) emits a
// QuerySet.* target, which resolves ONLY in a repo that defines a QuerySet
// class — django itself, where the convention holds — and is dropped as
// unresolvable everywhere else (edges need a resolved target to persist).
func isQuerySetNameConvention(name string) bool {
	return name == "qs" || name == "queryset"
}

// isQuerySetExpr reports whether an expression provably evaluates to a Django
// QuerySet builder: `<PascalModel>.objects`, a chain of QuerySet-RETURNING
// method calls rooted there (`Model.objects.filter(…).exclude(…)`), or a
// local variable already typed QuerySet in the function's type map. The
// PascalCase-model requirement keeps the rule off arbitrary `.objects`
// attributes; the chain-method whitelist keeps it off terminal calls
// (`Model.objects.get(pk=1)` returns an instance, not a QuerySet).
func isQuerySetExpr(n *sitter.Node, src []byte, types map[string]string) bool {
	return isQuerySetExprDepth(n, src, types, 0)
}

func isQuerySetExprDepth(n *sitter.Node, src []byte, types map[string]string, depth int) bool {
	if n == nil || depth > maxQuerySetChainDepth {
		return false
	}
	switch n.Kind() {
	case "identifier":
		name := extract.Text(n, src)
		// The name convention is safe here: every path FROM this root still
		// requires whitelisted QuerySet methods (chain hops above, the final
		// method key in typedReceiverTarget), so both keys always apply.
		return types[name] == querySetTypeName || isQuerySetNameConvention(name)
	case "attribute":
		return isModelObjectsAttr(n, src)
	case "call":
		return isQuerySetChainCall(n, src, types, depth)
	}
	return false
}

// djangoManagerLeafNames are the attribute names that root a manager chain:
// the public `objects` plus the private accessors Django's own plumbing uses
// (`Model._default_manager` / `Model._base_manager`). Two edges root here with
// different exposure bounds: the QuerySet-method edge's wrong guesses emit a
// QuerySet.* target that only resolves in a repo defining a QuerySet class,
// while the model edge (managerChainModelRoot) targets the PascalCase root
// name itself, which resolves against any same-named class — its bound is the
// gate stack (literal PascalCase receiver + exact manager attr + whitelisted
// method), and a rare wrong guess (`Config.objects.get(k)` on a class-level
// dict) still encodes a true textual reference to that class.
var djangoManagerLeafNames = map[string]bool{
	"objects":          true,
	"_default_manager": true,
	"_base_manager":    true,
}

// isModelObjectsAttr matches the chain root: a manager leaf (objects /
// _default_manager / _base_manager) on a model reference — a PascalCase
// identifier or one of the self-attribute idioms Django's own plumbing uses
// (`self.model` in managers/descriptors, `self.__class__` in Model methods,
// `self.through` in related descriptors).
func isModelObjectsAttr(n *sitter.Node, src []byte) bool {
	leaf := n.ChildByFieldName("attribute")
	obj := n.ChildByFieldName("object")
	if leaf == nil || obj == nil {
		return false
	}
	if !djangoManagerLeafNames[extract.Text(leaf, src)] {
		return false
	}
	if obj.Kind() == "identifier" && isPascalCase(extract.Text(obj, src)) {
		return true
	}
	return isSelfModelRef(obj, src)
}

// isSelfModelRef matches exactly `self.model`, `self.__class__`, and
// `self.through` — the three attribute shapes that hold a model class inside
// Django framework plumbing. No general attribute walk: anything else
// (self.request, obj.model) stays unproven.
func isSelfModelRef(n *sitter.Node, src []byte) bool {
	if n.Kind() != "attribute" {
		return false
	}
	obj := n.ChildByFieldName("object")
	leaf := n.ChildByFieldName("attribute")
	if obj == nil || leaf == nil || obj.Kind() != "identifier" || extract.Text(obj, src) != "self" {
		return false
	}
	switch extract.Text(leaf, src) {
	case "model", "__class__", "through":
		return true
	}
	return false
}

// managerChainModelRoot walks a manager-chain receiver expression
// (`Device.objects`, `Device.objects.filter(…).exclude(…)`) to its root and
// returns the literal model identifier it is anchored on. The caller emits a
// dependency edge to the model itself — code querying `Device.objects` depends
// on Device even though the resolved method target is QuerySet API. Chains
// rooted on self-attribute idioms (`self.model.objects`) or conventionally
// named locals (`qs.filter(…)`) carry no literal model name and return false.
//
// PRECONDITION: the walk does NOT re-verify hop method names, so it is only
// valid on a chain typedReceiverTarget has already vetted as QuerySet API —
// its sole call site. Consequently a chain ending in a CUSTOM manager method
// (`Device.objects.restrict()`) never reaches this function and emits no model
// edge: a deliberate false-positive bound, not an oversight. Extending the
// edge to custom-manager chains is a separate, bench-gated decision.
func managerChainModelRoot(n *sitter.Node, src []byte) (string, bool) {
	return managerChainModelRootDepth(n, src, 0)
}

func managerChainModelRootDepth(n *sitter.Node, src []byte, depth int) (string, bool) {
	if n == nil || depth > maxQuerySetChainDepth {
		return "", false
	}
	switch n.Kind() {
	case "attribute":
		leaf := n.ChildByFieldName("attribute")
		obj := n.ChildByFieldName("object")
		if leaf == nil || obj == nil || !djangoManagerLeafNames[extract.Text(leaf, src)] {
			return "", false
		}
		if obj.Kind() == "identifier" && isPascalCase(extract.Text(obj, src)) {
			return extract.Text(obj, src), true
		}
		return "", false
	case "call":
		fn := n.ChildByFieldName("function")
		if fn == nil || fn.Kind() != "attribute" {
			return "", false
		}
		return managerChainModelRootDepth(fn.ChildByFieldName("object"), src, depth+1)
	}
	return "", false
}

// isQuerySetChainCall matches a chain hop: a conventionally QuerySet-returning
// method regardless of receiver (self.get_queryset(), self._chain()), or a
// chainable QuerySet method whose receiver is itself a queryset expression.
func isQuerySetChainCall(n *sitter.Node, src []byte, types map[string]string, depth int) bool {
	fn := n.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "attribute" {
		return false
	}
	leaf := fn.ChildByFieldName("attribute")
	if leaf == nil {
		return false
	}
	name := extract.Text(leaf, src)
	if querySetReturningMethods[name] {
		return true
	}
	if !querySetChainMethods[name] {
		return false
	}
	return isQuerySetExprDepth(fn.ChildByFieldName("object"), src, types, depth+1)
}

// emitDjangoModelField checks if an assignment's RHS is a Django relational field
// call (e.g. `models.ForeignKey(User)`). If so, it emits a composes edge from the
// enclosing class to the target model. The call can be bare (`ForeignKey(User)`)
// or attribute-qualified (`models.ForeignKey(User)`), and the target model can be
// the first positional argument or the `to=` keyword (`ForeignKey(to='dcim.Device')`).
func (w *walker) emitDjangoModelField(assign *sitter.Node, scope []string) error {
	if len(scope) == 0 {
		return nil
	}
	rhs := assign.ChildByFieldName("right")
	if rhs == nil || rhs.Kind() != "call" {
		return nil
	}
	fn := rhs.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	if !djangoRelationFields[w.djangoFieldName(fn)] {
		return nil
	}
	args := rhs.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := firstPositionalArg(args)
	if first == nil {
		first = keywordArgValue(args, "to", w.source)
	}
	if first == nil {
		return nil
	}
	ownerQualified := strings.Join(scope, ".")
	line := extract.Line(assign.StartPosition())
	w.collectRelatedNameAccessor(args, ownerQualified, line)
	return w.emitModelRelationTarget(first, ownerQualified, line)
}

// collectRelatedNameAccessor records a relational field's `related_name` for
// the end-of-file flush (flushRelatedNameAccessors). Collection, not emission:
// whether an accessor name is unambiguous WITHIN this file is only known after
// the whole module is walked — symbols upsert on (file_id, qualified), so two
// same-file emissions could never reach the resolver as two candidates, and
// the cross-file ambiguity gate cannot see a same-file collision at all.
// Skipped as unprovable (closed-world): `related_name='+'` (reverse accessor
// disabled), `%(class)s` templates (expand per concrete subclass), f-strings
// and any other interpolated/dynamic value, and names that are not plain
// identifiers (Django would reject them; a truncated f-string fragment must
// never poison a real accessor name).
func (w *walker) collectRelatedNameAccessor(args *sitter.Node, ownerQualified string, line int) {
	val := keywordArgValue(args, "related_name", w.source)
	if val == nil || val.Kind() != "string" || hasInterpolation(val) {
		return
	}
	name := stringContent(val, w.source)
	if !isPlainIdentifier(name) {
		return
	}
	decl, seen := w.relatedNames[name]
	if !seen {
		w.relatedNames[name] = &relatedNameDecl{owner: ownerQualified, line: line}
		return
	}
	if decl.owner != ownerQualified {
		decl.ambiguous = true
	}
}

// hasInterpolation reports whether a string node contains an f-string
// interpolation child (`f"{prefix}_items"`): tree-sitter classifies f-strings
// as plain `string` nodes, and stringContent would return only the first
// literal fragment — a truncated name that could collide with a real
// related_name elsewhere.
func hasInterpolation(strNode *sitter.Node) bool {
	count := strNode.NamedChildCount()
	for i := uint(0); i < count; i++ {
		if c := strNode.NamedChild(i); c != nil && c.Kind() == "interpolation" {
			return true
		}
	}
	return false
}

// isPlainIdentifier reports whether a related_name is a plain Python
// identifier (unicode letters/digits/underscore, not starting with a digit).
// Anything else — empty, '+', '%(class)s' templates, spaces — is either a
// Django error or a dynamic form this extractor cannot prove.
func isPlainIdentifier(name string) bool {
	for i, r := range name {
		switch {
		case r == '_' || unicode.IsLetter(r):
		case unicode.IsDigit(r) && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

// relatedNameDecl accumulates one accessor name's declarations within a file:
// the first declaring class and line, and whether a SECOND class also claimed
// the same spelling (two FKs on different models sharing a related_name is
// legal Django when their targets differ).
type relatedNameDecl struct {
	owner     string
	line      int
	ambiguous bool
}

// flushRelatedNameAccessors emits, at end of file, one django-related:*
// synthetic symbol per accessor name whose in-file declarations all agree on
// the declaring class, plus a calls edge from the accessor to that model —
// the reverse manager yields child instances, so code reaching
// `parent.<related_name>` depends on the child model's contract. A name two
// classes claim is skipped entirely: a wrong anchor misleads blast worse than
// a gap, and the resolver could never tell (same-file emissions collapse to
// one row at persistence). Cross-file collisions are the resolver's job — two
// files each emit their synthetic and the ambiguity gate drops accessor edges
// to the duplicated name. Names are flushed in sorted order so the emission
// stream is deterministic.
func (w *walker) flushRelatedNameAccessors() error {
	names := make([]string, 0, len(w.relatedNames))
	for name, decl := range w.relatedNames {
		if !decl.ambiguous {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	for _, name := range names {
		decl := w.relatedNames[name]
		qualified := extract.PrefixDjangoRelated + name
		if err := w.emit.Symbol(extract.EmittedSymbol{
			Name:       name,
			Qualified:  qualified,
			Kind:       model.KindConstant,
			Visibility: "public",
			LineStart:  decl.line,
			LineEnd:    decl.line,
		}); err != nil {
			return err
		}
		line := decl.line
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: decl.owner,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ormAccessorVerbs are the QuerySet methods distinctive enough to key the
// reverse-related-manager accessor rule: seeing `<expr>.<name>.<verb>(…)` with
// one of these verbs treats <name> as a queryset accessor. Deliberately
// narrower than the QuerySet API tables above — verbs that collide with
// stdlib/common APIs (get, count, values, values_list, create, exists, first,
// last, none, reverse, using) are excluded so `self.config.get(…)` or
// `x.data.values()` never fires the rule (the Finding-12 over-attribution
// family). Relationship to the sibling tables: the LEAF verb here must be
// collision-safe on its own (it anchors the proof), while intermediate hops
// (relatedManagerAccessorRoot's call arm) only need querySetChainMethods —
// the leaf already anchored. Do not merge the two maps: that would silently
// widen the leaf key. Precision-first: widening any verb back in is a
// bench-gated call.
var ormAccessorVerbs = map[string]bool{
	"filter": true, "exclude": true, "annotate": true, "alias": true,
	"order_by": true, "distinct": true, "all": true,
	"select_related": true, "prefetch_related": true,
	"only": true, "defer": true, "select_for_update": true,
	"union": true, "intersection": true, "difference": true,
	"complex_filter": true, "aggregate": true,
	"earliest": true, "latest": true, "in_bulk": true,
	"get_or_create": true, "update_or_create": true,
	"bulk_create": true, "bulk_update": true,
}

// relatedManagerAccessor walks an ORM-verb call's receiver chain
// (`order.positions.filter(…)`, `self.all_positions.filter(…).exclude(…)`) to
// its attribute root and returns the accessor name — the leaf attribute the
// chain is rooted on. Roots already owned by the manager-chain rule (objects /
// _default_manager / _base_manager) return false, as do non-attribute roots
// (identifier receivers are the typed/convention rules' turf). The METHOD key
// (an ormAccessorVerbs verb) is checked by the caller; this walk supplies the
// NAME key. Hops are re-verified against querySetChainMethods here because,
// unlike managerChainModelRoot, this chain was never vetted by
// typedReceiverTarget. Exposure bound: the emitted target is ONLY the
// django-related:* candidate, which resolves exclusively against a declared
// related_name — in repos without one, the edge drops at write time.
func relatedManagerAccessor(fn *sitter.Node, src []byte) (string, bool) {
	leaf := fn.ChildByFieldName("attribute")
	if leaf == nil || !ormAccessorVerbs[extract.Text(leaf, src)] {
		return "", false
	}
	return relatedManagerAccessorRoot(fn.ChildByFieldName("object"), src, 0)
}

func relatedManagerAccessorRoot(n *sitter.Node, src []byte, depth int) (string, bool) {
	if n == nil || depth > maxQuerySetChainDepth {
		return "", false
	}
	switch n.Kind() {
	case "attribute":
		leaf := n.ChildByFieldName("attribute")
		if leaf == nil {
			return "", false
		}
		name := extract.Text(leaf, src)
		if name == "" || djangoManagerLeafNames[name] {
			return "", false
		}
		return name, true
	case "call":
		fn := n.ChildByFieldName("function")
		if fn == nil || fn.Kind() != "attribute" {
			return "", false
		}
		hop := fn.ChildByFieldName("attribute")
		if hop == nil || !querySetChainMethods[extract.Text(hop, src)] {
			return "", false
		}
		return relatedManagerAccessorRoot(fn.ChildByFieldName("object"), src, depth+1)
	}
	return "", false
}

// emitRelatedManagerEdge emits the calls edge for an ORM-verb accessor chain
// to the django-related:* synthetic its FK declaration emitted. The target is
// ONLY the prefixed candidate, never the bare accessor name: a bare name would
// bind single-candidate same-named symbols at the confident tier in ANY Python
// repo (`self.opts.filter(…)` reaching a lone `opts` — the Finding-12
// over-attribution family), while the prefixed target resolves exclusively
// against declared related_names and drops everywhere else. Property-mediated
// accessors (a queryset-returning `def positions` property) are deliberately
// NOT wired — that needs receiver typing, a separate bench-gated decision.
// Rides ConfidenceAmbiguous — receiver unproven, gated by the accessor-name +
// ORM-verb double key plus two uniqueness gates: same-file collisions are
// skipped at flush (flushRelatedNameAccessors), cross-file collisions are
// dropped by the resolver (isAmbiguousDjangoRelated).
func (w *walker) emitRelatedManagerEdge(fn *sitter.Node, source string, line int) error {
	name, ok := relatedManagerAccessor(fn, w.source)
	if !ok {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: source,
		TargetQualified: extract.PrefixDjangoRelated + name,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceAmbiguous,
	})
}

// djangoFieldName returns the field constructor's name from a bare
// (`ForeignKey(...)`) or attribute-qualified (`models.ForeignKey(...)`) call.
func (w *walker) djangoFieldName(fn *sitter.Node) string {
	switch fn.Kind() {
	case "identifier":
		return extract.Text(fn, w.source)
	case "attribute":
		return attrLastSegment(fn, w.source)
	}
	return ""
}

// emitModelRelationTarget emits the composes edge for a relational field's first
// positional argument: an identifier target (`User`, confidence 0.9) or a string
// like `"app.User"` (confidence 0.8, last dot-segment used as the target).
func (w *walker) emitModelRelationTarget(first *sitter.Node, ownerQualified string, line int) error {
	switch first.Kind() {
	case "identifier":
		target := extract.Text(first, w.source)
		if target == "" {
			return nil
		}
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: ownerQualified,
			TargetQualified: target,
			Kind:            model.EdgeComposes,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		})
	case "string":
		target := stringContent(first, w.source)
		if target == "" {
			return nil
		}
		if idx := strings.LastIndex(target, "."); idx >= 0 {
			target = target[idx+1:]
		}
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: ownerQualified,
			TargetQualified: target,
			Kind:            model.EdgeComposes,
			Line:            &line,
			Confidence:      extract.ConfidenceAmbiguous,
		})
	}
	return nil
}

// djangoURLFunctions are the Django URL routing functions that wire
// views to URL patterns.
var djangoURLFunctions = map[string]bool{
	"path":    true,
	"re_path": true,
}

// emitURLPatternEdges detects Django URL pattern calls at module scope
// (inside urlpatterns list assignments). Each `path()`/`re_path()` call
// with a view reference emits a calls edge to the view. `include()`
// calls emit an imports edge to the included module.
func (w *walker) emitURLPatternEdges(assign *sitter.Node) error {
	lhs := assign.ChildByFieldName("left")
	if lhs == nil || extract.Text(lhs, w.source) != "urlpatterns" {
		return nil
	}
	rhs := assign.ChildByFieldName("right")
	if rhs == nil || rhs.Kind() != "list" {
		return nil
	}
	count := rhs.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := rhs.NamedChild(i)
		if child != nil && child.Kind() == "call" {
			if err := w.emitSingleURLPattern(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitSingleURLPattern handles one path()/re_path()/include() call inside
// urlpatterns, dispatching the view argument to the matching edge emitter.
func (w *walker) emitSingleURLPattern(call *sitter.Node) error {
	fn := call.ChildByFieldName("function")
	if fn == nil {
		return nil
	}
	fnName := ""
	if fn.Kind() == "identifier" {
		fnName = extract.Text(fn, w.source)
	}
	if fnName == "include" {
		return w.emitIncludeEdge(call)
	}
	if !djangoURLFunctions[fnName] {
		return nil
	}
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() < 2 {
		return nil
	}
	positionals := positionalArgs(args)
	if len(positionals) < 2 {
		return nil
	}
	viewArg := positionals[1]
	line := extract.Line(call.StartPosition())
	if viewArg.Kind() == "call" {
		return w.emitURLViewCall(viewArg, line)
	}
	return w.emitURLViewRef(viewArg, line)
}

// emitURLViewCall handles a view argument that is itself a call: a class-based
// view `SomeView.as_view()` (calls edge to the view class) or a nested
// `include(...)` (imports edge).
func (w *walker) emitURLViewCall(viewArg *sitter.Node, line int) error {
	innerFn := viewArg.ChildByFieldName("function")
	if innerFn == nil {
		return nil
	}
	if innerFn.Kind() == "attribute" && attrLastSegment(innerFn, w.source) == "as_view" {
		return w.emitAsViewEdge(innerFn, line)
	}
	if innerFn.Kind() == "identifier" && extract.Text(innerFn, w.source) == "include" {
		return w.emitIncludeEdge(viewArg)
	}
	return nil
}

// emitAsViewEdge emits the calls edge to the class behind `SomeView.as_view()`.
func (w *walker) emitAsViewEdge(asViewFn *sitter.Node, line int) error {
	obj := asViewFn.ChildByFieldName("object")
	if obj == nil {
		return nil
	}
	target := attrLastSegment(obj, w.source)
	if target == "" {
		target = extract.Text(obj, w.source)
	}
	if target == "" {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: "urlpatterns",
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

// emitURLViewRef emits the calls edge for a plain view reference — a dotted
// `views.index` (attribute) or a bare `index` (identifier).
func (w *walker) emitURLViewRef(viewArg *sitter.Node, line int) error {
	target := ""
	switch viewArg.Kind() {
	case "identifier":
		target = extract.Text(viewArg, w.source)
	case "attribute":
		target = attrLastSegment(viewArg, w.source)
	}
	if target == "" {
		return nil
	}
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: "urlpatterns",
		TargetQualified: target,
		Kind:            model.EdgeCalls,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}

// emitIncludeEdge handles `include("app.urls")` → imports edge.
func (w *walker) emitIncludeEdge(call *sitter.Node) error {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return nil
	}
	first := firstPositionalArg(args)
	if first == nil || first.Kind() != "string" {
		return nil
	}
	target := stringContent(first, w.source)
	if target == "" {
		return nil
	}
	line := extract.Line(call.StartPosition())
	return w.emit.Edge(extract.EmittedEdge{
		SourceQualified: "urlpatterns",
		TargetQualified: target,
		Kind:            model.EdgeImports,
		Line:            &line,
		Confidence:      extract.ConfidenceStatic,
	})
}
