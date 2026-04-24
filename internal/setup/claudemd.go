package setup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	markerStart = "<!-- sense:start -->"
	markerEnd   = "<!-- sense:end -->"
)

const senseSection = `<!-- sense:start -->
## Sense

This project has a [Sense](https://github.com/luuuc/sense) index. Sense gives you structural understanding of the codebase — symbols, relationships, patterns — without reading dozens of files.

### Use Sense tools instead of grep/glob for structural questions

| Instead of | Use | Why |
|---|---|---|
| Grep for a function/class name | ` + "`sense_graph symbol=\"Name\"`" + ` | Returns callers, callees, inheritance — not just string matches |
| Glob to find related files | ` + "`sense_search query=\"description\"`" + ` | Semantic search finds conceptually related code, not just filename patterns |
| Reading files to trace dependencies | ` + "`sense_blast symbol=\"Name\"`" + ` | Shows the full blast radius — what breaks if you change it |
| Exploring code for patterns | ` + "`sense_conventions`" + ` | Detected patterns: naming, structure, inheritance, testing |
| Checking index health | ` + "`sense_status`" + ` | Symbol count, edge count, languages, freshness |

### When NOT to use Sense

- Exact text/string search (regex, log messages, string literals) → use grep
- Reading file contents → use Read
- Editing code → Sense is read-only

### Before writing code

1. ` + "`sense_status`" + ` — confirm index is healthy
2. ` + "`sense_conventions`" + ` — check patterns for the domain you're working in
3. ` + "`sense_search`" + ` — look for prior art before creating new code
4. ` + "`sense_blast`" + ` — check scope of the symbols you're about to change
<!-- sense:end -->`

// writeClaudeMD creates or updates the Sense section in CLAUDE.md.
// Uses marker comments for idempotent updates.
func writeClaudeMD(root string) (bool, error) {
	path := filepath.Join(root, "CLAUDE.md")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	content := string(data)

	if strings.Contains(content, markerStart) {
		// Replace existing Sense section between markers.
		startIdx := strings.Index(content, markerStart)
		endIdx := strings.Index(content, markerEnd)
		if endIdx < 0 {
			// Missing end marker — replace from start marker to EOF.
			content = content[:startIdx] + senseSection + "\n"
		} else {
			endIdx += len(markerEnd)
			content = content[:startIdx] + senseSection + content[endIdx:]
		}
	} else if len(content) == 0 {
		content = senseSection + "\n"
	} else {
		sep := "\n\n"
		if strings.HasSuffix(content, "\n\n") {
			sep = ""
		} else if strings.HasSuffix(content, "\n") {
			sep = "\n"
		}
		content = content + sep + senseSection + "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
