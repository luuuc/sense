package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "scala",
		Exts:      []string{".scala", ".sc"},
		Grammar:   grammars.Scala(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition", "function_declaration"},
		ClassTypes: []string{"class_definition", "object_definition", "trait_definition", "enum_definition"},
		CallTypes:  []string{"call_expression"},
		ImportTypes: []string{"import_declaration"},

		InheritFields: []string{"extend"},

		NameField: "name",
	}))
}
