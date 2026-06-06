package setup

import (
	"fmt"
	"io"
	"os"
)

// Tool identifies an AI coding tool that Sense can integrate with. Its string
// value is also the --tools flag name (see ParseTools). Detection, configure
// dispatch, and display names all live in the registry (registry.go); the
// per-tool detect/configure pair lives in that tool's own file.
type Tool string

const (
	ToolClaudeCode Tool = "claude-code"
	ToolCursor     Tool = "cursor"
	ToolCodexCLI   Tool = "codex-cli"
	ToolOpencode   Tool = "opencode"
)

// DetectResult holds whether a tool was found and what evidence was seen.
type DetectResult struct {
	Tool     Tool
	Found    bool
	Evidence string // human-readable reason, e.g. "claude on PATH"
}

// DetectAll checks for all known tools and returns results in display order.
func DetectAll() []DetectResult {
	var results []DetectResult
	for _, t := range AllTools() {
		results = append(results, Detect(t))
	}
	return results
}

// DetectCurrent returns the tool the user is currently running inside, based on
// each tool's live-session env vars (registry currentEnv), in display order.
// Falls back to Claude Code as the default consumer when no signal is present.
func DetectCurrent() Tool {
	for _, t := range registry() {
		for _, key := range t.currentEnv {
			if os.Getenv(key) != "" {
				return t.id
			}
		}
	}
	return ToolClaudeCode
}

// PrintDetection writes a summary of detected tools to out.
func PrintDetection(out io.Writer) {
	results := DetectAll()
	_, _ = fmt.Fprintln(out, "Detected AI tools:")
	for _, r := range results {
		if r.Found {
			_, _ = fmt.Fprintf(out, "  ✓ %-12s (%s)\n", r.Tool.DisplayName(), r.Evidence)
		} else {
			_, _ = fmt.Fprintf(out, "  ○ %-12s (not found)\n", r.Tool.DisplayName())
		}
	}
	_, _ = fmt.Fprintln(out, "")
}
