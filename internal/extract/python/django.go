package python

import (
	"strings"

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
// (`Model._default_manager` / `Model._base_manager`). The exposure bound is
// the same as the name convention: a wrong guess emits a QuerySet.* target
// that only resolves in a repo defining a QuerySet class.
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
// or attribute-qualified (`models.ForeignKey(User)`).
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
		return nil
	}
	ownerQualified := strings.Join(scope, ".")
	line := extract.Line(assign.StartPosition())
	return w.emitModelRelationTarget(first, ownerQualified, line)
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
