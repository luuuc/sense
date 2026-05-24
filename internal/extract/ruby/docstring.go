package ruby

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// docstringFor returns the RDoc-style doc comment attached to the given
// declaration node, or "" if none is attached. Attachment walks
// previous-sibling `comment` nodes (stacked `# ` lines or a single
// `=begin/=end` block) as long as no blank line separates them.
// Magic comments (frozen_string_literal, encoding, etc.), license
// headers, and invalid UTF-8 are filtered to "".
func docstringFor(node *sitter.Node, source []byte) string {
	cur := node
	// tree-sitter-ruby wraps a class/module body in a `body_statement`
	// node. When the target is the first (or only) statement inside that
	// wrapper, its preceding comment lives one level up — as a sibling of
	// the body_statement, not as a sibling of the target. Walk past the
	// wrapper so the sibling lookup below finds the comment.
	for cur.Parent() != nil && cur.Parent().Kind() == "body_statement" && cur.PrevNamedSibling() == nil {
		cur = cur.Parent()
	}
	var comments []*sitter.Node
	for {
		prev := cur.PrevNamedSibling()
		if prev == nil || prev.Kind() != "comment" {
			break
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
	return formatRubyComments(comments, source)
}

// formatRubyComments joins comment nodes into a single docstring,
// stripping markers and applying the magic-comment / license filters.
func formatRubyComments(nodes []*sitter.Node, source []byte) string {
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
	if isMagicComment(first) || isLicenseHeader(first) {
		return ""
	}
	out := strings.Join(lines, "\n")
	if !utf8.ValidString(out) {
		return ""
	}
	return strings.TrimRight(out, "\n")
}

// stripCommentMarkers turns one comment node's raw text into body
// lines with markers removed. Line comments (`# foo`) strip the `#`
// and one optional space; `=begin/=end` blocks take the body between
// the delimiters, one line per source line. tree-sitter-ruby emits
// only these two forms under the `comment` node kind, so the second
// branch handles everything that isn't `#`-led.
func stripCommentMarkers(text string) []string {
	text = strings.TrimRight(text, "\n")
	if strings.HasPrefix(text, "#") {
		body := strings.TrimPrefix(text, "#")
		body = strings.TrimPrefix(body, " ")
		return []string{body}
	}
	body := strings.TrimPrefix(text, "=begin")
	body = strings.TrimSuffix(body, "=end")
	body = strings.TrimSuffix(body, "\n")
	out := strings.Split(body, "\n")
	for len(out) > 0 && strings.TrimSpace(out[0]) == "" {
		out = out[1:]
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
}

// magicCommentPrefixes lists the file-top Ruby directives that look
// like comments but carry no documentation value. Kept tight on
// purpose: each prefix here risks dropping a real RDoc line whose first
// word happens to match, so only the ones that are unambiguous in
// practice belong. Tool-specific magic comments (Sorbet `# typed:`,
// RuboCop `# rubocop:`) are out of scope for this extractor.
var magicCommentPrefixes = []string{
	"frozen_string_literal:",
	"encoding:",
	"coding:",
}

func isMagicComment(line string) bool {
	for _, p := range magicCommentPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return false
}

// isLicenseHeader recognises the conventional file-top headers that
// must not be promoted into a symbol's docstring. The list is kept
// tight for the same reason as in the Go extractor — false positives
// here silently drop real RDoc.
func isLicenseHeader(line string) bool {
	return strings.HasPrefix(line, "Copyright") ||
		strings.HasPrefix(line, "SPDX-")
}
