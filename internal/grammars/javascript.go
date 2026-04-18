package grammars

import (
	sitter "github.com/tree-sitter/go-tree-sitter"
	ts_js "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
)

// JavaScript returns the tree-sitter Language for JavaScript (.js/.mjs/.cjs/.jsx).
// The JSX variant is parsed by the same grammar — tree-sitter-javascript
// handles JSX without a separate language.
func JavaScript() *sitter.Language { return sitter.NewLanguage(ts_js.Language()) }
