package rust

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// docstringFor returns the rustdoc-style doc comment attached to the
// given item node, or "" if none is attached. Outer doc comments
// (`///` and `/** … */`) attach; `attribute_item` nodes in between
// are skipped per rustdoc convention; a blank line between any
// adjacent pair detaches.
func docstringFor(node *sitter.Node, source []byte) string {
	var blocks []string
	cur := node
	for {
		prev := cur.PrevNamedSibling()
		if prev == nil {
			break
		}
		if prev.Kind() == "attribute_item" {
			if hasBlankLineGap(source, prev.EndByte(), cur.StartByte()) {
				break
			}
			cur = prev
			continue
		}
		if prev.Kind() != "line_comment" && prev.Kind() != "block_comment" {
			break
		}
		if !isOuterDocComment(prev) {
			break
		}
		if hasBlankLineGap(source, prev.EndByte(), cur.StartByte()) {
			break
		}
		blocks = append([]string{extractDocBody(prev, source)}, blocks...)
		cur = prev
	}
	if len(blocks) == 0 {
		return ""
	}
	out := strings.TrimRight(strings.Join(blocks, "\n"), "\n")
	if out == "" || !utf8.ValidString(out) {
		return ""
	}
	// extractDocBody strips leading blank lines per block, so the join's
	// first line is the first non-blank line of the run.
	firstLine := out
	if idx := strings.IndexByte(out, '\n'); idx >= 0 {
		firstLine = out[:idx]
	}
	if isLicenseHeader(strings.TrimSpace(firstLine)) {
		return ""
	}
	return out
}

// isOuterDocComment reports whether a `line_comment` or `block_comment`
// node is an outer doc comment (`///` or `/** … */`). The grammar
// signals this with an `outer_doc_comment_marker` first child; regular
// `//` and `/* */` comments have no such child.
func isOuterDocComment(n *sitter.Node) bool {
	if n.NamedChildCount() == 0 {
		return false
	}
	return n.NamedChild(0).Kind() == "outer_doc_comment_marker"
}

// extractDocBody pulls the documentation text out of a doc-comment
// node. The grammar's `doc_comment` child holds the body with outer
// markers already stripped; this helper drops the per-line `*`
// continuation prefix and surrounding blank lines.
func extractDocBody(n *sitter.Node, source []byte) string {
	var body string
	for i := uint(0); i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c != nil && c.Kind() == "doc_comment" {
			body = extract.Text(c, source)
			break
		}
	}
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
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
	return strings.Join(out, "\n")
}

// hasBlankLineGap reports whether the byte range [start, end) in source
// contains a blank line. Unlike the Go/TS extractors, the Rust grammar
// includes the trailing `\n` inside `line_comment` ranges — so this
// helper compensates: if `start` points to a byte just past a `\n`, we
// roll start back one so the comment's terminator is counted as part
// of the inter-node gap. With that adjustment a blank line is always
// `\n\n` regardless of whether the previous neighbour was a line- or
// block-comment.
//
// Gap rule: only `\n` counts — non-newline bytes are transparent. Safe
// under tree-sitter's invariant that inter-named-sibling gaps contain
// only whitespace. Caller guarantees start <= end <= len(source); the
// callers in docstringFor pass prev.EndByte() and cur.StartByte() of
// adjacent named siblings, which satisfies that by tree ordering.
func hasBlankLineGap(source []byte, start, end uint) bool {
	if start > 0 && source[start-1] == '\n' {
		start--
	}
	nl := 0
	for _, b := range source[start:end] {
		if b == '\n' {
			nl++
			if nl >= 2 {
				return true
			}
		}
	}
	return false
}

// isLicenseHeader recognises file-top headers that must not be
// promoted into a symbol's docstring.
func isLicenseHeader(line string) bool {
	return strings.HasPrefix(line, "Copyright") ||
		strings.HasPrefix(line, "SPDX-")
}
