package python

import (
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// djangoRelationFields maps Django ORM field names to whether they emit
// a composes edge. Only relational fields are tracked — value fields
// like CharField, IntegerField are ignored.
var djangoRelationFields = map[string]bool{
	"ForeignKey":      true,
	"OneToOneField":   true,
	"ManyToManyField": true,
}

// emitDjangoModelField checks if an assignment's RHS is a Django
// relational field call (e.g. `models.ForeignKey(User)`). If so,
// it emits a composes edge from the enclosing class to the target model.
//
// The call can be bare (`ForeignKey(User)`) or attribute-qualified
// (`models.ForeignKey(User)`). The first positional argument is the
// target: either an identifier (confidence 0.9) or a string literal
// like `"app.User"` (confidence 0.8, last dot-segment used as target).
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

	var fieldName string
	switch fn.Kind() {
	case "identifier":
		fieldName = extract.Text(fn, w.source)
	case "attribute":
		fieldName = attrLastSegment(fn, w.source)
	default:
		return nil
	}
	if !djangoRelationFields[fieldName] {
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

// fastapiHTTPMethods is the set of HTTP method names used as FastAPI
// route decorators (`@app.get`, `@router.post`, etc.).
var fastapiHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "head": true, "options": true, "trace": true,
}

// handleDecoratedDefinition inspects a decorated_definition for
// framework patterns before delegating to the inner definition.
// Currently handles FastAPI route decorators on functions.
func (w *walker) handleDecoratedDefinition(n *sitter.Node, scope []string) error {
	def := n.ChildByFieldName("definition")
	if def == nil {
		return w.walkChildren(n, scope)
	}

	switch def.Kind() {
	case "function_definition":
		return w.emitFunctionAndWalkBody(def, scope, collectDecorators(n))
	case "class_definition":
		return w.handleClass(def, scope)
	default:
		return w.walk(def, scope)
	}
}

// emitFastapiRouteEdge checks if a decorator is a FastAPI route
// (e.g. `@app.post("/orders")`) and emits a calls edge from the
// route path to the handler function.
func (w *walker) emitFastapiRouteEdge(decorator *sitter.Node, handlerQualified string) error {
	count := decorator.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := decorator.NamedChild(i)
		if child == nil || child.Kind() != "call" {
			continue
		}
		fn := child.ChildByFieldName("function")
		if fn == nil || fn.Kind() != "attribute" {
			continue
		}
		method := attrLastSegment(fn, w.source)
		if !fastapiHTTPMethods[method] {
			continue
		}
		args := child.ChildByFieldName("arguments")
		if args == nil || args.NamedChildCount() == 0 {
			continue
		}
		first := firstPositionalArg(args)
		if first == nil {
			continue
		}
		path := ""
		if first.Kind() == "string" {
			path = stringContent(first, w.source)
		}
		if path == "" {
			continue
		}
		routeName := strings.ToUpper(method) + " " + path
		line := extract.Line(decorator.StartPosition())
		if err := w.emit.Edge(extract.EmittedEdge{
			SourceQualified: routeName,
			TargetQualified: handlerQualified,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		}); err != nil {
			return err
		}
	}
	return nil
}

// emitDependsEdges scans function parameters for FastAPI `Depends(fn)`
// calls and emits a calls edge from the function to each dependency.
func (w *walker) emitDependsEdges(fn *sitter.Node, qualified string) error {
	params := fn.ChildByFieldName("parameters")
	if params == nil {
		return nil
	}
	return extract.WalkNamedDescendants(params, "call", func(c *sitter.Node) error {
		callFn := c.ChildByFieldName("function")
		if callFn == nil || callFn.Kind() != "identifier" {
			return nil
		}
		if extract.Text(callFn, w.source) != "Depends" {
			return nil
		}
		args := c.ChildByFieldName("arguments")
		if args == nil || args.NamedChildCount() == 0 {
			return nil
		}
		first := firstPositionalArg(args)
		if first == nil {
			return nil
		}
		kind := first.Kind()
		if kind != "identifier" && kind != "attribute" {
			return nil
		}
		target := extract.Text(first, w.source)
		if target == "" {
			return nil
		}
		line := extract.Line(c.StartPosition())
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: qualified,
			TargetQualified: target,
			Kind:            model.EdgeCalls,
			Line:            &line,
			Confidence:      extract.ConfidenceStatic,
		})
	})
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

// emitSingleURLPattern handles one path()/re_path()/include() call inside urlpatterns.
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
		innerFn := viewArg.ChildByFieldName("function")
		if innerFn != nil && innerFn.Kind() == "attribute" {
			if attrLastSegment(innerFn, w.source) == "as_view" {
				obj := innerFn.ChildByFieldName("object")
				if obj != nil {
					target := attrLastSegment(obj, w.source)
					if target == "" {
						target = extract.Text(obj, w.source)
					}
					if target != "" {
						return w.emit.Edge(extract.EmittedEdge{
							SourceQualified: "urlpatterns",
							TargetQualified: target,
							Kind:            model.EdgeCalls,
							Line:            &line,
							Confidence:      extract.ConfidenceStatic,
						})
					}
				}
			}
		}
		if innerFn != nil && innerFn.Kind() == "identifier" && extract.Text(innerFn, w.source) == "include" {
			return w.emitIncludeEdge(viewArg)
		}
		return nil
	}

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

// pythonPrimitives is the set of built-in type names that should NOT
// produce composes edges from type annotations.
var pythonPrimitives = map[string]bool{
	"int": true, "str": true, "float": true, "bool": true,
	"bytes": true, "None": true, "object": true, "type": true,
	"complex": true,
}

// pythonGenericWrappers are type constructors where we unwrap one level
// to find the inner class reference. Limited to the types that commonly
// appear in Django, FastAPI, and Pydantic codebases.
var pythonGenericWrappers = map[string]bool{
	"Optional": true, "Union": true,
	"list": true, "List": true,
	"set": true, "Set": true,
	"tuple": true, "Tuple": true,
	"dict": true, "Dict": true,
	"Sequence": true, "Type": true,
	"ClassVar": true,
}

// emitTypeAnnotationEdge extracts a composes edge from a type
// annotation node. Handles plain identifiers, generic types
// (Optional[X], list[X], Union[X, Y], dict[str, X]), and skips
// primitives.
func (w *walker) emitTypeAnnotationEdge(typeNode *sitter.Node, ownerQualified string, line int) error {
	if typeNode == nil {
		return nil
	}

	switch typeNode.Kind() {
	case "type":
		if typeNode.NamedChildCount() > 0 {
			return w.emitTypeAnnotationEdge(typeNode.NamedChild(0), ownerQualified, line)
		}
	case "identifier":
		name := extract.Text(typeNode, w.source)
		if name == "" || pythonPrimitives[name] || pythonGenericWrappers[name] {
			return nil
		}
		if !isPascalCase(name) {
			return nil
		}
		return w.emit.Edge(extract.EmittedEdge{
			SourceQualified: ownerQualified,
			TargetQualified: name,
			Kind:            model.EdgeComposes,
			Line:            &line,
			Confidence:      extract.ConfidenceConvention,
		})
	case "generic_type":
		outerNode := typeNode.ChildByFieldName("type")
		if outerNode == nil {
			count := typeNode.NamedChildCount()
			if count > 0 {
				outerNode = typeNode.NamedChild(0)
			}
		}
		if outerNode == nil {
			return nil
		}
		outerName := extract.Text(outerNode, w.source)
		if pythonGenericWrappers[outerName] {
			return w.emitTypeParamEdges(typeNode, ownerQualified, line)
		}
		if isPascalCase(outerName) && !pythonPrimitives[outerName] {
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: ownerQualified,
				TargetQualified: outerName,
				Kind:            model.EdgeComposes,
				Line:            &line,
				Confidence:      extract.ConfidenceConvention,
			})
		}
	case "attribute":
		name := extract.Text(typeNode, w.source)
		if name != "" {
			return w.emit.Edge(extract.EmittedEdge{
				SourceQualified: ownerQualified,
				TargetQualified: name,
				Kind:            model.EdgeComposes,
				Line:            &line,
				Confidence:      extract.ConfidenceConvention,
			})
		}
	}
	return nil
}

// emitTypeParamEdges unwraps one level of generic type parameters and
// emits composes edges for each inner type that references a class.
func (w *walker) emitTypeParamEdges(genericType *sitter.Node, ownerQualified string, line int) error {
	count := genericType.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := genericType.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "type_parameter" {
			innerCount := child.NamedChildCount()
			for j := uint(0); j < innerCount; j++ {
				inner := child.NamedChild(j)
				if inner != nil {
					if err := w.emitTypeAnnotationEdge(inner, ownerQualified, line); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// ---- helpers ----

// attrLastSegment returns the last identifier in an attribute chain.
// For `models.ForeignKey` → "ForeignKey", for `views.OrderListView` → "OrderListView".
func attrLastSegment(attr *sitter.Node, source []byte) string {
	nameNode := attr.ChildByFieldName("attribute")
	if nameNode != nil {
		return extract.Text(nameNode, source)
	}
	count := attr.NamedChildCount()
	if count > 0 {
		last := attr.NamedChild(count - 1)
		if last != nil && last.Kind() == "identifier" {
			return extract.Text(last, source)
		}
	}
	return ""
}

// stringContent extracts the text payload from a string node,
// using the string_content child node.
func stringContent(strNode *sitter.Node, source []byte) string {
	count := strNode.NamedChildCount()
	for i := uint(0); i < count; i++ {
		c := strNode.NamedChild(i)
		if c != nil && c.Kind() == "string_content" {
			return extract.Text(c, source)
		}
	}
	return ""
}

// firstPositionalArg returns the first non-keyword argument from an
// argument_list node, or nil if there are no positional args.
func firstPositionalArg(args *sitter.Node) *sitter.Node {
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg != nil && arg.Kind() != "keyword_argument" {
			return arg
		}
	}
	return nil
}

// positionalArgs returns all non-keyword arguments from an argument_list.
func positionalArgs(args *sitter.Node) []*sitter.Node {
	var result []*sitter.Node
	count := args.NamedChildCount()
	for i := uint(0); i < count; i++ {
		arg := args.NamedChild(i)
		if arg != nil && arg.Kind() != "keyword_argument" {
			result = append(result, arg)
		}
	}
	return result
}

// collectDecorators returns all decorator nodes from a decorated_definition.
func collectDecorators(n *sitter.Node) []*sitter.Node {
	var result []*sitter.Node
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child != nil && child.Kind() == "decorator" {
			result = append(result, child)
		}
	}
	return result
}

// isPascalCase returns true if the name starts with an uppercase letter,
// which in Python convention indicates a class/type rather than a
// variable or function.
func isPascalCase(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}
