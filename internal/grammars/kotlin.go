package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
)

func Kotlin() *sitter.Language { return sitter.NewLanguage(ts_kotlin.Language()) }
