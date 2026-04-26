package setup

import (
	"os"
	"path/filepath"
)

const cursorrules = `<!-- sense:start -->
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

// writeCursorMCPJSON creates or merges the Sense MCP server entry into
// .cursor/mcp.json (Cursor's MCP config location).
func writeCursorMCPJSON(root string) (bool, error) {
	dir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "mcp.json")

	senseCfg := map[string]any{
		"command": "sense",
		"args":    []any{"mcp"},
	}

	existing, err := readJSONFile(path)
	if err != nil {
		return false, err
	}

	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["sense"] = senseCfg
	existing["mcpServers"] = servers

	if err := writeJSONFile(path, existing); err != nil {
		return false, err
	}
	return true, nil
}

// writeCursorRules creates or updates the Sense section in .cursorrules.
// Uses the same marker-comment strategy as CLAUDE.md for idempotent merges.
func writeCursorRules(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, ".cursorrules"), cursorrules)
}
