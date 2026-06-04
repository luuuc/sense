package ruby

import (
	"slices"
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// collectionMethods is the set of method names for which block parameter
// type inference is supported. When a call with a block uses one of these
// methods, the block parameter inherits the receiver's element type.
var collectionMethods = map[string]bool{
	"each": true, "map": true, "select": true,
	"reject": true, "find": true, "detect": true,
	"flat_map": true, "collect": true, "filter": true,
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

// collectNewAssignments records, for each `target = ClassName.new(...)`
// assignment under body whose left-hand side is of lhsKind, a mapping from
// the assigned name to the class name. Shared by buildLocalTypeMap
// (identifier targets) and buildInstanceVarTypeMap (instance_variable targets).
func collectNewAssignments(body *sitter.Node, source []byte, lhsKind string, types map[string]string) {
	for _, kind := range []string{"assignment", "operator_assignment"} {
		_ = extract.WalkNamedDescendants(body, kind, func(a *sitter.Node) error {
			lhs := a.ChildByFieldName("left")
			rhs := a.ChildByFieldName("right")
			if lhs == nil || rhs == nil || lhs.Kind() != lhsKind {
				return nil
			}
			if typ := typeFromNewCall(rhs, source, nil); typ != "" {
				types[extract.Text(lhs, source)] = typ
			}
			return nil
		})
	}
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
	collectNewAssignments(body, source, "identifier", types)
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
		collectNewAssignments(initBody, source, "instance_variable", types)
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
