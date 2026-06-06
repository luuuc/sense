package setup

import (
	"errors"
	"io/fs"
	"os"
	"strings"
)

const (
	markerStart = "<!-- sense:start -->"
	markerEnd   = "<!-- sense:end -->"
)

// guidanceMarkdown is the single source of truth for the Sense routing guidance
// every tool's instructions file receives (CLAUDE.md, .cursorrules, AGENTS.md).
// One source keeps the message consistent across tools; to tune what every AI
// tool is told, edit this block. It is tool-agnostic on purpose: a phrasing
// that only makes sense for one tool does not belong here.
//
// The one guidance surface NOT defined here is the MCP `serverInstructions`
// string sent to a client at connect time. That lives in mcpio.ServerInstructions
// because the running `sense mcp` server also sends it; setup only forwards it
// (see writeMCPJSON). Keep the two in spirit, not in lockstep: this block is
// the in-repo prompt, ServerInstructions is the protocol-level one-liner.
const guidanceMarkdown = `<!-- sense:start -->
## Use the Sense index for codebase understanding

Sense gives you structural understanding of the codebase (symbols, relationships, patterns) without reading dozens of files. Prefer it over grep, glob, and file-walking for any structural or semantic question.

| Question | Tool |
|---|---|
| Who calls X? What does X call? | sense_graph |
| Find code related to a concept | sense_search |
| What breaks if I change X? | sense_blast |
| What patterns does this project follow? | sense_conventions |
| Index health, what's indexed | sense_status |

**You MUST NOT** use grep/glob for symbol lookup, or skip Sense because its tools load on demand. For list outputs (dead code, blast radius, callers), spot-check a sample with grep before relying on them.

**When NOT to use Sense** (use grep instead): exact text/string search, reading file contents, editing code (Sense is read-only).
<!-- sense:end -->`

// writeMarkerFile creates or updates a marker-delimited Sense section
// in the file at path. If the file already contains markers, the section
// between them is replaced. Otherwise the section is appended.
func writeMarkerFile(path, section string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	content := string(data)

	switch {
	case strings.Contains(content, markerStart):
		startIdx := strings.Index(content, markerStart)
		endIdx := strings.Index(content, markerEnd)
		if endIdx < 0 {
			content = content[:startIdx] + section + "\n"
		} else {
			endIdx += len(markerEnd)
			content = content[:startIdx] + section + content[endIdx:]
		}
	case len(content) == 0:
		content = section + "\n"
	default:
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
