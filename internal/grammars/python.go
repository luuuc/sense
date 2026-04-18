package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// Python returns the tree-sitter Language for Python source.
func Python() *sitter.Language { return sitter.NewLanguage(ts_python.Language()) }
