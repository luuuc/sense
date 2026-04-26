// Package setup generates AI tool integration files for detected
// coding tools. It writes per-tool config files (MCP server entries,
// routing guidance, hooks, skills) so that AI tools discover and
// prefer Sense without manual configuration.
//
// All writes are idempotent: JSON files are deep-merged, Markdown
// files use marker comments, skill files are overwritten. Running
// setup twice produces the same result.
package setup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options controls which tools setup configures.
type Options struct {
	// Tools overrides auto-detection. When non-empty, only these
	// tools are configured regardless of detection results.
	Tools []Tool

	// CurrentOnly restricts setup to the tool the user is currently
	// running inside (detected via env vars). Used by scan first-run.
	CurrentOnly bool
}

// ToolResult summarises what was written for a single tool.
type ToolResult struct {
	Tool  Tool
	Files []string // relative paths of files written
}

// Result summarises what setup wrote across all tools.
type Result struct {
	Tools []ToolResult
}

// Run detects installed AI tools and writes integration files into root.
// When opts is nil, all detected tools are configured.
func Run(root string, out io.Writer, opts *Options) (*Result, error) {
	tools := resolveTools(opts)
	res := &Result{}

	for _, t := range tools {
		tr, err := configureTool(root, t)
		if err != nil {
			return nil, fmt.Errorf("configure %s: %w", t.DisplayName(), err)
		}
		res.Tools = append(res.Tools, *tr)
	}

	printSetupSummary(out, res)
	return res, nil
}

func resolveTools(opts *Options) []Tool {
	if opts != nil && len(opts.Tools) > 0 {
		return opts.Tools
	}
	if opts != nil && opts.CurrentOnly {
		return []Tool{DetectCurrent()}
	}
	var tools []Tool
	for _, dr := range DetectAll() {
		if dr.Found {
			tools = append(tools, dr.Tool)
		}
	}
	if len(tools) == 0 {
		tools = []Tool{ToolClaudeCode}
	}
	return tools
}

func configureTool(root string, t Tool) (*ToolResult, error) {
	switch t {
	case ToolClaudeCode:
		return configureClaudeCode(root)
	case ToolCursor:
		return configureCursor(root)
	case ToolCodexCLI:
		return configureCodexCLI(root)
	default:
		return nil, fmt.Errorf("unknown tool: %s", t)
	}
}

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

	return tr, nil
}

func configureCursor(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolCursor}

	if wrote, err := writeCursorMCPJSON(root); err != nil {
		return nil, fmt.Errorf("write .cursor/mcp.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".cursor/mcp.json")
	}

	if wrote, err := writeCursorRules(root); err != nil {
		return nil, fmt.Errorf("write .cursorrules: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".cursorrules")
	}

	return tr, nil
}

func configureCodexCLI(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolCodexCLI}

	if wrote, err := writeMCPJSON(root); err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".mcp.json")
	}

	if wrote, err := writeAgentsMD(root); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, "AGENTS.md")
	}

	return tr, nil
}

func printSetupSummary(out io.Writer, res *Result) {
	if len(res.Tools) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "")
	for _, tr := range res.Tools {
		_, _ = fmt.Fprintf(out, "Configuring %s...\n", tr.Tool.DisplayName())
		for _, f := range tr.Files {
			_, _ = fmt.Fprintf(out, "  wrote %s\n", f)
		}
		_, _ = fmt.Fprintln(out, "")
	}

	var names []string
	for _, tr := range res.Tools {
		names = append(names, tr.Tool.DisplayName())
	}
	_, _ = fmt.Fprintf(out, "Done. Sense is configured for %s.\n", joinNames(names))
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// writeMCPJSON creates or merges the Sense MCP server entry into .mcp.json.
func writeMCPJSON(root string) (bool, error) {
	path := filepath.Join(root, ".mcp.json")

	senseCfg := map[string]any{
		"command": "sense",
		"args":    []any{"mcp"},
		"serverInstructions": "When Sense is available and indexed, you MUST use Sense tools " +
			"instead of grep, glob, or exploration agents for codebase understanding, " +
			"exploration, and research. Sense provides pre-indexed results that are faster and more complete.\n\n" +
			"WHEN TO USE SENSE TOOLS:\n" +
			"- Symbol relationships, callers, dependencies → sense.graph\n" +
			"- \"What would break if I changed X?\", impact analysis → sense.blast\n" +
			"- Conceptual/semantic code search (not exact string match) → sense.search\n" +
			"- Project patterns and conventions → sense.conventions\n" +
			"- Index health, what's indexed → sense.status\n" +
			"- ANY question about how the codebase works or is structured → start with Sense\n\n" +
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
		"PostToolUse": []any{
			map[string]any{
				"matcher": "Write|Edit|NotebookEdit",
				"hooks": []any{
					map[string]any{
						"type":    "command",
						"command": "sense hook post-tool-use",
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

// ParseTools parses a comma-separated list of tool names into Tool values.
// Returns an error if any name is unrecognized.
func ParseTools(s string) ([]Tool, error) {
	if s == "" {
		return nil, nil
	}
	var tools []Tool
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		switch name {
		case "claude-code":
			tools = append(tools, ToolClaudeCode)
		case "cursor":
			tools = append(tools, ToolCursor)
		case "codex-cli":
			tools = append(tools, ToolCodexCLI)
		default:
			return nil, fmt.Errorf("unknown tool %q (valid: claude-code, cursor, codex-cli)", name)
		}
	}
	return tools, nil
}
