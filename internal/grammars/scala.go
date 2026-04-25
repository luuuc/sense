package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_scala "github.com/tree-sitter/tree-sitter-scala/bindings/go"
)

func Scala() *sitter.Language { return sitter.NewLanguage(ts_scala.Language()) }
