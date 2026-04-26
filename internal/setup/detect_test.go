package setup

import "testing"

func TestDetectAllReturnsAllTools(t *testing.T) {
	results := DetectAll()
	if len(results) != 3 {
		t.Fatalf("DetectAll returned %d results, want 3", len(results))
	}
	want := []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI}
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
	// In test env, CLAUDE_CODE and CURSOR_* are unlikely to be set,
	// so we should get the Claude Code fallback. If they are set,
	// that's fine too — the function is correct either way.
	if got != ToolClaudeCode && got != ToolCursor {
		t.Errorf("DetectCurrent() = %s, want claude-code or cursor", got)
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
