package scan

import "bytes"

// The Go project's generated-file marker (golang/go#41196): a line-comment
// line of the form `// Code generated <by tool> DO NOT EDIT.`, matched the
// way go/ast.IsGenerated matches it (prefix + suffix + length guard, no
// regexp). It covers protoc, stringer, mockgen, wire, and every conforming
// generator, so no per-generator globs are needed. The marker is Go-spec
// first; other ecosystems' markers are added only when their vertical
// demands them.
var (
	generatedPrefix = []byte("// Code generated ")
	generatedSuffix = []byte(" DO NOT EDIT.")
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// isGeneratedMarker reports whether one whitespace-trimmed line is the
// #41196 marker. The length guard keeps the prefix and suffix from
// overlapping on short lines.
func isGeneratedMarker(line []byte) bool {
	return len(line) >= len(generatedPrefix)+len(generatedSuffix) &&
		bytes.HasPrefix(line, generatedPrefix) &&
		bytes.HasSuffix(line, generatedSuffix)
}

// isGeneratedSource reports whether src carries the generated-file marker
// before its first non-comment, non-blank line. The scan is line-based and
// stops at the first line of real code, so normal files exit within a few
// lines while arbitrarily long license headers and build-tag blocks before
// the marker are still traversed (a fixed byte budget would miss those).
// CRLF endings and a leading UTF-8 BOM are tolerated, and lines are
// whitespace-trimmed before matching — siding with go/ast (an indented
// marker counts) over the anchored-regex reading of the spec. It is
// stricter than go/ast in one direction: markers inside or straddling
// /* */ block comments are not honored (conservative, fails toward
// indexing). Generators that put the marker elsewhere (pre-#41196) slip
// through; accepted and recorded in the 30-05 pitch.
func isGeneratedSource(src []byte) bool {
	src = bytes.TrimPrefix(src, utf8BOM)
	inBlock := false
	for len(src) > 0 {
		var line []byte
		if i := bytes.IndexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			line, src = src, nil
		}
		generated, done := classifyHeaderLine(bytes.TrimSpace(line), &inBlock)
		if done {
			return generated
		}
	}
	return false
}

// classifyHeaderLine inspects one whitespace-trimmed header line and reports
// (generated, done). done is true when the verdict is final: the marker was
// found, or real code was reached. inBlock carries /* ... */ state across
// lines; anything after a block close on the same line is conservatively
// treated as code (fails toward indexing).
func classifyHeaderLine(line []byte, inBlock *bool) (bool, bool) {
	if *inBlock {
		end := bytes.Index(line, []byte("*/"))
		if end < 0 {
			return false, false
		}
		*inBlock = false
		return false, len(bytes.TrimSpace(line[end+2:])) > 0
	}
	switch {
	case len(line) == 0:
		return false, false
	case bytes.HasPrefix(line, []byte("//")):
		matched := isGeneratedMarker(line)
		return matched, matched
	case bytes.HasPrefix(line, []byte("/*")):
		*inBlock = true
		return classifyHeaderLine(line[2:], inBlock)
	default:
		return false, true
	}
}
