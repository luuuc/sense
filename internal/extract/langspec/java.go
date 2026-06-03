package langspec

import (
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

func init() {
	extract.Register(New(langSpec{
		Name:      "java",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"method_declaration", "constructor_declaration"},
		ClassTypes:  []string{"class_declaration", "interface_declaration", "enum_declaration", "record_declaration"},
		CallTypes:   []string{"method_invocation", "object_creation_expression"},
		ImportTypes: []string{"import_declaration"},

		InheritFields: []string{"superclass", "interfaces"},

		NameField: "name",

		VisibilityFn:    javaVisibility,
		AnnotationKinds: []string{"marker_annotation", "annotation"},
		// Java is the one langspec language with a real-world benchmark repo
		// (javalin), so it harvests mentions and its file-local symbols may earn
		// `dead` (gated by the per-language decision in the langspec voice).
		MentionKinds: []string{"identifier", "type_identifier"},
	}))
}
