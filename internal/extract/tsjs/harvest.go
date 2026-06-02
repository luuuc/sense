package tsjs

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// HarvestsMentions reports that the TS/JS extractor streams the broad mention
// set (see emitHarvest), so the scan records the language as harvested even on a
// scan that yields zero mentions — the dead-code soundness gate then treats a
// TS/JS symbol as proven-against-an-empty-set, not never-harvested. Without this
// opt-in every TS/JS symbol would fail closed at the per-language soundness gate
// (core_no_harvest) and none could earn `dead`. Declared on each of the three
// extractor types (TypeScript, TSX, JavaScript) so all three languages register
// as harvested — they share this one walker.
func (TypeScript) HarvestsMentions() bool { return true }
func (TSX) HarvestsMentions() bool        { return true }
func (JavaScript) HarvestsMentions() bool { return true }

// emitHarvest streams the file's four dead-code fact sets to the emitter when it
// accepts them, all from one set of tree walks (scan is not a hot path):
//
//   - mentions (MentionEmitter): every identifier / property / type token except a
//     definition's own name — the broad superset feeding the arbiter's soundness
//     gate. A method invoked as `obj.render()` leaves a `render` property mention,
//     so a same-named method stays open-world instead of falsely `dead`.
//   - dispatch (DispatchEmitter): the literal key of every computed-property access
//     (`obj["render"]`) — the JS/TS analog of Ruby `send`. A name reached this way
//     is invisible to the static graph, so the core voice keeps it open-world.
//   - decorated names (TSHarvestEmitter): the name of every class / method carrying
//     a decorator (`@Component`, `@Injectable`, `@Get`). A framework's DI/router
//     instantiates or routes to it with no source caller, so the TS voice keeps it
//     open-world (ts_decorator) even when module-private.
//   - default-export names (TSHarvestEmitter): the name bound by each `export
//     default` form, so the TS voice can raise the more specific ts_default_export.
//
// Each emit is best-effort — an Emitter that implements none of the extensions
// simply receives no names.
func emitHarvest(root *sitter.Node, source []byte, filePath string, emit extract.Emitter) error {
	if me, ok := emit.(extract.MentionEmitter); ok {
		for _, name := range extract.HarvestMentions(root, source, mentionWalkSpec()) {
			if err := me.MentionName(name); err != nil {
				return err
			}
		}
	}
	if de, ok := emit.(extract.DispatchEmitter); ok {
		for name := range collectComputedDispatch(root, source) {
			if err := de.DispatchName(name); err != nil {
				return err
			}
		}
	}
	if te, ok := emit.(extract.TSHarvestEmitter); ok {
		for name := range collectDecoratedNames(root, source) {
			if err := te.TSDecoratedName(name); err != nil {
				return err
			}
		}
		_, defaults := collectExportedNames(root, source, filePath)
		for name := range defaults {
			if err := te.TSDefaultExportName(name); err != nil {
				return err
			}
		}
	}
	return nil
}

// mentionWalkSpec is the TS/JS grammar parameterisation of the shared mention
// harvest. Plain identifiers, member-access property names, type references, and
// `{ foo }` shorthand keys each carry a mention; a definition's own name token is
// excluded so a symbol is never cancelled by its own declaration. property_identifier
// is included so a `obj.render()` call keeps a same-named method open-world even
// when the resolver could not bind the member access to the class.
func mentionWalkSpec() extract.MentionWalkSpec {
	return extract.MentionWalkSpec{
		NameOf: map[string]func(*sitter.Node, []byte) string{
			"identifier":                    extract.Text,
			"property_identifier":           extract.Text,
			"type_identifier":               extract.Text,
			"shorthand_property_identifier": extract.Text,
		},
		SkipDefinitionName: isTSDefinitionName,
	}
}

// isTSDefinitionName reports whether n is the `name` field of a symbol-producing
// declaration (function / class / interface / type-alias / enum / method /
// const declarator). Those tokens are excluded from the mention set so a symbol
// does not count as a mention of itself — otherwise no symbol could ever earn
// `dead`. Class-field and parameter names are intentionally NOT excluded: leaving
// them in the set only ever keeps a same-named symbol open-world (the safe
// direction).
func isTSDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "function_declaration", "generator_function_declaration",
		"class_declaration", "abstract_class_declaration",
		"interface_declaration", "type_alias_declaration", "enum_declaration",
		"method_definition", "variable_declarator":
		name := p.ChildByFieldName("name")
		return name != nil && name.Id() == n.Id()
	}
	return false
}

// collectComputedDispatch returns the set of string-literal keys used in
// computed-property access (`obj["render"]`, `obj["render"]()`). A non-literal
// index (`obj[name]`) names no statically-knowable member, so it is skipped — the
// safe direction (the target stays whatever the static graph says). These are the
// reflective dispatch targets the core voice keeps open-world.
func collectComputedDispatch(root *sitter.Node, source []byte) map[string]struct{} {
	seen := map[string]struct{}{}
	_ = extract.WalkNamedDescendants(root, "subscript_expression", func(n *sitter.Node) error {
		idx := n.ChildByFieldName("index")
		if idx == nil || idx.Kind() != "string" {
			return nil
		}
		if name := stringFragmentText(idx, source); name != "" {
			seen[name] = struct{}{}
		}
		return nil
	})
	return seen
}

// collectDecoratedNames returns the set of class and method names carrying a
// decorator. A decorator attaches as a `decorator` node in three structural
// positions — preceding a bare class_declaration, inside the export_statement
// that wraps an exported class, or inside a class_body preceding a
// method_definition — so the decorated symbol's name is read from the decorator's
// structural context. Name-based over-approximation is safe: a decorated name only
// ever keeps a symbol open-world (ts_decorator), never forces `dead`.
func collectDecoratedNames(root *sitter.Node, source []byte) map[string]struct{} {
	seen := map[string]struct{}{}
	_ = extract.WalkNamedDescendants(root, "decorator", func(d *sitter.Node) error {
		if name := decoratedName(d, source); name != "" {
			seen[name] = struct{}{}
		}
		return nil
	})
	return seen
}

// decoratedName returns the name of the symbol a decorator node annotates, read
// from its parent context: a class_declaration's name (bare decorated class), the
// declaration named in a wrapping export_statement (exported decorated class), or
// the next method_definition sibling in a class_body (decorated method).
func decoratedName(d *sitter.Node, source []byte) string {
	p := d.Parent()
	if p == nil {
		return ""
	}
	switch p.Kind() {
	case "class_declaration", "abstract_class_declaration":
		return extract.Text(p.ChildByFieldName("name"), source)
	case "export_statement":
		if decl := p.ChildByFieldName("declaration"); decl != nil {
			return extract.Text(decl.ChildByFieldName("name"), source)
		}
	case "class_body":
		for sib := d.NextNamedSibling(); sib != nil; sib = sib.NextNamedSibling() {
			if sib.Kind() == "method_definition" {
				return extract.Text(sib.ChildByFieldName("name"), source)
			}
			if sib.Kind() != "decorator" {
				return ""
			}
		}
	}
	return ""
}

// stringFragmentText returns the text of a string node's string_fragment child
// (`"render"` → `render`), or "" for an empty string or one with no fragment.
func stringFragmentText(str *sitter.Node, source []byte) string {
	for i := uint(0); i < str.NamedChildCount(); i++ {
		c := str.NamedChild(i)
		if c != nil && c.Kind() == "string_fragment" {
			return extract.Text(c, source)
		}
	}
	return ""
}
