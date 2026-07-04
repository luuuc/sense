package python

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// Receiver-type inference for method calls. A call on a receiver whose type is
// provable inside the function scope resolves to `<Type>.<method>` at
// ConfidenceDynamic instead of falling to the bare-name ConfidenceUnresolved
// tier. This is deliberately NOT a type inferencer — it covers exactly the
// provable local patterns (mirroring Ruby's localTypes in
// extract/ruby/typeinfer.go): annotated parameters, annotated locals,
// constructor-assigned locals, and (via django.go) the `Model.objects`
// builder chain.

// inferLocalTypes builds the function-scope receiver→class map. The map is
// POSITION-BLIND: it is precomputed over the whole body before calls are
// walked, so a call textually before a reassignment also sees the final type
// (there is no flow analysis; a wrong edge is bounded at ConfidenceDynamic to
// a class the variable provably held somewhere in the function). Assignments
// inside nested defs are NOT collected, and any name a nested def's
// parameters shadow is dropped from the map — calls inside closures are
// attributed to the enclosing function (see handleFunction), so a shadowed
// name is no longer provable there.
func inferLocalTypes(funcNode *sitter.Node, src []byte) map[string]string {
	types := map[string]string{}
	collectParamTypes(funcNode, src, types)
	walkOuterAssignments(funcNode.ChildByFieldName("body"), src, types)
	return types
}

// walkOuterAssignments visits assignments in document order (last wins),
// stopping at nested function boundaries: a nested def's assignments belong
// to its own scope, and its parameter names shadow the outer scope, so those
// names are deleted from the map instead.
func walkOuterAssignments(n *sitter.Node, src []byte, types map[string]string) {
	if n == nil {
		return
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == "function_definition" {
			deleteShadowedParams(child, src, types)
			continue
		}
		if child.Kind() == "assignment" {
			collectAssignmentType(child, src, types)
		}
		walkOuterAssignments(child, src, types)
	}
}

// deleteShadowedParams removes a nested def's parameter names from the outer
// type map (conservative: the outer type may still hold before the nested
// def, but position-blind maps cannot express that).
func deleteShadowedParams(funcNode *sitter.Node, src []byte, types map[string]string) {
	for name := range collectParams(funcNode, src) {
		delete(types, name)
	}
}

// collectParamTypes records `def f(q: Query)` / `def f(q: Query = None)`
// parameter annotations. `self`/`cls` are skipped: the resolver's
// self-rewrite path already resolves them to the enclosing class at full
// confidence, which an annotation must not downgrade.
func collectParamTypes(funcNode *sitter.Node, src []byte, types map[string]string) {
	params := funcNode.ChildByFieldName("parameters")
	if params == nil {
		return
	}
	count := params.NamedChildCount()
	for i := uint(0); i < count; i++ {
		p := params.NamedChild(i)
		if p == nil {
			continue
		}
		// typed_parameter has no "name" field in the grammar — its name is
		// the first named child; typed_default_parameter does carry one.
		var name *sitter.Node
		switch p.Kind() {
		case "typed_parameter":
			if first := p.NamedChild(0); first != nil && first.Kind() == "identifier" {
				name = first
			}
		case "typed_default_parameter":
			name = p.ChildByFieldName("name")
		default:
			continue
		}
		typ := p.ChildByFieldName("type")
		if name == nil || typ == nil {
			continue
		}
		paramName := extract.Text(name, src)
		if paramName == "self" || paramName == "cls" {
			continue
		}
		if t, ok := annotationTypeName(typ, src); ok {
			types[paramName] = t
		}
	}
}

// collectAssignmentType records `x: Query = …` annotated assignments and
// `x = Query(…)` / `x = sql.Query(…)` constructor assignments, plus the
// django `x = Model.objects.…(…)` chain (typed QuerySet). A reassignment
// whose RHS proves nothing DELETES the entry — the variable is no longer
// provably the old type.
func collectAssignmentType(a *sitter.Node, src []byte, types map[string]string) {
	left := a.ChildByFieldName("left")
	if left == nil || left.Kind() != "identifier" {
		return
	}
	name := extract.Text(left, src)
	if typ := a.ChildByFieldName("type"); typ != nil {
		if t, ok := annotationTypeName(typ, src); ok {
			types[name] = t
		} else {
			delete(types, name)
		}
		return
	}
	if t, ok := callResultTypeName(a.ChildByFieldName("right"), src, types); ok {
		types[name] = t
	} else {
		delete(types, name)
	}
}

// callResultTypeName resolves an assignment's RHS call to the class of its
// result: a direct constructor call (`Query(…)`, `sql.Query(…)`) or a django
// objects chain (`Model.objects.filter(…)` → QuerySet).
func callResultTypeName(right *sitter.Node, src []byte, types map[string]string) (string, bool) {
	if right == nil || right.Kind() != "call" {
		return "", false
	}
	fn := right.ChildByFieldName("function")
	if fn == nil {
		return "", false
	}
	switch fn.Kind() {
	case "identifier":
		if name := extract.Text(fn, src); isReceiverClassName(name) {
			return name, true
		}
	case "attribute":
		leaf := fn.ChildByFieldName("attribute")
		if leaf == nil {
			return "", false
		}
		name := extract.Text(leaf, src)
		if isReceiverClassName(name) {
			return name, true
		}
		// The call's RESULT is a QuerySet only when the method itself is a
		// builder (terminal methods like get/first return instances).
		if querySetChainMethods[name] {
			if obj := fn.ChildByFieldName("object"); isQuerySetExpr(obj, src, types) {
				return querySetTypeName, true
			}
		}
	}
	return "", false
}

// isReceiverClassName gates every name admitted as a receiver type: PascalCase
// and neither a primitive nor a bare generic wrapper (`x = typing.List(…)`
// must not type x as List).
func isReceiverClassName(name string) bool {
	return isPascalCase(name) && !pythonPrimitives[name] && !pythonGenericWrappers[name]
}

// annotationTypeName resolves an annotation node to a single class name: a
// bare PascalCase identifier, `Optional[X]` unwrapped to X, or the PascalCase
// leaf of a dotted annotation (`sql.Query`).
//
// This deliberately diverges from annotations.go's composes walk in two ways:
// a receiver has exactly ONE type, so only the first proving name is taken
// (composes emits an edge per referenced class), and a dotted annotation
// yields its leaf (targets bind by unqualified class name, while composes
// keeps the full dotted text).
func annotationTypeName(typeNode *sitter.Node, src []byte) (string, bool) {
	if typeNode == nil {
		return "", false
	}
	switch typeNode.Kind() {
	case "type":
		if typeNode.NamedChildCount() > 0 {
			return annotationTypeName(typeNode.NamedChild(0), src)
		}
	case "identifier":
		if name := extract.Text(typeNode, src); isReceiverClassName(name) {
			return name, true
		}
	case "generic_type":
		return genericAnnotationTypeName(typeNode, src)
	case "attribute":
		if leaf := typeNode.ChildByFieldName("attribute"); leaf != nil {
			if name := extract.Text(leaf, src); isReceiverClassName(name) {
				return name, true
			}
		}
	}
	return "", false
}

// genericAnnotationTypeName types a generic annotation for RECEIVER use.
// Only `Optional[X]` proves the receiver is X; every other wrapper lies about
// the receiver (`items: list[Item]` makes items a list, NOT an Item, and
// `Union[A, B]` proves neither) so they yield nothing — unlike annotations.go,
// where unwrapping the full wrapper set is correct for composes edges. A
// non-wrapper PascalCase outer name (`Registry[Task]`) IS the receiver type.
func genericAnnotationTypeName(typeNode *sitter.Node, src []byte) (string, bool) {
	outer := typeNode.ChildByFieldName("type")
	if outer == nil && typeNode.NamedChildCount() > 0 {
		outer = typeNode.NamedChild(0)
	}
	if outer == nil {
		return "", false
	}
	outerName := extract.Text(outer, src)
	if !pythonGenericWrappers[outerName] {
		if isReceiverClassName(outerName) {
			return outerName, true
		}
		return "", false
	}
	if outerName != "Optional" {
		return "", false
	}
	count := typeNode.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := typeNode.NamedChild(i)
		if child == nil || child.Kind() != "type_parameter" {
			continue
		}
		inner := child.NamedChildCount()
		for j := uint(0); j < inner; j++ {
			if t, ok := annotationTypeName(child.NamedChild(j), src); ok {
				return t, true
			}
		}
	}
	return "", false
}

// typedReceiverTarget resolves an attribute call to `<Type>.<method>` when
// the receiver's type is provable: an identifier in the local type map, or a
// django objects chain (django.go's isQuerySetExpr).
func typedReceiverTarget(fn *sitter.Node, src []byte, types map[string]string) (string, bool) {
	leaf := fn.ChildByFieldName("attribute")
	obj := fn.ChildByFieldName("object")
	if leaf == nil || obj == nil {
		return "", false
	}
	method := extract.Text(leaf, src)
	if method == "" {
		return "", false
	}
	if obj.Kind() == "identifier" {
		if t := types[extract.Text(obj, src)]; t != "" {
			return t + "." + method, true
		}
	}
	if isQuerySetExpr(obj, src, types) {
		return querySetTypeName + "." + method, true
	}
	return "", false
}
