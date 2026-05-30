package ruby

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// visibilityKeywords are the three Module visibility setters. A bare one (on
// its own line) flips the default visibility for subsequent defs in the same
// body; one with arguments sets the visibility of the named (or inline)
// methods.
var visibilityKeywords = map[string]bool{
	"private":   true,
	"protected": true,
	"public":    true,
}

// recordBodyVisibility computes the visibility of each instance method
// directly defined in a class/module body and records it on the walker keyed
// by the method's qualified name (classQualified#method). It mirrors the
// `classInstanceVars` pre-pass: a single ordered walk of the body's direct
// children, tracking the running default visibility (`public` until a bare
// `private`/`protected` flips it) and applying retroactive `private :sym` /
// inline `private def m` forms.
//
// Only DIRECT children of the body are considered, so nested classes, modules,
// and conditionals get their own (or no) pass — never this class's. Only
// instance methods are tracked; singleton methods (`def self.x`) keep the
// default public, matching that `private` does not affect them. Conservatism
// is deliberate: a method this pass cannot classify stays public, and a public
// method can never earn `dead` — so the only way this could cause a false
// `dead` is to wrongly mark a public method private, which the explicit,
// structural rules here do not do.
func (w *walker) recordBodyVisibility(body *sitter.Node, classQualified string) {
	if body == nil || classQualified == "" {
		return
	}
	current := "public"
	count := body.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := body.NamedChild(i)
		if child == nil {
			continue
		}
		switch child.Kind() {
		case "method":
			if name := methodName(child, w.source); name != "" {
				w.methodVisibility[classQualified+"#"+name] = current
			}
		case "identifier":
			// A bare visibility keyword flips the running default.
			if kw := extract.Text(child, w.source); visibilityKeywords[kw] {
				current = kw
			}
		case "call":
			w.applyVisibilityCall(child, classQualified)
		}
	}
}

// applyVisibilityCall handles the argument forms of a visibility setter:
// `private :a, :b` (retroactive, by name) and `private def m; end` (inline).
// A call that is not a visibility setter, or whose arguments are neither
// symbols/strings nor an inline method, is ignored.
func (w *walker) applyVisibilityCall(call *sitter.Node, classQualified string) {
	methodNode := call.ChildByFieldName("method")
	if methodNode == nil {
		return
	}
	kw := extract.Text(methodNode, w.source)
	if !visibilityKeywords[kw] {
		return
	}
	args := call.ChildByFieldName("arguments")
	if args == nil {
		return
	}
	n := args.NamedChildCount()
	for i := uint(0); i < n; i++ {
		arg := args.NamedChild(i)
		if arg == nil {
			continue
		}
		switch arg.Kind() {
		case "simple_symbol", "string":
			if name := symbolOrStringName(arg, w.source); name != "" {
				w.methodVisibility[classQualified+"#"+name] = kw
			}
		case "method":
			if name := methodName(arg, w.source); name != "" {
				w.methodVisibility[classQualified+"#"+name] = kw
			}
		}
	}
}

// methodName returns a method node's name, or "" when absent.
func methodName(methodNode *sitter.Node, source []byte) string {
	nameNode := methodNode.ChildByFieldName("name")
	if nameNode == nil {
		return ""
	}
	return extract.Text(nameNode, source)
}

// symbolOrStringName extracts the bare name from a `simple_symbol` (`:foo` →
// `foo`) or a `string` literal (`"foo"` → `foo`). Returns "" for any other
// shape (interpolated strings, etc.).
func symbolOrStringName(node *sitter.Node, source []byte) string {
	switch node.Kind() {
	case "simple_symbol":
		return strings.TrimPrefix(extract.Text(node, source), ":")
	case "string":
		count := node.NamedChildCount()
		for i := uint(0); i < count; i++ {
			c := node.NamedChild(i)
			if c != nil && c.Kind() == "string_content" {
				return extract.Text(c, source)
			}
		}
	}
	return ""
}

// firstArgDispatchNames are the reflection/metaprogramming methods whose
// dispatch target is the FIRST argument as a literal symbol or string.
var firstArgDispatchNames = map[string]bool{
	"send":          true,
	"public_send":   true,
	"__send__":      true,
	"define_method": true,
	"respond_to?":   true,
	"method":        true,
	"const_get":     true,
}

// collectDispatchNames walks every call node under root and returns the set of
// literal names the file dispatches on reflectively: the first symbol/string
// argument to send/public_send/__send__/define_method/respond_to?/method/
// const_get, and the receiver string of `"Name".constantize`. The set drives
// the dead-code arbiter's reflection gate — a symbol whose name appears here
// could be invoked dynamically, so it must stay open-world. Names are
// deduplicated; order is unspecified (the caller writes them to a set).
func collectDispatchNames(root *sitter.Node, source []byte) []string {
	seen := map[string]struct{}{}
	_ = extract.WalkNamedDescendants(root, "call", func(c *sitter.Node) error {
		methodNode := c.ChildByFieldName("method")
		if methodNode == nil {
			return nil
		}
		name := extract.Text(methodNode, source)
		switch {
		case firstArgDispatchNames[name]:
			if t := firstArgName(c, source); t != "" {
				seen[t] = struct{}{}
			}
		case name == "constantize":
			if t := receiverStringName(c, source); t != "" {
				seen[t] = struct{}{}
			}
		}
		return nil
	})
	if len(seen) == 0 {
		return nil
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	return out
}

// firstArgName returns the literal symbol/string name of a call's first
// argument, or "" when it is absent or not a literal.
func firstArgName(call *sitter.Node, source []byte) string {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	return symbolOrStringName(args.NamedChild(0), source)
}

// receiverStringName returns the literal name of a call's receiver when it is
// a string (`"Thing".constantize` → `Thing`), or "" otherwise.
func receiverStringName(call *sitter.Node, source []byte) string {
	recv := call.ChildByFieldName("receiver")
	if recv == nil || recv.Kind() != "string" {
		return ""
	}
	return symbolOrStringName(recv, source)
}
