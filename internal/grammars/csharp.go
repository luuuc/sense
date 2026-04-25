package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_csharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
)

func CSharp() *sitter.Language { return sitter.NewLanguage(ts_csharp.Language()) }
