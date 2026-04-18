package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_ruby "github.com/tree-sitter/tree-sitter-ruby/bindings/go"
)

// Ruby returns the tree-sitter Language for Ruby source.
func Ruby() *sitter.Language { return sitter.NewLanguage(ts_ruby.Language()) }
