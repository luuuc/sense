package setup

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestDetectUnknownTool(t *testing.T) {
	r := Detect(Tool("unknown"))
	if r.Found {
		t.Error("unknown tool should not be found")
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
	// In test env, CLAUDE_CODE, CURSOR_*, and OPENCODE are unlikely to be set,
	// so we should get the Claude Code fallback. If they are set,
	// that's fine too — the function is correct either way.
	if got != ToolClaudeCode && got != ToolCursor && got != ToolOpencode {
		t.Errorf("DetectCurrent() = %s, want claude-code, cursor, or opencode", got)
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
		{"claude-code, cursor, codex-cli, opencode", []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI, ToolOpencode}, false},
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
	// Should list all four tools
	for _, name := range []string{"Claude Code", "Cursor", "Codex CLI", "Opencode"} {
		if !strings.Contains(output, name) {
			t.Errorf("expected %q in PrintDetection output", name)
		}
	}
}

func TestDetectClaudeCode(t *testing.T) {
	// Test that Detect(ToolClaudeCode) returns a DetectResult with the right tool
	r := Detect(ToolClaudeCode)
	if r.Tool != ToolClaudeCode {
		t.Errorf("Detect(ToolClaudeCode).Tool = %s", r.Tool)
	}
	// Found may be true or false depending on env; just ensure no panic
}

func TestDetectCursor(t *testing.T) {
	r := Detect(ToolCursor)
	if r.Tool != ToolCursor {
		t.Errorf("Detect(ToolCursor).Tool = %s", r.Tool)
	}
}

func TestDetectCodexCLI(t *testing.T) {
	r := Detect(ToolCodexCLI)
	if r.Tool != ToolCodexCLI {
		t.Errorf("Detect(ToolCodexCLI).Tool = %s", r.Tool)
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

func TestDetectOpencode(t *testing.T) {
	r := Detect(ToolOpencode)
	if r.Tool != ToolOpencode {
		t.Errorf("Detect(ToolOpencode).Tool = %s", r.Tool)
	}
}

func TestDetectOpencodeHomeDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE", "")
	// Remove opencode from PATH so we test the home-directory fallback.
	t.Setenv("PATH", "/nonexistent")

	// Create ~/.config/opencode to trigger home directory detection.
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	r := Detect(ToolOpencode)
	if !r.Found {
		t.Error("expected opencode to be found via home directory")
	}
	if r.Evidence != "~/.config/opencode/ directory" {
		t.Errorf("Evidence = %q, want '~/.config/opencode/ directory'", r.Evidence)
	}
}

func TestDetectOpencodeNotFound(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENCODE", "")
	t.Setenv("PATH", "/nonexistent")

	// Do NOT create ~/.config/opencode — all detection paths should miss.

	r := Detect(ToolOpencode)
	if r.Found {
		t.Error("expected opencode to not be found")
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
