package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "csharp",
		Exts:      []string{".cs"},
		Grammar:   grammars.CSharp(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"method_declaration", "constructor_declaration"},
		ClassTypes: []string{"class_declaration", "interface_declaration", "struct_declaration", "enum_declaration", "record_declaration", "namespace_declaration"},
		CallTypes:  []string{"invocation_expression", "object_creation_expression"},
		ImportTypes: []string{"using_directive"},

		InheritKinds: []string{"base_list"},

		NameField: "name",
	}))
}
