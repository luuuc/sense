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

**Your FIRST action in every conversation — before responding, before any tool call — MUST be:**

1. Load Sense tools: ` + "`ToolSearch(\"select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status\")`" + `
2. Call ` + "`sense_status`" + ` to confirm index health
3. Use Sense tools for ALL codebase understanding — exploration, research, architecture, callers, dependencies, search

**You MUST NOT:**

- Spawn deep-explore, Explore, or general-purpose agents for codebase questions — Sense answers them directly
- Use grep/glob to find symbol callers, implementations, or dependencies
- Skip Sense because tools are deferred — load them with ToolSearch first
- Fall back to Read/Bash after a hook blocks another tool — the hook is telling you to use Sense

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
	return writeMarkerFile(filepath.Join(root, "CLAUDE.md"), senseSection)
}

// writeMarkerFile creates or updates a marker-delimited Sense section
// in the file at path. If the file already contains markers, the section
// between them is replaced. Otherwise the section is appended.
func writeMarkerFile(path, section string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	content := string(data)

	if strings.Contains(content, markerStart) {
		startIdx := strings.Index(content, markerStart)
		endIdx := strings.Index(content, markerEnd)
		if endIdx < 0 {
			content = content[:startIdx] + section + "\n"
		} else {
			endIdx += len(markerEnd)
			content = content[:startIdx] + section + content[endIdx:]
		}
	} else if len(content) == 0 {
		content = section + "\n"
	} else {
		sep := "\n\n"
		if strings.HasSuffix(content, "\n\n") {
			sep = ""
		} else if strings.HasSuffix(content, "\n") {
			sep = "\n"
		}
		content = content + sep + section + "\n"
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
