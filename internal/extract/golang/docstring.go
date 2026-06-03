package golang

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// docstringFor returns the godoc-style doc comment attached to the
// given declaration node, or "" if none is attached. Attachment walks
// previous-sibling `comment` nodes as long as no blank line separates
// them; license headers and invalid UTF-8 are filtered.
func docstringFor(node *sitter.Node, source []byte) string {
	var comments []*sitter.Node
	cur := node
	for {
		prev := cur.PrevNamedSibling()
		if prev == nil || prev.Kind() != "comment" {
			break
		}
		// Gap rule: a blank line between this comment and its forward
		// neighbour detaches it. The loop counts `\n` and ignores
		// everything else — non-newline bytes are transparent.
		//
		// Safe under tree-sitter's invariant that inter-named-sibling
		// gaps contain only whitespace (the tokenizer places non-
		// whitespace bytes inside named or anonymous nodes, never in
		// the gap). If a future grammar bump ever violates that, this
		// loop detaches more aggressively than the previous reset-on-
		// non-whitespace form did: a stray byte between two `\n` would
		// still trigger detachment. That's the trade for the simpler
		// shape; document if it ever bites.
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
	return formatGoComments(comments, source)
}

// formatGoComments joins comment nodes into a single docstring, stripping
// markers and applying filters. Returns "" when filters fire or the
// joined text is not valid UTF-8.
func formatGoComments(nodes []*sitter.Node, source []byte) string {
	var lines []string
	for _, n := range nodes {
		lines = append(lines, stripCommentMarkers(extract.Text(n, source))...)
	}
	// Find the first non-empty line for the license-header filter.
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

// stripCommentMarkers turns one comment node's raw text (which still
// has `//` or `/* */` markers) into zero or more body lines with
// markers removed. The leading space godoc convention writes (`// foo`)
// is trimmed; a block-continuation ` *` prefix is also trimmed.
func stripCommentMarkers(text string) []string {
	text = strings.TrimRight(text, "\n")
	if strings.HasPrefix(text, "//") {
		body := strings.TrimPrefix(text, "//")
		body = strings.TrimPrefix(body, " ")
		return []string{body}
	}
	// Only other shape tree-sitter-go emits under `comment` is `/* */`.
	body := strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
	var out []string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimPrefix(line, "*")
		line = strings.TrimPrefix(line, " ")
		out = append(out, line)
	}
	// Strip purely-empty leading and trailing lines that the
	// `/*\n…\n*/` shape introduces — but preserve blank lines
	// in the middle of a multi-paragraph block.
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// isLicenseHeader recognises the conventional headers that should never
// be promoted into a symbol's docstring. The list is intentionally tight
// — false positives here would silently drop real godoc — so only the
// two prefixes that are unambiguous in Go source qualify. The pitch's
// general guidance also mentions a `<` (HTML/XML preamble) prefix; it
// is omitted here because godoc legitimately uses `<nil>`, `<see X>`,
// and similar bracket-led phrases, and HTML preambles do not appear in
// Go source.
func isLicenseHeader(line string) bool {
	return strings.HasPrefix(line, "Copyright") ||
		strings.HasPrefix(line, "SPDX-")
}
