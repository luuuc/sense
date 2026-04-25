// Package setup generates AI tool integration files on first scan.
// It writes .mcp.json, .claude/settings.json, CLAUDE.md, and
// .claude/skills/ so that Claude Code (and future AI tools) discover
// and prefer Sense tools without manual configuration.
//
// All writes are idempotent: JSON files are deep-merged, CLAUDE.md
// uses marker comments, skill files use existence checks. Running
// setup twice produces the same result.
package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Result summarises what setup wrote.
type Result struct {
	MCPJSON        bool // .mcp.json created or updated
	ClaudeSettings bool // .claude/settings.json created or updated
	ClaudeMD       bool // CLAUDE.md created or updated
	Skills         int  // number of skill files written
}

// Run writes AI tool integration files into root. It is called by
// scan.Run on first-run (no .sense/ existed) or when --init is passed.
func Run(root string, out io.Writer) (*Result, error) {
	res := &Result{}

	wrote, err := writeMCPJSON(root)
	if err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	}
	res.MCPJSON = wrote

	wrote, err = writeClaudeSettings(root)
	if err != nil {
		return nil, fmt.Errorf("write .claude/settings.json: %w", err)
	}
	res.ClaudeSettings = wrote

	wrote, err = writeClaudeMD(root)
	if err != nil {
		return nil, fmt.Errorf("write CLAUDE.md: %w", err)
	}
	res.ClaudeMD = wrote

	n, err := writeSkills(root)
	if err != nil {
		return nil, fmt.Errorf("write .claude/skills: %w", err)
	}
	res.Skills = n

	printSummary(out, res)
	return res, nil
}

func printSummary(out io.Writer, res *Result) {
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "AI tool integration:")
	if res.MCPJSON {
		_, _ = fmt.Fprintln(out, "  wrote .mcp.json (MCP server config)")
	}
	if res.ClaudeSettings {
		_, _ = fmt.Fprintln(out, "  wrote .claude/settings.json (hooks + permissions)")
	}
	if res.ClaudeMD {
		_, _ = fmt.Fprintln(out, "  wrote CLAUDE.md (Sense routing guidance)")
	}
	if res.Skills > 0 {
		_, _ = fmt.Fprintf(out, "  wrote %d skill files in .claude/skills/\n", res.Skills)
	}
	_, _ = fmt.Fprintln(out, "")
	_, _ = fmt.Fprintln(out, "Tip: .mcp.json and .claude/ may or may not belong in version control — that's your call.")
}

// writeMCPJSON creates or merges the Sense MCP server entry into .mcp.json.
func writeMCPJSON(root string) (bool, error) {
	path := filepath.Join(root, ".mcp.json")

	senseCfg := map[string]any{
		"command": "sense",
		"args":    []any{"mcp"},
		"serverInstructions": "When Sense is available and indexed, you MUST use Sense tools " +
			"instead of grep, glob, or exploration agents for structural and semantic code " +
			"questions. Sense provides pre-indexed results that are faster and more complete.\n\n" +
			"WHEN TO USE SENSE TOOLS:\n" +
			"- Symbol relationships, callers, dependencies → sense.graph\n" +
			"- \"What would break if I changed X?\", impact analysis → sense.blast\n" +
			"- Conceptual/semantic code search (not exact string match) → sense.search\n" +
			"- Project patterns and conventions → sense.conventions\n" +
			"- Index health, what's indexed → sense.status\n\n" +
			"WHEN NOT TO USE SENSE TOOLS:\n" +
			"- Exact text/string search → use grep\n" +
			"- Reading file contents → use your file reading tool\n" +
			"- Editing code → Sense is read-only",
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
	mergePermissions(existing, []string{"mcp__sense__*"})

	if err := writeJSONFile(path, existing); err != nil {
		return false, err
	}
	return true, nil
}
