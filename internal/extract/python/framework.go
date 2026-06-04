package python

import (
	"strings"
	"unicode"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// This file holds the decorator dispatch shared across Python web frameworks and
// the FastAPI route/dependency machinery, plus the small argument/attribute
// helpers every framework reader reuses. Django ORM/URL idioms live in
// django.go; type-annotation composes edges live in annotations.go.

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

// fastapiHTTPMethods is the set of HTTP method names used as FastAPI
// route decorators (`@app.get`, `@router.post`, etc.).
var fastapiHTTPMethods = map[string]bool{
	"get": true, "post": true, "put": true, "delete": true,
	"patch": true, "head": true, "options": true, "trace": true,
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
