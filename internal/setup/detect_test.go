package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- existing integration tests (do not delete) ---

func TestDetectAllReturnsAllTools(t *testing.T) {
	results := DetectAll()
	if len(results) != 4 {
		t.Fatalf("DetectAll returned %d results, want 4", len(results))
	}
	want := []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI, ToolOpencode}
	for i, r := range results {
		if r.Tool != want[i] {
			t.Errorf("result[%d].Tool = %s, want %s", i, r.Tool, want[i])
		}
	}
}

func TestToolDisplayName(t *testing.T) {
	cases := []struct {
		tool Tool
		want string
	}{
		{ToolClaudeCode, "Claude Code"},
		{ToolCursor, "Cursor"},
		{ToolCodexCLI, "Codex CLI"},
		{ToolOpencode, "Opencode"},
		{Tool("other"), "other"},
	}
	for _, tc := range cases {
		if got := tc.tool.DisplayName(); got != tc.want {
			t.Errorf("%s.DisplayName() = %q, want %q", tc.tool, got, tc.want)
		}
	}
}

func TestDetectCurrentFallsBackToClaudeCode(t *testing.T) {
	got := DetectCurrent()
	if got != ToolClaudeCode && got != ToolCursor && got != ToolOpencode {
		t.Errorf("DetectCurrent() = %s, want claude-code, cursor, or opencode", got)
	}
}

func TestDetectCurrentWithCursorEnv(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "test-trace")
	got := DetectCurrent()
	if got != ToolCursor {
		t.Errorf("DetectCurrent() = %s with CURSOR_TRACE_ID set, want cursor", got)
	}
}

func TestDetectCurrentWithClaudeCodeEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "1")
	got := DetectCurrent()
	if got != ToolClaudeCode {
		t.Errorf("DetectCurrent() = %s with CLAUDE_CODE set, want claude-code", got)
	}
}

func TestDetectCurrentWithOpencodeEnv(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	t.Setenv("OPENCODE", "1")
	got := DetectCurrent()
	if got != ToolOpencode {
		t.Errorf("DetectCurrent() = %s with OPENCODE set, want opencode", got)
	}
}

func TestParseTools(t *testing.T) {
	cases := []struct {
		input string
		want  []Tool
		err   bool
	}{
		{"", nil, false},
		{"claude-code", []Tool{ToolClaudeCode}, false},
		{"cursor", []Tool{ToolCursor}, false},
		{"codex-cli", []Tool{ToolCodexCLI}, false},
		{"opencode", []Tool{ToolOpencode}, false},
		{"claude-code,cursor", []Tool{ToolClaudeCode, ToolCursor}, false},
		{"claude-code, cursor, codex-cli", []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI}, false},
		{"unknown", nil, true},
		{"claude-code,bad", nil, true},
	}
	for _, tc := range cases {
		tools, err := ParseTools(tc.input)
		if tc.err && err == nil {
			t.Errorf("ParseTools(%q): expected error", tc.input)
			continue
		}
		if !tc.err && err != nil {
			t.Errorf("ParseTools(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if len(tools) != len(tc.want) {
			t.Errorf("ParseTools(%q) = %d tools, want %d", tc.input, len(tools), len(tc.want))
			continue
		}
		for i := range tools {
			if tools[i] != tc.want[i] {
				t.Errorf("ParseTools(%q)[%d] = %s, want %s", tc.input, i, tools[i], tc.want[i])
			}
		}
	}
}

func TestPrintDetection(t *testing.T) {
	var buf bytes.Buffer
	PrintDetection(&buf)
	output := buf.String()
	if !strings.Contains(output, "Detected AI tools:") {
		t.Error("expected header in PrintDetection output")
	}
	for _, name := range []string{"Claude Code", "Cursor", "Codex CLI", "Opencode"} {
		if !strings.Contains(output, name) {
			t.Errorf("expected %q in PrintDetection output", name)
		}
	}
}

func TestHasCursorEnvSessionID(t *testing.T) {
	t.Setenv("CURSOR_SESSION_ID", "test-session")
	if !hasCursorEnv() {
		t.Error("hasCursorEnv should return true with CURSOR_SESSION_ID set")
	}
}

func TestHasCursorEnvNone(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	if hasCursorEnv() {
		t.Error("hasCursorEnv should return false with no cursor env vars")
	}
}

func TestAllToolsOrder(t *testing.T) {
	tools := AllTools()
	if len(tools) != 4 {
		t.Fatalf("AllTools() len = %d, want 4", len(tools))
	}
	if tools[0] != ToolClaudeCode || tools[1] != ToolCursor || tools[2] != ToolCodexCLI || tools[3] != ToolOpencode {
		t.Errorf("AllTools() = %v, want [claude-code cursor codex-cli opencode]", tools)
	}
}

func TestJoinNames(t *testing.T) {
	cases := []struct {
		names []string
		want  string
	}{
		{nil, ""},
		{[]string{"A"}, "A"},
		{[]string{"A", "B"}, "A and B"},
		{[]string{"A", "B", "C"}, "A, B, and C"},
	}
	for _, tc := range cases {
		if got := joinNames(tc.names); got != tc.want {
			t.Errorf("joinNames(%v) = %q, want %q", tc.names, got, tc.want)
		}
	}
}

// --- coverage floor tests for detect.go ---

func TestDetectClaudeCodeEnvVar(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "1")
	result := detectClaudeCode()
	if !result.Found {
		t.Error("expected Found=true for CLAUDE_CODE env var")
	}
	if result.Evidence != "CLAUDE_CODE env var set" {
		t.Errorf("expected env var evidence, got %q", result.Evidence)
	}
}

func TestDetectClaudeCodeHomeDir(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("PATH", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o755)

	result := detectClaudeCode()
	if !result.Found {
		t.Error("expected Found=true for ~/.claude directory")
	}
	if result.Evidence != "~/.claude/ directory" {
		t.Errorf("expected home dir evidence, got %q", result.Evidence)
	}
}

func TestDetectCursorEnvVar(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "abc123")
	result := detectCursor()
	if !result.Found {
		t.Error("expected Found=true for CURSOR_TRACE_ID env var")
	}
	if result.Evidence != "CURSOR_* env var set" {
		t.Errorf("expected env var evidence, got %q", result.Evidence)
	}
}

func TestDetectCursorHomeDir(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".cursor"), 0o755)

	result := detectCursor()
	if !result.Found {
		t.Error("expected Found=true for ~/.cursor directory")
	}
	if result.Evidence != "~/.cursor/ directory" {
		t.Errorf("expected home dir evidence, got %q", result.Evidence)
	}
}

func TestDetectCodexCLIPath(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	_ = os.WriteFile(filepath.Join(binDir, "codex"), []byte("#!/bin/sh\n"), 0o755)

	result := detectCodexCLI()
	if !result.Found {
		t.Error("expected Found=true for codex on PATH")
	}
	if result.Evidence != "codex on PATH" {
		t.Errorf("expected PATH evidence, got %q", result.Evidence)
	}
}

func TestDetectCodexCLIHomeDir(t *testing.T) {
	t.Setenv("PATH", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".codex"), 0o755)

	result := detectCodexCLI()
	if !result.Found {
		t.Error("expected Found=true for ~/.codex directory")
	}
	if result.Evidence != "~/.codex/ directory" {
		t.Errorf("expected home dir evidence, got %q", result.Evidence)
	}
}

func TestDetectOpencodePath(t *testing.T) {
	t.Setenv("OPENCODE", "")
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	_ = os.WriteFile(filepath.Join(binDir, "opencode"), []byte("#!/bin/sh\n"), 0o755)

	result := detectOpencode()
	if !result.Found {
		t.Error("expected Found=true for opencode on PATH")
	}
	if result.Evidence != "opencode on PATH" {
		t.Errorf("expected PATH evidence, got %q", result.Evidence)
	}
}

func TestPrintDetectionFound(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "1")
	var buf strings.Builder
	PrintDetection(&buf)
	output := buf.String()
	if !strings.Contains(output, "Claude Code") {
		t.Errorf("expected 'Claude Code' in output, got:\n%s", output)
	}
	if !strings.Contains(output, "Found") && !strings.Contains(output, "✓") {
		t.Errorf("expected found indicator in output, got:\n%s", output)
	}
}

func TestPrintDetectionNotFound(t *testing.T) {
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	t.Setenv("OPENCODE", "")
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())

	var buf strings.Builder
	PrintDetection(&buf)
	output := buf.String()
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' in output when no tools detected, got:\n%s", output)
	}
}

func TestDetectUnknownTool(t *testing.T) {
	result := Detect(Tool("unknown"))
	if result.Found {
		t.Error("expected Found=false for unknown tool")
	}
	if result.Tool != Tool("unknown") {
		t.Errorf("expected Tool='unknown', got %q", result.Tool)
	}
}

func TestDisplayNameUnknown(t *testing.T) {
	name := Tool("unknown").DisplayName()
	if name != "unknown" {
		t.Errorf("expected DisplayName='unknown', got %q", name)
	}
}

func TestDetectCursorPath(t *testing.T) {
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	_ = os.WriteFile(filepath.Join(binDir, "cursor"), []byte("#!/bin/sh\n"), 0o755)

	result := detectCursor()
	if !result.Found {
		t.Error("expected Found=true for cursor on PATH")
	}
	if result.Evidence != "cursor on PATH" {
		t.Errorf("expected PATH evidence, got %q", result.Evidence)
	}
}

func TestDetectOpencodeEnvVar(t *testing.T) {
	t.Setenv("OPENCODE", "1")
	result := detectOpencode()
	if !result.Found {
		t.Error("expected Found=true for OPENCODE env var")
	}
	if result.Evidence != "OPENCODE env var set" {
		t.Errorf("expected env var evidence, got %q", result.Evidence)
	}
}

func TestDetectOpencodeHomeDir(t *testing.T) {
	t.Setenv("OPENCODE", "")
	t.Setenv("PATH", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	_ = os.MkdirAll(filepath.Join(home, ".config", "opencode"), 0o755)

	result := detectOpencode()
	if !result.Found {
		t.Error("expected Found=true for ~/.config/opencode directory")
	}
	if result.Evidence != "~/.config/opencode/ directory" {
		t.Errorf("expected home dir evidence, got %q", result.Evidence)
	}
}
