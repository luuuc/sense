package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "c",
		Exts:      []string{".c", ".h"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier", "enum_specifier"},
		CallTypes:  []string{"call_expression"},
		ImportTypes: []string{"preproc_include"},

		NameField: "name",
	}))
}
