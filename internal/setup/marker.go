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

Sense tools are loaded at session start. Use them for ALL codebase understanding.

| Question | Tool |
|---|---|
| Who calls X? What does X call? | ` + "`sense_graph symbol=\"X\"`" + ` |
| Find code related to a concept | ` + "`sense_search query=\"description\"`" + ` |
| What breaks if I change X? | ` + "`sense_blast symbol=\"X\"`" + ` |
| What patterns does this project follow? | ` + "`sense_conventions`" + ` |

**You MUST NOT:** spawn Explore/deep-explore agents, use grep/glob for symbol lookup, or skip Sense because tools are deferred.

**Verify list results:** For list outputs (dead code, blast radius, callers), verify a sample with grep before finalizing.
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
