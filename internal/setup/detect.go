package setup

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Tool identifies an AI coding tool that Sense can integrate with.
type Tool string

const (
	ToolClaudeCode Tool = "claude-code"
	ToolCursor     Tool = "cursor"
	ToolCodexCLI   Tool = "codex-cli"
)

// AllTools returns every tool Sense knows how to configure, in display order.
func AllTools() []Tool {
	return []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI}
}

// DisplayName returns the human-readable name for a tool.
func (t Tool) DisplayName() string {
	switch t {
	case ToolClaudeCode:
		return "Claude Code"
	case ToolCursor:
		return "Cursor"
	case ToolCodexCLI:
		return "Codex CLI"
	default:
		return string(t)
	}
}

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

// Detect checks for evidence of a single tool's installation.
func Detect(t Tool) DetectResult {
	switch t {
	case ToolClaudeCode:
		return detectClaudeCode()
	case ToolCursor:
		return detectCursor()
	case ToolCodexCLI:
		return detectCodexCLI()
	default:
		return DetectResult{Tool: t}
	}
}

// DetectCurrent returns the tool the user is currently running inside,
// based on environment variables. Falls back to Claude Code as the
// default consumer.
func DetectCurrent() Tool {
	if os.Getenv("CLAUDE_CODE") != "" {
		return ToolClaudeCode
	}
	if hasCursorEnv() {
		return ToolCursor
	}
	return ToolClaudeCode
}

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

func detectCursor() DetectResult {
	r := DetectResult{Tool: ToolCursor}
	if hasCursorEnv() {
		r.Found = true
		r.Evidence = "CURSOR_* env var set"
		return r
	}
	if _, err := exec.LookPath("cursor"); err == nil {
		r.Found = true
		r.Evidence = "cursor on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
			r.Found = true
			r.Evidence = "~/.cursor/ directory"
			return r
		}
	}
	return r
}

func detectCodexCLI() DetectResult {
	r := DetectResult{Tool: ToolCodexCLI}
	if _, err := exec.LookPath("codex"); err == nil {
		r.Found = true
		r.Evidence = "codex on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
			r.Found = true
			r.Evidence = "~/.codex/ directory"
			return r
		}
	}
	return r
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

func hasCursorEnv() bool {
	for _, key := range []string{"CURSOR_TRACE_ID", "CURSOR_SESSION_ID"} {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}
