package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_ts "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// TypeScript returns the tree-sitter Language for .ts files. The upstream
// module ships two distinct parsers — one that accepts JSX (TSX) and one
// that doesn't. Use TSX for .tsx files.
func TypeScript() *sitter.Language { return sitter.NewLanguage(ts_ts.LanguageTypescript()) }

// TSX returns the tree-sitter Language for .tsx files.
func TSX() *sitter.Language { return sitter.NewLanguage(ts_ts.LanguageTSX()) }
