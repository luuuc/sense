package setup

import (
	"fmt"
	"strings"
)

// tool is one AI coding tool Sense can integrate with. The registry of these
// values is the single source of truth: detection, display names, the --tools
// flag, and the configure dispatch all derive from it, so adding a tool is a
// new per-tool file (detectX + configureX) plus one entry here, with no switch
// statements to update.
type tool struct {
	id          Tool
	displayName string
	// detect reports whether this tool is installed, with human-readable
	// evidence. It reads the environment and filesystem only.
	detect func() DetectResult
	// configure writes every integration file this tool needs into root and
	// returns the relative paths written. It must be idempotent: running it
	// twice produces the same result (JSON is deep-merged, Markdown uses
	// marker comments, skills/agents are overwritten).
	configure func(root string) (*ToolResult, error)
	// currentEnv lists env vars whose presence means the user is running inside
	// this tool right now. DetectCurrent uses it to pick the active tool for
	// scan first-run. Leave empty when the tool exposes no live-session signal
	// (a tool with none can still be detected and configured; it just never
	// wins DetectCurrent, which falls back to Claude Code).
	currentEnv []string
}

// registry returns every tool Sense knows how to configure, in display order.
// To add a tool, append one entry here and implement its detect/configure pair
// in a new file (see claude.go as the template, CONTRIBUTING-AN-AI-TOOL.md for
// the walkthrough).
//
// The slice is rebuilt on each call rather than memoized: the set is tiny and
// callers are not hot, so a fresh slice keeps the function pure and dodges
// init-order questions. Do not "optimize" it into a package var.
func registry() []tool {
	return []tool{
		{id: ToolClaudeCode, displayName: "Claude Code", detect: detectClaudeCode, configure: configureClaudeCode, currentEnv: []string{"CLAUDE_CODE"}},
		{id: ToolCursor, displayName: "Cursor", detect: detectCursor, configure: configureCursor, currentEnv: cursorSessionEnvs},
		{id: ToolCodexCLI, displayName: "Codex CLI", detect: detectCodexCLI, configure: configureCodexCLI},
		{id: ToolOpencode, displayName: "Opencode", detect: detectOpencode, configure: configureOpencode, currentEnv: []string{"OPENCODE"}},
	}
}

// lookup returns the registry entry for an id, or false if none matches.
func lookup(id Tool) (tool, bool) {
	for _, t := range registry() {
		if t.id == id {
			return t, true
		}
	}
	return tool{}, false
}

// AllTools returns every tool Sense knows how to configure, in display order.
func AllTools() []Tool {
	reg := registry()
	ids := make([]Tool, len(reg))
	for i, t := range reg {
		ids[i] = t.id
	}
	return ids
}

// DisplayName returns the human-readable name for a tool.
func (t Tool) DisplayName() string {
	if e, ok := lookup(t); ok {
		return e.displayName
	}
	return string(t)
}

// Detect checks for evidence of a single tool's installation.
func Detect(t Tool) DetectResult {
	if e, ok := lookup(t); ok {
		return e.detect()
	}
	return DetectResult{Tool: t}
}

// configureTool writes the integration files for a single tool.
func configureTool(root string, t Tool) (*ToolResult, error) {
	if e, ok := lookup(t); ok {
		return e.configure(root)
	}
	return nil, fmt.Errorf("unknown tool: %s", t)
}

// ParseTools parses a comma-separated list of tool names into Tool values.
// Returns an error if any name is unrecognized.
func ParseTools(s string) ([]Tool, error) {
	if s == "" {
		return nil, nil
	}
	var tools []Tool
	for _, name := range strings.Split(s, ",") {
		id := Tool(strings.TrimSpace(name))
		if _, ok := lookup(id); !ok {
			return nil, fmt.Errorf("unknown tool %q (valid: %s)", id, strings.Join(toolNames(), ", "))
		}
		tools = append(tools, id)
	}
	return tools, nil
}

// toolNames returns the registry's tool ids as strings, for help and error text.
func toolNames() []string {
	reg := registry()
	names := make([]string, len(reg))
	for i, t := range reg {
		names[i] = string(t.id)
	}
	return names
}
