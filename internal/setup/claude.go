package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/luuuc/sense/internal/mcpio"
)

// This file is the complete Claude Code integration: how Sense detects it
// (detectClaudeCode) and what files it writes (configureClaudeCode plus its
// writers). Adding a new tool means adding a sibling file shaped like this one
// and a single line in registry().

// detectClaudeCode looks for evidence that Claude Code is installed.
func detectClaudeCode() DetectResult {
	r := DetectResult{Tool: ToolClaudeCode}
	if os.Getenv("CLAUDE_CODE") != "" {
		r.Found = true
		r.Evidence = "CLAUDE_CODE env var set"
		return r
	}
	if _, err := exec.LookPath("claude"); err == nil {
		r.Found = true
		r.Evidence = "claude on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".claude")); err == nil {
			r.Found = true
			r.Evidence = "~/.claude/ directory"
			return r
		}
	}
	return r
}

// configureClaudeCode writes every Claude Code integration file: the MCP server
// entry, hook + permission settings, the CLAUDE.md guidance section, and the
// skill and agent files.
func configureClaudeCode(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolClaudeCode}

	if wrote, err := writeMCPJSON(root); err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".mcp.json")
	}

	if wrote, err := writeClaudeSettings(root); err != nil {
		return nil, fmt.Errorf("write .claude/settings.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".claude/settings.json")
	}

	if wrote, err := writeClaudeMD(root); err != nil {
		return nil, fmt.Errorf("write CLAUDE.md: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, "CLAUDE.md")
	}

	n, err := writeSkills(root)
	if err != nil {
		return nil, fmt.Errorf("write .claude/skills: %w", err)
	}
	if n > 0 {
		tr.Files = append(tr.Files, fmt.Sprintf("%d skill files in .claude/skills/", n))
	}

	na, err := writeAgents(root)
	if err != nil {
		return nil, fmt.Errorf("write .claude/agents: %w", err)
	}
	if na > 0 {
		tr.Files = append(tr.Files, fmt.Sprintf("%d agent files in .claude/agents/", na))
	}

	return tr, nil
}

// writeClaudeMD creates or updates the Sense guidance section in CLAUDE.md.
func writeClaudeMD(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, "CLAUDE.md"), guidanceMarkdown)
}

// writeMCPJSON creates or merges the Sense MCP server entry into .mcp.json.
// Shared with Codex CLI, which reads the same project-local file.
func writeMCPJSON(root string) (bool, error) {
	path := filepath.Join(root, ".mcp.json")

	senseCfg := map[string]any{
		"command":            "sense",
		"args":               []any{"mcp"},
		"serverInstructions": mcpio.ServerInstructions,
		// Pre-load Sense's tools into the initial tool set instead of letting
		// Claude Code defer them behind ToolSearch. Deferred MCP tools are
		// only callable after the model first invokes ToolSearch to load their
		// schemas; in practice the model skips that hop whenever an obvious
		// grep/ls target exists, so the structural tools (sense_graph,
		// sense_blast) never ran. alwaysLoad makes them callable from turn 1,
		// so reaching for Sense is the path of least resistance, not an extra
		// step. Claude Code reads this per-server field (v2.1.121+); other MCP
		// clients ignore the unknown key.
		"alwaysLoad": true,
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

// writeClaudeSettings creates or merges hook config and permissions
// into .claude/settings.json.
func writeClaudeSettings(root string) (bool, error) {
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "settings.json")

	existing, err := readJSONFile(path)
	if err != nil {
		return false, err
	}

	hooks := map[string]any{
		"PreToolUse": []any{
			map[string]any{
				"matcher": "Grep|Glob|Agent|Bash",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "sense hook pre-tool-use",
						"timeout": 5000,
					},
				},
			},
		},
		"PreCompact": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "sense hook pre-compact",
						"timeout": 5000,
					},
				},
			},
		},
		"SubagentStart": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "sense hook subagent-start",
						"timeout": 5000,
					},
				},
			},
		},
		"SessionStart": []any{
			map[string]any{
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "sense hook session-start",
						"timeout": 5000,
					},
				},
			},
		},
	}

	mergeHooks(existing, hooks)
	// Retire the synchronous PostToolUse re-index hook (pitch 26-01): the
	// embedded watcher in `sense mcp` plus per-query read-repair now keep
	// the index fresh off the agent's critical path. Strip any Sense entry
	// an earlier setup wrote so re-running setup migrates old configs.
	removeRetiredHook(existing, "PostToolUse")
	mergePermissions(existing, []string{"mcp__sense__*"})

	if err := writeJSONFile(path, existing); err != nil {
		return false, err
	}
	return true, nil
}
