package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
)

func Java() *sitter.Language { return sitter.NewLanguage(ts_java.Language()) }
