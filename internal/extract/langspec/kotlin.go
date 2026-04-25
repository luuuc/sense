package langspec

import (
	"strings"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "kotlin",
		Exts:      []string{".kt", ".kts"},
		Grammar:   grammars.Kotlin(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_declaration"},
		ClassTypes: []string{"class_declaration", "object_declaration"},
		CallTypes:  []string{"call_expression"},
		ImportTypes: []string{"import_header"},

		InheritKinds: []string{"delegation_specifier"},

		NameField: "name",

		CallNameFn: kotlinCallName,
	}))
}

func kotlinCallName(n *sitter.Node, source []byte) string {
	if n.NamedChildCount() == 0 {
		return ""
	}
	callee := n.NamedChild(0)
	if callee == nil {
		return ""
	}
	return strings.TrimSpace(extract.Text(callee, source))
}
