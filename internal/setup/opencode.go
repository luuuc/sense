package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// detectOpencode looks for evidence that OpenCode is installed.
func detectOpencode() DetectResult {
	r := DetectResult{Tool: ToolOpencode}
	if os.Getenv("OPENCODE") != "" {
		r.Found = true
		r.Evidence = "OPENCODE env var set"
		return r
	}
	if _, err := exec.LookPath("opencode"); err == nil {
		r.Found = true
		r.Evidence = "opencode on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); err == nil {
			r.Found = true
			r.Evidence = "~/.config/opencode/ directory"
			return r
		}
	}
	return r
}

// configureOpencode writes OpenCode's project-local MCP config, the AGENTS.md
// guidance, and the per-skill SKILL.md tree OpenCode expects.
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
	return writeMarkerFile(filepath.Join(root, "AGENTS.md"), guidanceMarkdown)
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
