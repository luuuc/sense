package tsjs

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// docstringFor returns the JSDoc comment attached to the given
// declaration node, or "" if none is attached. Only `/** … */` blocks
// count as JSDoc; plain `//` and plain `/* … */` break the chain.
// Transparent wrappers (see isJSDocWrapper) are walked through when
// the target is the first child, so JSDoc above a wrapper reaches the
// inner declaration.
func docstringFor(node *sitter.Node, source []byte) string {
	cur := node
	for cur.Parent() != nil && isJSDocWrapper(cur.Parent().Kind()) && cur.PrevNamedSibling() == nil {
		cur = cur.Parent()
	}
	var comments []*sitter.Node
	for {
		prev := cur.PrevNamedSibling()
		if prev == nil || prev.Kind() != "comment" {
			break
		}
		text := extract.Text(prev, source)
		if !isJSDocText(text) {
			break // a `//` or plain `/*` comment ends the JSDoc chain
		}
		// Gap rule: see golang/docstring.go for the contract — only `\n`
		// matters; non-newline bytes are transparent. Safe under tree-
		// sitter's whitespace-only-between-siblings invariant.
		gap := source[prev.EndByte():cur.StartByte()]
		nl := 0
		blank := false
		for _, b := range gap {
			if b == '\n' {
				nl++
				if nl >= 2 {
					blank = true
				}
			}
		}
		if blank {
			break
		}
		comments = append([]*sitter.Node{prev}, comments...)
		cur = prev
	}
	if len(comments) == 0 {
		return ""
	}
	return formatJSDocComments(comments, source)
}

// isJSDocWrapper reports whether a node kind is a transparent wrapper
// that JSDoc may sit above rather than next to the inner declaration.
func isJSDocWrapper(kind string) bool {
	switch kind {
	case "lexical_declaration", "variable_declaration", "export_statement":
		return true
	}
	return false
}

// isJSDocText reports whether a comment node's raw text is a JSDoc
// block (`/** … */`). Plain `//` and `/* … */` (single-star) are not
// documentation per JSDoc convention.
func isJSDocText(text string) bool {
	return strings.HasPrefix(text, "/**") && strings.HasSuffix(text, "*/")
}

// formatJSDocComments joins JSDoc blocks into a single docstring,
// stripping `/**`, `*/`, and the conventional ` * ` continuation
// prefix from each line. License and invalid-UTF-8 filters apply.
func formatJSDocComments(nodes []*sitter.Node, source []byte) string {
	var lines []string
	for _, n := range nodes {
		lines = append(lines, stripCommentMarkers(extract.Text(n, source))...)
	}
	firstIdx := -1
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			firstIdx = i
			break
		}
	}
	if firstIdx < 0 {
		return ""
	}
	first := strings.TrimSpace(lines[firstIdx])
	if isLicenseHeader(first) {
		return ""
	}
	out := strings.Join(lines, "\n")
	if !utf8.ValidString(out) {
		return ""
	}
	return strings.TrimRight(out, "\n")
}

// stripCommentMarkers turns one JSDoc block's raw text into body lines:
// strips the opening `/**` and closing `*/`, then strips the leading
// ` * ` continuation prefix from each interior line and one optional
// leading space from the first/last line. Leading and trailing empty
// lines (the convention `/**\n * body\n */` introduces) are dropped;
// internal blank lines are preserved.
func stripCommentMarkers(text string) []string {
	body := strings.TrimPrefix(text, "/**")
	body = strings.TrimSuffix(body, "*/")
	raw := strings.Split(body, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		trimmed = strings.TrimPrefix(trimmed, "*")
		trimmed = strings.TrimPrefix(trimmed, " ")
		out = append(out, trimmed)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// isLicenseHeader recognises the conventional file-top headers that
// must not be promoted into a symbol's docstring.
func isLicenseHeader(line string) bool {
	return strings.HasPrefix(line, "Copyright") ||
		strings.HasPrefix(line, "SPDX-")
}
