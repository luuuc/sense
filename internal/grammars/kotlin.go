package grammars

import (
	ts_kotlin "github.com/fwcd/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
)

func Kotlin() *sitter.Language { return sitter.NewLanguage(ts_kotlin.Language()) }
