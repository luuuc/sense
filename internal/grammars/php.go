package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_php "github.com/tree-sitter/tree-sitter-php/bindings/go"
)

func PHP() *sitter.Language { return sitter.NewLanguage(ts_php.LanguagePHP()) }
