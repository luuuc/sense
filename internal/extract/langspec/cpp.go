package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "cpp",
		Exts:      []string{".cpp", ".cc", ".cxx", ".hpp", ".hxx"},
		Grammar:   grammars.Cpp(),
		Tier:      extract.TierStandard,
		Separator: "::",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_specifier", "struct_specifier", "namespace_definition", "enum_specifier"},
		CallTypes:  []string{"call_expression"},
		ImportTypes: []string{"preproc_include"},

		InheritKinds: []string{"base_class_clause"},

		NameField: "name",
	}))
}
