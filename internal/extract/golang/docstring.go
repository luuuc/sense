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
	if node == nil {
		return ""
	}
	var comments []*sitter.Node
	cur := node
	for {
		prev := cur.PrevNamedSibling()
		if prev == nil || prev.Kind() != "comment" {
			break
		}
		// Gap rule: a blank line between this comment and its forward
		// neighbour detaches it. A blank line is two `\n` with only
		// horizontal whitespace between them.
		gap := source[prev.EndByte():cur.StartByte()]
		nl := 0
		blank := false
		for _, b := range gap {
			switch b {
			case '\n':
				nl++
				if nl >= 2 {
					blank = true
				}
			case ' ', '\t', '\r':
				// whitespace doesn't reset
			default:
				nl = 0
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
	if strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") {
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
	return nil
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

