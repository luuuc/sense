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
## IMPORTANT: This project has a Sense index — you MUST use it

Sense gives you structural understanding of the codebase — symbols, relationships, patterns — without reading dozens of files.

**BEFORE any code exploration, symbol lookup, or agent spawning:**

1. Load Sense tools: ` + "`ToolSearch(\"select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status\")`" + `
2. Call ` + "`sense_status`" + ` to confirm index health
3. Use ` + "`sense_graph`" + `, ` + "`sense_search`" + `, ` + "`sense_blast`" + ` for ALL structural questions

**You MUST NOT:**

- Spawn deep-explore or Explore agents for structural questions — Sense answers them directly
- Use grep/glob to find symbol callers, implementations, or dependencies
- Skip Sense because tools are deferred — load them with ToolSearch first

**When to use each tool:**

| Question | Tool |
|---|---|
| Who calls X? What does X call? | ` + "`sense_graph symbol=\"X\"`" + ` |
| Find code related to a concept | ` + "`sense_search query=\"description\"`" + ` |
| What breaks if I change X? | ` + "`sense_blast symbol=\"X\"`" + ` |
| What patterns does this project follow? | ` + "`sense_conventions`" + ` |

**When NOT to use Sense** (use grep instead):

- Exact text/string search (regex, log messages, string literals)
- Reading file contents → use Read
- Editing code → Sense is read-only
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
