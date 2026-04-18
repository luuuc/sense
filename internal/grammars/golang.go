package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

// Go returns the tree-sitter Language for Go source. The file is named
// golang.go because `package go` is illegal (go is a keyword), but the
// exported function is just Go to stay idiomatic for callers.
func Go() *sitter.Language { return sitter.NewLanguage(ts_go.Language()) }
