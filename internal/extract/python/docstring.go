package python

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// docstringFor returns the PEP 257 docstring attached to the given
// function or class node, or "" if the body's first statement is not a
// string literal. Reading the body's first statement (not a preceding
// comment) is what distinguishes Python documentation from C-family
// conventions and is the reason decorators don't interfere — the body
// is reached via the `body` field on the definition node, never by
// walking siblings backward.
//
// License headers and invalid UTF-8 are filtered to "".
func docstringFor(node *sitter.Node, source []byte) string {
	// Walk: definition node → body block → first statement → string.
	// Each step either yields the next or short-circuits to "". The
	// helper is bounded to the tree shapes tree-sitter-python emits;
	// non-string first statements (pass, assignment, return) bail at
	// the kind check below, which is the most common "no docstring"
	// path in practice.
	//
	// ChildByFieldName is a grammar contract, not a caller contract:
	// tree-sitter-python today always emits the `body` field on
	// function/class definitions, but grammar churn (PEP 695, future
	// "function signature" nodes) could change that. A nil body would
	// segfault on NamedChild below — guard explicitly.
	body := node.ChildByFieldName("body")
	if body == nil {
		return ""
	}
	first := body.NamedChild(0)
	if first == nil || first.Kind() != "expression_statement" {
		return ""
	}
	str := first.NamedChild(0)
	if str == nil || str.Kind() != "string" {
		return ""
	}
	// Strip leading/trailing whitespace introduced by `"""\n…\n"""`
	// formatting. Internal indentation is preserved — a
	// textwrap.dedent-equivalent pass would mutate bytes a Python
	// author wrote, for marginal reader gain.
	out := strings.TrimSpace(stringContent(str, source))
	if out == "" || !utf8.ValidString(out) {
		return ""
	}
	// License-header filter on the first line. TrimSpace above
	// guarantees the first byte is non-whitespace, so the first line
	// of the split is always non-blank.
	firstLine := out
	if idx := strings.IndexByte(out, '\n'); idx >= 0 {
		firstLine = out[:idx]
	}
	if isLicenseHeader(strings.TrimSpace(firstLine)) {
		return ""
	}
	return out
}

// isLicenseHeader recognises the conventional file-top headers that
// must not be promoted into a symbol's docstring. The list mirrors the
// other extractors — Copyright and SPDX- are unambiguous; broader
// prefixes risk dropping real PEP 257 prose.
func isLicenseHeader(line string) bool {
	return strings.HasPrefix(line, "Copyright") ||
		strings.HasPrefix(line, "SPDX-")
}
