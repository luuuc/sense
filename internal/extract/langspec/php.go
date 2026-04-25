package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "php",
		Exts:      []string{".php"},
		Grammar:   grammars.PHP(),
		Tier:      extract.TierStandard,
		Separator: "\\",

		FuncTypes:  []string{"function_definition", "method_declaration"},
		ClassTypes: []string{"class_declaration", "interface_declaration", "trait_declaration", "enum_declaration", "namespace_definition"},
		CallTypes:  []string{"function_call_expression", "member_call_expression", "scoped_call_expression"},
		ImportTypes: []string{"namespace_use_declaration"},

		InheritKinds: []string{"base_clause", "class_interface_clause"},

		NameField: "name",
	}))
}
