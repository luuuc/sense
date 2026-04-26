package setup

import "path/filepath"

const agentsMD = `<!-- sense:start -->
## Sense — codebase understanding

This project has a Sense index. Sense gives you structural understanding of the codebase — symbols, relationships, patterns — without reading dozens of files.

**Use Sense MCP tools for ALL codebase understanding:**

| Question | Tool |
|---|---|
| Who calls X? What does X call? | sense_graph |
| Find code related to a concept | sense_search |
| What breaks if I change X? | sense_blast |
| What patterns does this project follow? | sense_conventions |
| Index health, what's indexed | sense_status |

**When NOT to use Sense** (use grep instead):
- Exact text/string search (regex, log messages, string literals)
- Reading file contents → use your file reading tool
- Editing code → Sense is read-only
<!-- sense:end -->`

// writeAgentsMD creates or updates the Sense section in AGENTS.md.
// Uses the same marker-comment strategy as CLAUDE.md.
func writeAgentsMD(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, "AGENTS.md"), agentsMD)
}
