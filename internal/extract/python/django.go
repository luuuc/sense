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
