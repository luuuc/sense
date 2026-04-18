package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_rust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
)

// Rust returns the tree-sitter Language for Rust source.
func Rust() *sitter.Language { return sitter.NewLanguage(ts_rust.Language()) }
