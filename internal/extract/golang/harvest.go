package golang

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// HarvestsMentions reports that the Go extractor streams the broad mention set
// (see emitHarvest), so the scan records `go` as harvested even on a scan that
// yields zero mentions — the dead-code soundness gate then treats a Go symbol as
// proven-against-an-empty-set, not never-harvested. Without this opt-in, every
// Go symbol would fail closed at the per-language soundness gate (core_no_harvest)
// and no Go symbol could ever earn `dead`.
func (Extractor) HarvestsMentions() bool { return true }

// emitHarvest streams the file's reflective dispatch-target names, cgo `//export`
// names, and broad mention set to the emitter when it accepts them. The dispatch
// set feeds the core voice's reflection gate (a name reached via
// reflect.MethodByName / FieldByName, or a tagged struct field, can be invoked
// invisibly); the cgo set feeds the Go voice's go_cgo reason; the mention set
// feeds the arbiter's soundness gate (a symbol earns `dead` only when its bare
// name is mentioned nowhere a hidden caller could be). All three are gathered in
// ONE tree walk; each emit is best-effort — an Emitter that implements neither
// extension simply receives no names.
func emitHarvest(root *sitter.Node, source []byte, emit extract.Emitter) error {
	h := collectGoHarvest(root, source)
	if de, ok := emit.(extract.DispatchEmitter); ok {
		for name := range h.dispatch {
			if err := de.DispatchName(name); err != nil {
				return err
			}
		}
	}
	if ce, ok := emit.(extract.CgoExportEmitter); ok {
		for name := range h.cgoExports {
			if err := ce.CgoExportName(name); err != nil {
				return err
			}
		}
	}
	if me, ok := emit.(extract.MentionEmitter); ok {
		for name := range h.mentions {
			if err := me.MentionName(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// goHarvest accumulates the three name sets the Go dead-code analysis needs,
// gathered in a single tree walk:
//   - mentions: every identifier / type-identifier / field-identifier token
//     EXCEPT a definition's own name — the broad superset feeding the arbiter's
//     soundness gate (a candidate earns `dead` only when its name is absent here,
//     i.e. mentioned nowhere a hidden caller could be).
//   - dispatch: names reached by reflection — the string argument to a
//     `.MethodByName(...)` / `.FieldByName(...)` call, and the Go name of every
//     tagged struct field (json/gorm/… reach the field by reflection).
//   - cgoExports: function names marked with a cgo `//export <name>` directive,
//     called from C with no Go caller.
type goHarvest struct {
	mentions   map[string]struct{}
	dispatch   map[string]struct{}
	cgoExports map[string]struct{}
}

// collectGoHarvest walks every named node ONCE and routes it to the right set.
// A single pass replaces the dozen kind-filtered traversals the three separate
// harvests would otherwise each make. Scan is not a hot path, but one walk is
// both faster and the clearer statement of intent: look at every node, decide
// what it contributes.
func collectGoHarvest(root *sitter.Node, source []byte) goHarvest {
	h := goHarvest{
		mentions:   map[string]struct{}{},
		dispatch:   map[string]struct{}{},
		cgoExports: map[string]struct{}{},
	}
	walkNamed(root, func(n *sitter.Node) {
		switch n.Kind() {
		case "call_expression":
			h.addReflectDispatch(n, source)
		case "field_declaration":
			h.addTaggedFields(n, source)
		case "comment":
			h.addCgoExport(n, source)
		case "identifier", "type_identifier", "field_identifier":
			if !isGoDefinitionName(n) {
				if name := extract.Text(n, source); name != "" {
					h.mentions[name] = struct{}{}
				}
			}
		}
	})
	return h
}

// walkNamed invokes visit on every named descendant of n (not n itself), once,
// depth-first — the single-pass, all-kinds counterpart to the kind-filtered
// extract.WalkNamedDescendants.
func walkNamed(n *sitter.Node, visit func(*sitter.Node)) {
	if n == nil {
		return
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		visit(child)
		walkNamed(child, visit)
	}
}

// isGoDefinitionName reports whether n is a definition's OWN name token: the
// `name` field of a func / method / type declaration, or a declared identifier
// in a const / var spec. The mention harvest skips these so a symbol is never
// cancelled by its own definition (which would mute `dead` entirely). Checking
// the parent at visit time replaces a precomputed ID set, keeping the harvest a
// single pass. Interface method declarations (`method_elem`) are deliberately
// NOT matched: leaving an interface method's name in the mention set keeps a
// same-named concrete implementor open-world even when the resolver never drew
// the implicit satisfaction edge.
func isGoDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "function_declaration", "method_declaration", "type_spec", "type_alias":
		name := p.ChildByFieldName("name")
		return name != nil && name.Id() == n.Id()
	case "const_spec", "var_spec":
		// Names are the spec's direct `identifier` children; the type is a
		// type_identifier and the value is nested under an expression_list, so
		// neither is mistaken for a name.
		return n.Kind() == "identifier"
	}
	return false
}

// addReflectDispatch records the string argument of a `.MethodByName(...)` /
// `.FieldByName(...)` call as a reflection-reached name.
func (h goHarvest) addReflectDispatch(call *sitter.Node, source []byte) {
	fn := call.ChildByFieldName("function")
	if fn == nil || fn.Kind() != "selector_expression" {
		return
	}
	field := fn.ChildByFieldName("field")
	if field == nil || !reflectDispatchMethods[extract.Text(field, source)] {
		return
	}
	if name := firstStringArg(call, source); name != "" {
		h.dispatch[name] = struct{}{}
	}
}

// addTaggedFields records the Go name of every field in a tagged struct field
// declaration as reflection-reachable. Forward-looking: Tier-Basic does not yet
// emit struct fields as symbols, so they cannot be dead candidates today, but
// keying them now keeps the gate sound the moment fields are indexed.
func (h goHarvest) addTaggedFields(fd *sitter.Node, source []byte) {
	if !hasStructTag(fd) {
		return
	}
	for i := uint(0); i < fd.NamedChildCount(); i++ {
		c := fd.NamedChild(i)
		if c != nil && c.Kind() == "field_identifier" {
			if name := extract.Text(c, source); name != "" {
				h.dispatch[name] = struct{}{}
			}
		}
	}
}

// addCgoExport records the function name of a cgo `//export <name>` directive.
func (h goHarvest) addCgoExport(c *sitter.Node, source []byte) {
	rest, ok := strings.CutPrefix(extract.Text(c, source), cgoExportPrefix)
	if !ok || rest == "" || !isSpace(rest[0]) {
		// Require whitespace after the directive so `//exported` (a normal
		// comment) is not mistaken for `//export ed`.
		return
	}
	if fields := strings.Fields(rest); len(fields) > 0 {
		h.cgoExports[fields[0]] = struct{}{}
	}
}

// cgoExportPrefix is the cgo directive that makes a Go function callable from C:
// `//export Name`. The directive is a line comment whose text begins exactly
// with `//export` followed by the exported Go function name.
const cgoExportPrefix = "//export"

// isSpace reports whether b is an ASCII space or tab — the separator cgo allows
// between the `//export` directive and the function name.
func isSpace(b byte) bool { return b == ' ' || b == '\t' }

// reflectDispatchMethods are the reflect (and reflect.Value) methods whose first
// string argument names a symbol reached dynamically: reflect.Value.MethodByName
// invokes a method by name, FieldByName reads a field by name. A symbol whose
// name appears as one of these literals could be reached invisibly, so the core
// voice keeps it open-world.
var reflectDispatchMethods = map[string]bool{
	"MethodByName": true,
	"FieldByName":  true,
}

// firstStringArg returns the content of a call's first argument when it is a
// string literal, else "". Used to read the symbol name out of
// `MethodByName("Foo")` / `FieldByName("Foo")`.
func firstStringArg(call *sitter.Node, source []byte) string {
	args := call.ChildByFieldName("arguments")
	if args == nil || args.NamedChildCount() == 0 {
		return ""
	}
	return stringLiteralContent(args.NamedChild(0), source)
}

// stringLiteralContent returns the inner text of an interpreted or raw string
// literal (without the surrounding quotes/backticks), or "" for any other node
// or an empty literal.
func stringLiteralContent(n *sitter.Node, source []byte) string {
	if n == nil {
		return ""
	}
	switch n.Kind() {
	case "interpreted_string_literal", "raw_string_literal":
		for i := uint(0); i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			if c == nil {
				continue
			}
			if c.Kind() == "interpreted_string_literal_content" || c.Kind() == "raw_string_literal_content" {
				return extract.Text(c, source)
			}
		}
	}
	return ""
}

// hasStructTag reports whether a field_declaration carries a tag — a trailing
// string literal after the field type (`Name string ` + "`json:\"name\"`" + `).
func hasStructTag(fd *sitter.Node) bool {
	for i := uint(0); i < fd.NamedChildCount(); i++ {
		c := fd.NamedChild(i)
		if c != nil && (c.Kind() == "raw_string_literal" || c.Kind() == "interpreted_string_literal") {
			return true
		}
	}
	return false
}
