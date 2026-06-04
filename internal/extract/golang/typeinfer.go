package golang

import (
	"strings"
	"unicode"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// This file holds the Go type-inference cluster: the machinery that figures out
// a local variable's type so a `x.Method()` selector call can resolve to
// `pkg.Type.Method` instead of the raw `x.Method`. Symbol/edge emission and the
// top-level walk live in golang.go; everything here is the type map and the
// helpers that populate and read it.

// localType tracks a variable's resolved type within a function body.
type localType struct {
	name       string  // unqualified type name (e.g. "Order")
	elemName   string  // element type for slices/arrays (for range resolution)
	confidence float64 // 1.0 for explicit declarations, 0.8 for inferred
}

// buildTypeMap scans a function/method declaration for local variable
// type information and builds a set of all known local variable names.
// Type sources: parameters, receiver, var declarations, short
// declarations with composite literals or constructor calls, range
// variables. The locals set tracks every declared variable name
// (even those with unknown types) so callers can distinguish
// unresolved locals from package references.
func (w *walker) buildTypeMap(funcNode *sitter.Node) (map[string]localType, map[string]bool) {
	types := map[string]localType{}
	locals := map[string]bool{}

	w.collectReceiverTypes(funcNode.ChildByFieldName("receiver"), types, locals)
	w.collectParamTypes(funcNode.ChildByFieldName("parameters"), types, locals)

	body := funcNode.ChildByFieldName("body")
	if body == nil {
		return types, locals
	}
	_ = extract.WalkNamedDescendants(body, "var_declaration", func(n *sitter.Node) error {
		w.collectVarDecl(n, types, locals)
		return nil
	})
	_ = extract.WalkNamedDescendants(body, "short_var_declaration", func(n *sitter.Node) error {
		w.collectShortVarDecl(n, types, locals)
		return nil
	})
	_ = extract.WalkNamedDescendants(body, "range_clause", func(n *sitter.Node) error {
		w.collectRangeVars(n, types, locals)
		return nil
	})
	return types, locals
}

// collectReceiverTypes records the method receiver's name → type binding so a
// method body's `r.Other()` calls resolve against the receiver type.
func (w *walker) collectReceiverTypes(recv *sitter.Node, types map[string]localType, locals map[string]bool) {
	if recv == nil {
		return
	}
	for i := uint(0); i < recv.NamedChildCount(); i++ {
		pd := recv.NamedChild(i)
		if pd == nil || pd.Kind() != "parameter_declaration" {
			continue
		}
		name := extract.Text(pd.ChildByFieldName("name"), w.source)
		typeName := unwrapTypeName(pd.ChildByFieldName("type"), w.source)
		if name != "" && typeName != "" {
			types[name] = localType{typeName, "", extract.ConfidenceStatic}
			locals[name] = true
		}
	}
}

// collectParamTypes records each parameter's name → type binding, keeping the
// element type for slice/array params so a `range param` loop resolves.
func (w *walker) collectParamTypes(params *sitter.Node, types map[string]localType, locals map[string]bool) {
	if params == nil {
		return
	}
	for i := uint(0); i < params.NamedChildCount(); i++ {
		pd := params.NamedChild(i)
		if pd == nil || pd.Kind() != "parameter_declaration" {
			continue
		}
		typeName, elemName := resolveTypeAndElem(pd.ChildByFieldName("type"), w.source)
		if typeName == "" && elemName == "" {
			continue
		}
		for j := uint(0); j < pd.NamedChildCount(); j++ {
			ch := pd.NamedChild(j)
			if ch.Kind() == "identifier" {
				name := extract.Text(ch, w.source)
				types[name] = localType{typeName, elemName, extract.ConfidenceStatic}
				locals[name] = true
			}
		}
	}
}

// collectVarDecl handles `var x Type` and `var x []Type` declarations.
func (w *walker) collectVarDecl(n *sitter.Node, types map[string]localType, locals map[string]bool) {
	for i := uint(0); i < n.NamedChildCount(); i++ {
		spec := n.NamedChild(i)
		if spec == nil || spec.Kind() != "var_spec" {
			continue
		}
		typeNode := spec.ChildByFieldName("type")
		typeName, elemName := resolveTypeAndElem(typeNode, w.source)
		for j := uint(0); j < spec.NamedChildCount(); j++ {
			ch := spec.NamedChild(j)
			if ch.Kind() == "identifier" {
				name := extract.Text(ch, w.source)
				locals[name] = true
				if typeName != "" || elemName != "" {
					types[name] = localType{typeName, elemName, extract.ConfidenceStatic}
				}
			}
		}
	}
}

// collectShortVarDecl handles `x := expr` — extracts type from
// composite literals (Order{...}) and constructor calls (NewOrder()).
func (w *walker) collectShortVarDecl(n *sitter.Node, types map[string]localType, locals map[string]bool) {
	left := n.ChildByFieldName("left")
	right := n.ChildByFieldName("right")
	if left == nil || right == nil {
		return
	}
	lhsCount := left.NamedChildCount()
	rhsCount := right.NamedChildCount()
	if lhsCount == 0 || rhsCount == 0 {
		return
	}
	for i := uint(0); i < lhsCount; i++ {
		varNode := left.NamedChild(i)
		if varNode == nil || varNode.Kind() != "identifier" {
			continue
		}
		varName := extract.Text(varNode, w.source)
		locals[varName] = true
		if i < rhsCount {
			valNode := right.NamedChild(i)
			if lt, ok := w.inferType(valNode); ok {
				types[varName] = lt
			}
		}
	}
}

// collectRangeVars handles `for _, v := range src` — assigns the element type of
// the range source to the value variable.
func (w *walker) collectRangeVars(rc *sitter.Node, types map[string]localType, locals map[string]bool) {
	left := rc.ChildByFieldName("left")
	right := rc.ChildByFieldName("right")
	if left == nil || right == nil {
		return
	}
	w.registerRangeLocals(left, locals)

	valueNode := rangeValueNode(left)
	if valueNode == nil {
		return
	}
	valueName := extract.Text(valueNode, w.source)
	if valueName == "" || valueName == "_" {
		return
	}
	if elemName := w.rangeElemType(right, types); elemName != "" {
		types[valueName] = localType{elemName, "", extract.ConfidenceStatic}
	}
}

// registerRangeLocals marks every loop variable in the range's left list as a
// local, so an unresolved loop variable is not mistaken for a package reference.
func (w *walker) registerRangeLocals(left *sitter.Node, locals map[string]bool) {
	for i := uint(0); i < left.NamedChildCount(); i++ {
		ch := left.NamedChild(i)
		if ch != nil && ch.Kind() == "identifier" {
			locals[extract.Text(ch, w.source)] = true
		}
	}
}

// rangeValueNode returns the value variable of a range clause: the second
// identifier in `for k, v := range`. A single-variable `for i := range` binds
// the key/index (not the element), so it is intentionally not type-resolved and
// this returns nil.
func rangeValueNode(left *sitter.Node) *sitter.Node {
	count := 0
	for i := uint(0); i < left.NamedChildCount(); i++ {
		ch := left.NamedChild(i)
		if ch != nil && ch.Kind() == "identifier" {
			count++
			if count == 2 {
				return ch
			}
		}
	}
	return nil
}

// rangeElemType determines the element type produced by ranging over `right`,
// reading a known slice/array variable's element type or a composite literal's.
func (w *walker) rangeElemType(right *sitter.Node, types map[string]localType) string {
	switch right.Kind() {
	case "identifier":
		if lt, ok := types[extract.Text(right, w.source)]; ok {
			return lt.elemName
		}
	case "composite_literal":
		if typeNode := right.ChildByFieldName("type"); typeNode != nil {
			return sliceElemType(typeNode, w.source)
		}
	}
	return ""
}

// resolveSelector attempts to resolve a selector_expression callee
// (e.g. `x.Method`) using the local type map. Returns the target
// qualified name and confidence. When the operand is a known local
// variable without a resolved type, confidence drops to 0.8;
// unknown operands (likely package references like `fmt`) stay at 1.0.
func (w *walker) resolveSelector(sel *sitter.Node, types map[string]localType, locals map[string]bool) (string, float64) {
	operand := sel.ChildByFieldName("operand")
	field := sel.ChildByFieldName("field")
	if operand == nil || field == nil {
		return extract.Text(sel, w.source), extract.ConfidenceStatic
	}
	if operand.Kind() != "identifier" {
		return extract.Text(sel, w.source), extract.ConfidenceStatic
	}
	varName := extract.Text(operand, w.source)
	methodName := extract.Text(field, w.source)
	if varName == "" || methodName == "" {
		return "", 0
	}
	lt, ok := types[varName]
	if !ok || lt.name == "" {
		confidence := extract.ConfidenceStatic
		if locals[varName] || ok {
			confidence = extract.ConfidenceAmbiguous
		}
		return varName + "." + methodName, confidence
	}
	return w.qualify(lt.name) + "." + methodName, lt.confidence
}

// inferType attempts to determine the type of a value expression by dispatching
// on the expression kind.
func (w *walker) inferType(val *sitter.Node) (localType, bool) {
	if val == nil {
		return localType{}, false
	}
	switch val.Kind() {
	case "composite_literal":
		return w.inferFromComposite(val)
	case "unary_expression":
		return w.inferFromUnary(val)
	case "call_expression":
		return w.inferFromCall(val)
	}
	return localType{}, false
}

// inferFromComposite reads the type of a composite literal `T{...}`, returning
// the element type for a slice/array literal `[]T{...}`.
func (w *walker) inferFromComposite(val *sitter.Node) (localType, bool) {
	typeNode := val.ChildByFieldName("type")
	if typeNode == nil {
		typeNode = val.NamedChild(0)
	}
	if typeNode != nil {
		if typeNode.Kind() == "slice_type" || typeNode.Kind() == "array_type" {
			if elemName := sliceElemType(typeNode, w.source); elemName != "" {
				return localType{"", elemName, extract.ConfidenceStatic}, true
			}
		}
		if typeName := unwrapTypeName(typeNode, w.source); typeName != "" {
			return localType{typeName, "", extract.ConfidenceStatic}, true
		}
	}
	return localType{}, false
}

// inferFromUnary reads the type behind `&T{...}` (address-of a composite literal).
func (w *walker) inferFromUnary(val *sitter.Node) (localType, bool) {
	operand := val.ChildByFieldName("operand")
	if operand == nil || operand.Kind() != "composite_literal" {
		return localType{}, false
	}
	if typeName := unwrapTypeName(operand.NamedChild(0), w.source); typeName != "" {
		return localType{typeName, "", extract.ConfidenceStatic}, true
	}
	return localType{}, false
}

// inferFromCall infers a type from a constructor call `NewT()` → "T".
func (w *walker) inferFromCall(val *sitter.Node) (localType, bool) {
	fn := val.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "identifier" {
		return localType{}, false
	}
	if typeName := constructorType(extract.Text(fn, w.source)); typeName != "" {
		return localType{typeName, "", extract.ConfidenceAmbiguous}, true
	}
	return localType{}, false
}

// constructorType extracts "Order" from "NewOrder" or "newOrder".
func constructorType(funcName string) string {
	if len(funcName) <= 3 {
		return ""
	}
	if !strings.HasPrefix(funcName, "New") && !strings.HasPrefix(funcName, "new") {
		return ""
	}
	typeName := funcName[3:]
	r, _ := utf8.DecodeRuneInString(typeName)
	if r == utf8.RuneError || !unicode.IsUpper(r) {
		return ""
	}
	return typeName
}

// resolveTypeAndElem extracts the type name and optional element type
// from a type node. For slice/array types, elemName is the element
// type; for plain types, elemName is empty.
func resolveTypeAndElem(typeNode *sitter.Node, source []byte) (typeName, elemName string) {
	if typeNode == nil {
		return "", ""
	}
	if typeNode.Kind() == "slice_type" || typeNode.Kind() == "array_type" {
		elem := sliceElemType(typeNode, source)
		return "", elem
	}
	return unwrapTypeName(typeNode, source), ""
}

// sliceElemType extracts the element type from a slice_type or
// array_type node via the `element` field.
func sliceElemType(typeNode *sitter.Node, source []byte) string {
	elem := typeNode.ChildByFieldName("element")
	return unwrapTypeName(elem, source)
}

// unwrapTypeName peels pointer and generic wrappers off a type
// expression to get at the base type_identifier.
func unwrapTypeName(t *sitter.Node, source []byte) string {
	for t != nil {
		switch t.Kind() {
		case "type_identifier":
			return extract.Text(t, source)
		case "pointer_type":
			// `*T` has exactly one named child — the inner type.
			t = t.NamedChild(0)
		case "generic_type":
			if name := t.ChildByFieldName("type"); name != nil {
				t = name
				continue
			}
			return ""
		default:
			// Qualified types like `pkg.Type` (qualified_type node)
			// land here. Skip — cross-package resolution is 01-03's
			// job, not Tier-Basic's.
			return ""
		}
	}
	return ""
}
