package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_c "github.com/tree-sitter/tree-sitter-c/bindings/go"
)

func C() *sitter.Language { return sitter.NewLanguage(ts_c.Language()) }
