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

	if wrote, err := writeOpencodePlugin(root); err != nil {
		return nil, fmt.Errorf("write .opencode/plugin: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".opencode/plugin/sense.js")
	}

	return tr, nil
}

// opencodePluginJS is the Sense adoption plugin OpenCode loads from
// .opencode/plugin/. It is the parallel to Sense's Claude Code PreToolUse hook:
// OpenCode has no pre-tool *config* hook (config hooks fire only on file_edited /
// session_completed), so the steer-toward-Sense behavior has to be a plugin. It
// intercepts grep/glob (redirecting to the Sense MCP tools) and nudges once after
// heavy file-reading, which a passive AGENTS.md alone does not achieve for many
// models. Plain JS, no dependencies.
const opencodePluginJS = `// Sense adoption plugin for OpenCode (written by ` + "`sense setup --tools opencode`" + `).
// Steers the model toward the Sense MCP tools, the parallel to Sense's Claude
// Code PreToolUse hook. OpenCode loads any module under .opencode/plugin/.
export const sense = async () => {
  let reads = 0
  return {
    "tool.execute.before": async (input) => {
      const tool = input && input.tool
      if (tool === "grep" || tool === "glob") {
        throw new Error(
          "Use the Sense MCP tools for symbol or structural lookups instead of " +
          "grep/glob: sense_search (find by meaning), sense_graph (callers and " +
          "callees), sense_blast (change impact). Use grep only for literal text " +
          "you cannot express structurally."
        )
      }
      if (tool === "read") {
        reads++
        // Periodic nudge (every 6th read), not once: read-happy models resume
        // reading after a single interrupt. Name the task-right tool so the
        // model reaches for sense_graph, not the useless sense_status.
        if (reads % 6 === 0) {
          throw new Error(
            "You have read " + reads + " files. Stop reading file by file and " +
            "navigate structurally with Sense: sense_graph for who-calls-what " +
            "(callers and callees), sense_search to find code by meaning, " +
            "sense_conventions for the project's patterns. Read a file only after " +
            "Sense points you to it."
          )
        }
      }
    },
  }
}
`

// writeOpencodePlugin writes the Sense adoption plugin to .opencode/plugin/.
// Overwritten on re-run to pick up template changes, matching writeOpencodeSkills.
func writeOpencodePlugin(root string) (bool, error) {
	dir := filepath.Join(root, ".opencode", "plugin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "sense.js")
	if err := os.WriteFile(path, []byte(opencodePluginJS), 0o644); err != nil {
		return false, err
	}
	return true, nil
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
