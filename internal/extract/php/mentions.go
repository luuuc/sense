package php

import (
	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// emitMentions streams every bare `name` token the file mentions (calls,
// property accesses, type references - everything except a definition's
// own name) to the emitter when it is a MentionEmitter. PHP symbols never
// earn `dead` (reasons-only), so today this only adds caution - but it
// keeps the per-language soundness gate able to distinguish "harvested,
// nothing mentioned" from "never harvested".
func (w *walker) emitMentions(root *sitter.Node) error {
	me, ok := w.emit.(extract.MentionEmitter)
	if !ok {
		return nil
	}
	spec := extract.MentionWalkSpec{
		NameOf:             map[string]func(*sitter.Node, []byte) string{"name": extract.Text},
		SkipDefinitionName: w.isDefinitionName,
	}
	for _, name := range extract.HarvestMentions(root, w.source, spec) {
		if err := me.MentionName(name); err != nil {
			return err
		}
	}
	return nil
}

// isDefinitionName reports whether n is a declaration's own name token -
// excluded from the mention set so a symbol is never kept open-world by
// its own declaration.
func (w *walker) isDefinitionName(n *sitter.Node) bool {
	p := n.Parent()
	if p == nil {
		return false
	}
	switch p.Kind() {
	case "class_declaration", "interface_declaration", "trait_declaration",
		"enum_declaration", "function_definition", "method_declaration":
	default:
		return false
	}
	name := p.ChildByFieldName("name")
	return name != nil && name.Equals(*n)
}
