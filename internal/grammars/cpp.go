package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_cpp "github.com/tree-sitter/tree-sitter-cpp/bindings/go"
)

func Cpp() *sitter.Language { return sitter.NewLanguage(ts_cpp.Language()) }
