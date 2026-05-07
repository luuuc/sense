package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const opencodeAgentsMD = `<!-- sense:start -->
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

func configureOpencode(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolOpencode}

	if wrote, err := writeOpencodeJSON(root); err != nil {
		return nil, fmt.Errorf("write opencode.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, "opencode.json")
	}

	if wrote, err := writeOpencodeAgentsMD(root); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, "AGENTS.md")
	}

	n, err := writeOpencodeSkills(root)
	if err != nil {
		return nil, fmt.Errorf("write .opencode/skills: %w", err)
	}
	if n > 0 {
		tr.Files = append(tr.Files, fmt.Sprintf("%d skill files in .opencode/skills/", n))
	}

	return tr, nil
}

// writeOpencodeJSON creates or merges the Sense MCP server entry into
// opencode.json (project-local OpenCode config).
func writeOpencodeJSON(root string) (bool, error) {
	path := filepath.Join(root, "opencode.json")

	existing, err := readJSONFile(path)
	if err != nil {
		return false, err
	}

	mcpServers, _ := existing["mcp"].(map[string]any)
	if mcpServers == nil {
		mcpServers = map[string]any{}
	}

	// Only add if not already present
	if _, ok := mcpServers["sense"]; !ok {
		mcpServers["sense"] = map[string]any{
			"type":    "local",
			"command": []any{"sense", "mcp"},
			"enabled": true,
		}
	}

	existing["mcp"] = mcpServers

	if err := writeJSONFile(path, existing); err != nil {
		return false, err
	}
	return true, nil
}

// writeOpencodeAgentsMD creates or updates the Sense section in AGENTS.md.
func writeOpencodeAgentsMD(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, "AGENTS.md"), opencodeAgentsMD)
}

// writeOpencodeSkills creates skill files in .opencode/skills/.
// Existing files are overwritten to pick up template changes on --init.
func writeOpencodeSkills(root string) (int, error) {
	dir := filepath.Join(root, ".opencode", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	written := 0
	for _, s := range skills {
		// Write to .opencode/skills/<name>/SKILL.md format
		skillDir := filepath.Join(dir, strings.TrimSuffix(s.filename, ".md"))
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			return written, err
		}
		path := filepath.Join(skillDir, "SKILL.md")
		if err := os.WriteFile(path, []byte(s.content), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
