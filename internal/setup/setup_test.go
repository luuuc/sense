package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
)

func claudeCodeOnly() *Options {
	return &Options{Tools: []Tool{ToolClaudeCode}}
}

func TestRunCreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	res, err := Run(root, &buf, claudeCodeOnly())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Tools) != 1 {
		t.Fatalf("Tools = %d, want 1", len(res.Tools))
	}
	if res.Tools[0].Tool != ToolClaudeCode {
		t.Errorf("Tool = %s, want claude-code", res.Tools[0].Tool)
	}

	for _, path := range []string{
		".mcp.json",
		".claude/settings.json",
		"CLAUDE.md",
		".claude/skills/sense-explore.md",
		".claude/skills/sense-impact.md",
		".claude/skills/sense-conventions.md",
		".claude/agents/deep-explore.md",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if !strings.Contains(buf.String(), "Configuring Claude Code") {
		t.Error("expected summary output")
	}
}

func TestRunIdempotent(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	if _, err := Run(root, &buf, claudeCodeOnly()); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Read files after first run.
	mcp1, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings1, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	claude1, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	agent1, _ := os.ReadFile(filepath.Join(root, ".claude/agents/deep-explore.md"))

	buf.Reset()
	if _, err := Run(root, &buf, claudeCodeOnly()); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings2, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	claude2, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	agent2, _ := os.ReadFile(filepath.Join(root, ".claude/agents/deep-explore.md"))

	if string(mcp1) != string(mcp2) {
		t.Error(".mcp.json changed between runs")
	}
	if string(settings1) != string(settings2) {
		t.Error(".claude/settings.json changed between runs")
	}
	if string(claude1) != string(claude2) {
		t.Error("CLAUDE.md changed between runs")
	}
	if string(agent1) != string(agent2) {
		t.Error(".claude/agents/deep-explore.md changed between runs")
	}
}

func TestMCPJSONPreservesExistingServers(t *testing.T) {
	root := t.TempDir()
	existing := `{"mcpServers":{"other":{"command":"other-tool"}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}

	servers, _ := m["mcpServers"].(map[string]any)
	if servers == nil {
		t.Fatal("mcpServers missing")
	}
	if servers["other"] == nil {
		t.Error("existing 'other' server was overwritten")
	}
	if servers["sense"] == nil {
		t.Error("sense server not added")
	}
}

// The Sense server entry must carry alwaysLoad:true so Claude Code pre-loads
// the tools into the initial set instead of deferring them behind ToolSearch
// (the adoption gate the model kept skipping).
func TestMCPJSONSenseAlwaysLoad(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse .mcp.json: %v", err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	sense, _ := servers["sense"].(map[string]any)
	if sense == nil {
		t.Fatal("sense server not added")
	}
	if al, ok := sense["alwaysLoad"].(bool); !ok || !al {
		t.Errorf("sense.alwaysLoad = %v, want true", sense["alwaysLoad"])
	}
}

func TestSettingsPreservesExistingHooks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other-tool check"}]}]}}`
	if err := os.WriteFile(filepath.Join(root, ".claude/settings.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, _ := m["hooks"].(map[string]any)
	preToolUse, _ := hooks["PreToolUse"].([]any)

	// Should have both: the existing Bash hook and the new Sense Grep|Glob|Agent|Bash hook.
	if len(preToolUse) != 2 {
		t.Errorf("PreToolUse hooks = %d, want 2 (existing + sense)", len(preToolUse))
	}
}

func TestSettingsOmitsRetiredPostToolUse(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, _ := m["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; ok {
		t.Error("PostToolUse hook should no longer be written (retired in pitch 26-01)")
	}
}

func TestRemoveRetiredHook(t *testing.T) {
	// No hooks map: no-op.
	s := map[string]any{}
	removeRetiredHook(s, "PostToolUse")

	// Event absent: no-op.
	s = map[string]any{"hooks": map[string]any{"SessionStart": []any{}}}
	removeRetiredHook(s, "PostToolUse")
	if _, ok := s["hooks"].(map[string]any)["SessionStart"]; !ok {
		t.Error("unrelated events must be preserved")
	}

	// Event with only a Sense entry: the key is deleted entirely.
	s = map[string]any{"hooks": map[string]any{
		"PostToolUse": []any{
			map[string]any{"hooks": []any{map[string]any{"command": "sense hook post-tool-use"}}},
		},
	}}
	removeRetiredHook(s, "PostToolUse")
	if _, ok := s["hooks"].(map[string]any)["PostToolUse"]; ok {
		t.Error("PostToolUse should be removed when only the Sense entry remained")
	}
}

// TestSetupMigratesAwayPostToolUse verifies that re-running setup over an
// older config strips the retired Sense PostToolUse hook while preserving a
// non-Sense PostToolUse hook the user added.
func TestSetupMigratesAwayPostToolUse(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Seed an old settings.json: a Sense post-tool-use hook plus a foreign one.
	old := `{
  "hooks": {
    "PostToolUse": [
      {"matcher": "Write|Edit|NotebookEdit", "hooks": [{"type": "command", "command": "sense hook post-tool-use", "timeout": 5000}]},
      {"matcher": "Write", "hooks": [{"type": "command", "command": "my-linter"}]}
    ]
  }
}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}
	hooks, _ := m["hooks"].(map[string]any)
	ptu, _ := hooks["PostToolUse"].([]any)
	if len(ptu) != 1 {
		t.Fatalf("PostToolUse should retain only the user's hook, got %d entries", len(ptu))
	}
	entry, _ := ptu[0].(map[string]any)
	inner, _ := entry["hooks"].([]any)
	cmd, _ := inner[0].(map[string]any)["command"].(string)
	if cmd != "my-linter" {
		t.Errorf("expected the user's PostToolUse hook to survive, got %q", cmd)
	}
}

func TestClaudeMDMarkerReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")

	// First run: creates CLAUDE.md with markers.
	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if !strings.Contains(string(first), markerStart) {
		t.Fatal("expected marker start")
	}
	if !strings.Contains(string(first), markerEnd) {
		t.Fatal("expected marker end")
	}

	// Second run: replaces between markers, no duplication.
	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if strings.Count(string(second), markerStart) != 1 {
		t.Errorf("expected exactly 1 marker start, got %d", strings.Count(string(second), markerStart))
	}
}

func TestClaudeMDAppendToExisting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My Project\n\nExisting content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.HasPrefix(content, "# My Project") {
		t.Error("existing content was overwritten")
	}
	if !strings.Contains(content, markerStart) {
		t.Error("sense section not appended")
	}
}

func TestClaudeMDAppendToDoubleNewline(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My Project\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "\n\n\n") {
		t.Error("should not add extra blank lines when content already ends with double newline")
	}
	if !strings.Contains(content, markerStart) {
		t.Error("sense section not appended")
	}
}

func TestClaudeMDMissingEndMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")
	// Write a file with start marker but no end marker.
	broken := "# Project\n\n<!-- sense:start -->\nold stale content\n"
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "old stale content") {
		t.Error("stale content after broken marker should be replaced")
	}
	if !strings.Contains(content, markerEnd) {
		t.Error("end marker should now be present")
	}
}

// --- Cursor integration tests ---

func TestCursorCreatesFiles(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolCursor}}

	_, err := Run(root, &bytes.Buffer{}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{".cursor/mcp.json", ".cursorrules"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(root, ".cursor/mcp.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse .cursor/mcp.json: %v", err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers["sense"] == nil {
		t.Error("sense server not in .cursor/mcp.json")
	}
}

func TestCursorIdempotent(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolCursor}}

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	mcp1, _ := os.ReadFile(filepath.Join(root, ".cursor/mcp.json"))
	rules1, _ := os.ReadFile(filepath.Join(root, ".cursorrules"))

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, ".cursor/mcp.json"))
	rules2, _ := os.ReadFile(filepath.Join(root, ".cursorrules"))

	if string(mcp1) != string(mcp2) {
		t.Error(".cursor/mcp.json changed between runs")
	}
	if string(rules1) != string(rules2) {
		t.Error(".cursorrules changed between runs")
	}
}

func TestCursorRulesMergeOverUserContent(t *testing.T) {
	root := t.TempDir()
	userContent := "# My Cursor Rules\n\nAlways use TypeScript.\n"
	if err := os.WriteFile(filepath.Join(root, ".cursorrules"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Tools: []Tool{ToolCursor}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".cursorrules"))
	content := string(data)

	if !strings.Contains(content, "My Cursor Rules") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, "Always use TypeScript") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, markerStart) {
		t.Error("sense section not appended")
	}
	if !strings.Contains(content, markerEnd) {
		t.Error("sense end marker missing")
	}
	if strings.Count(content, markerStart) != 1 {
		t.Error("duplicate sense section")
	}
}

func TestCursorMCPJSONPreservesExisting(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"mcpServers":{"my-tool":{"command":"my-cmd"}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Tools: []Tool{ToolCursor}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	servers, _ := m["mcpServers"].(map[string]any)
	if servers["my-tool"] == nil {
		t.Error("existing server was overwritten")
	}
	if servers["sense"] == nil {
		t.Error("sense server not added")
	}
}

// --- Codex CLI integration tests ---

func TestCodexCreatesFiles(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolCodexCLI}}

	_, err := Run(root, &bytes.Buffer{}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{".codex/config.toml", ".mcp.json", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(data), markerStart) {
		t.Error("AGENTS.md missing sense markers")
	}

	// The config.toml is the actual Codex MCP registration: Codex ignores
	// .mcp.json, so this block is what makes the sense_* tools appear.
	toml, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	cfg := string(toml)
	for _, want := range []string{"[mcp_servers.sense]", `command = "sense"`, `args = ["mcp"]`, tomlMarkerStart, tomlMarkerEnd} {
		if !strings.Contains(cfg, want) {
			t.Errorf(".codex/config.toml missing %q\ngot:\n%s", want, cfg)
		}
	}
}

func TestCodexIdempotent(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolCodexCLI}}

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	mcp1, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	agents1, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	toml1, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	agents2, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	toml2, _ := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))

	if string(mcp1) != string(mcp2) {
		t.Error(".mcp.json changed between runs")
	}
	if string(agents1) != string(agents2) {
		t.Error("AGENTS.md changed between runs")
	}
	if string(toml1) != string(toml2) {
		t.Errorf(".codex/config.toml changed between runs:\nfirst:\n%s\nsecond:\n%s", toml1, toml2)
	}
}

func TestCodexConfigPreservesUserContentAndSkipsManualEntry(t *testing.T) {
	t.Run("appends below existing user config", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		userCfg := "model = \"gpt-5.5-codex\"\nmodel_reasoning_effort = \"high\"\n"
		path := filepath.Join(root, ".codex", "config.toml")
		if err := os.WriteFile(path, []byte(userCfg), 0o644); err != nil {
			t.Fatal(err)
		}

		opts := &Options{Tools: []Tool{ToolCodexCLI}}
		if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}

		got, _ := os.ReadFile(path)
		content := string(got)
		if !strings.Contains(content, `model = "gpt-5.5-codex"`) {
			t.Error("user config was overwritten")
		}
		if !strings.Contains(content, "[mcp_servers.sense]") {
			t.Error("sense block not appended")
		}
		if c := strings.Count(content, tomlMarkerStart); c != 1 {
			t.Errorf("expected exactly 1 sense block, got %d", c)
		}
	})

	t.Run("does not clobber a hand-written sense entry", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
			t.Fatal(err)
		}
		manual := "[mcp_servers.sense]\ncommand = \"/custom/sense\"\nargs = [\"mcp\", \"--verbose\"]\n"
		path := filepath.Join(root, ".codex", "config.toml")
		if err := os.WriteFile(path, []byte(manual), 0o644); err != nil {
			t.Fatal(err)
		}

		opts := &Options{Tools: []Tool{ToolCodexCLI}}
		if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
			t.Fatalf("Run: %v", err)
		}

		got, _ := os.ReadFile(path)
		content := string(got)
		if content != manual {
			t.Errorf("hand-written entry was modified:\nwant:\n%s\ngot:\n%s", manual, content)
		}
		if strings.Contains(content, tomlMarkerStart) {
			t.Error("sense markers added on top of a manual entry")
		}
	})
}

func TestCodexTrustNotePrinted(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer
	if _, err := Run(root, &buf, &Options{Tools: []Tool{ToolCodexCLI}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "trusted projects") {
		t.Errorf("setup output missing Codex trust note:\n%s", buf.String())
	}
}

func TestAgentsMDMergeOverUserContent(t *testing.T) {
	root := t.TempDir()
	userContent := "# My Agent Rules\n\nUse Python 3.12.\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Tools: []Tool{ToolCodexCLI}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	content := string(data)

	if !strings.Contains(content, "My Agent Rules") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, "Use Python 3.12") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, markerStart) {
		t.Error("sense section not appended")
	}
	if strings.Count(content, markerStart) != 1 {
		t.Error("duplicate sense section")
	}
}

// --- Opencode integration tests ---

func TestOpencodeCreatesFiles(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolOpencode}}

	_, err := Run(root, &bytes.Buffer{}, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, path := range []string{
		"opencode.json",
		"AGENTS.md",
		".opencode/skills/sense-explore/SKILL.md",
		".opencode/skills/sense-impact/SKILL.md",
		".opencode/skills/sense-conventions/SKILL.md",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse opencode.json: %v", err)
	}
	mcpServers, _ := m["mcp"].(map[string]any)
	if mcpServers == nil || mcpServers["sense"] == nil {
		t.Error("sense server not in opencode.json mcp")
	}
}

func TestOpencodeIdempotent(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolOpencode}}

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	mcp1, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	agents1, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	agents2, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))

	if string(mcp1) != string(mcp2) {
		t.Error("opencode.json changed between runs")
	}
	if string(agents1) != string(agents2) {
		t.Error("AGENTS.md changed between runs")
	}
}

func TestOpencodeJSONPreservesExisting(t *testing.T) {
	root := t.TempDir()
	existing := `{"mcp":{"other-server":{"type":"remote","url":"https://example.com"}}}`
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Tools: []Tool{ToolOpencode}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "opencode.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	mcpServers, _ := m["mcp"].(map[string]any)
	if mcpServers["other-server"] == nil {
		t.Error("existing server was overwritten")
	}
	if mcpServers["sense"] == nil {
		t.Error("sense server not added")
	}
}

func TestOpencodeAgentsMDMergeOverUserContent(t *testing.T) {
	root := t.TempDir()
	userContent := "# My Opencode Rules\n\nUse Go 1.23.\n"
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	opts := &Options{Tools: []Tool{ToolOpencode}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	content := string(data)

	if !strings.Contains(content, "My Opencode Rules") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, "Use Go 1.23") {
		t.Error("user content was overwritten")
	}
	if !strings.Contains(content, markerStart) {
		t.Error("sense section not appended")
	}
	if strings.Count(content, markerStart) != 1 {
		t.Error("duplicate sense section")
	}
}

func TestOpencodeJSONReadError(t *testing.T) {
	root := t.TempDir()
	// Create opencode.json with invalid JSON so readJSONFile fails.
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), []byte("not json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeOpencodeJSON(root)
	if err == nil {
		t.Fatal("expected error when opencode.json contains invalid JSON")
	}
}

func TestOpencodeAgentsMDWriteError(t *testing.T) {
	root := t.TempDir()
	// Create AGENTS.md as a directory so writeMarkerFile fails.
	if err := os.MkdirAll(filepath.Join(root, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureOpencode(root)
	if err == nil {
		t.Fatal("expected error when AGENTS.md is a directory")
	}
}

func TestOpencodeJSONWriteError(t *testing.T) {
	root := t.TempDir()
	// Create opencode.json as a directory so writeJSONFile fails.
	if err := os.MkdirAll(filepath.Join(root, "opencode.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureOpencode(root)
	if err == nil {
		t.Fatal("expected error when opencode.json is a directory")
	}
}

func TestOpencodeSkillsWriteError(t *testing.T) {
	root := t.TempDir()
	// Create .opencode as a file so MkdirAll(".opencode/skills") fails.
	if err := os.WriteFile(filepath.Join(root, ".opencode"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeOpencodeSkills(root)
	if err == nil {
		t.Fatal("expected error when .opencode exists as a file")
	}
}

func TestOpencodeSkillsSubdirWriteError(t *testing.T) {
	root := t.TempDir()
	skillsDir := filepath.Join(root, ".opencode", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create a file with the same name as a skill directory so MkdirAll fails.
	if err := os.WriteFile(filepath.Join(skillsDir, "sense-explore"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeOpencodeSkills(root)
	if err == nil {
		t.Fatal("expected error when skill subdir exists as a file")
	}
}

// --- Multi-tool integration tests ---

func TestMultiToolSetup(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI, ToolOpencode}}

	var buf bytes.Buffer
	res, err := Run(root, &buf, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Tools) != 4 {
		t.Fatalf("Tools = %d, want 4", len(res.Tools))
	}

	for _, path := range []string{
		".mcp.json",
		".claude/settings.json",
		"CLAUDE.md",
		".cursor/mcp.json",
		".cursorrules",
		"AGENTS.md",
		"opencode.json",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if !strings.Contains(buf.String(), "Claude Code, Cursor, Codex CLI, and Opencode") {
		t.Errorf("summary should list all four tools, got: %s", buf.String())
	}
}

func TestResolveToolsDefaultsToClaudeCode(t *testing.T) {
	tools := resolveTools(nil)
	// With nil opts, resolveTools runs detection. At minimum it should
	// return at least one tool (falls back to Claude Code if none detected).
	if len(tools) == 0 {
		t.Fatal("resolveTools(nil) returned empty")
	}
}

func TestGuidanceMarkdownContent(t *testing.T) {
	for _, tool := range []string{"sense_graph", "sense_search", "sense_blast", "sense_conventions", "sense_status"} {
		if !strings.Contains(guidanceMarkdown, tool) {
			t.Errorf("guidanceMarkdown must include %s in the tool table", tool)
		}
	}
	if !strings.Contains(guidanceMarkdown, "MUST NOT") {
		t.Error("guidanceMarkdown must include the MUST NOT rule")
	}
	if !strings.Contains(guidanceMarkdown, "grepping a name") {
		t.Error("guidanceMarkdown must include the name-vs-string grep routing rule")
	}
	if strings.Contains(guidanceMarkdown, "FIRST action in every conversation") {
		t.Error("guidanceMarkdown must not include cold-start protocol (now handled by SessionStart hook)")
	}
	if strings.Contains(guidanceMarkdown, "sense_orient") || strings.Contains(guidanceMarkdown, "sense.orient") {
		t.Error("guidanceMarkdown must not reference the removed orient tool")
	}
	// The guidance is tool-agnostic on purpose: a single source feeds every
	// tool's instructions file, so it must not name one tool's UI.
	for _, leak := range []string{"Explore/deep-explore", ".cursorrules", "Claude Code"} {
		if strings.Contains(guidanceMarkdown, leak) {
			t.Errorf("guidanceMarkdown must stay tool-agnostic, found %q", leak)
		}
	}
	if strings.Contains(mcpio.ServerInstructions, "sense.orient") {
		t.Error("ServerInstructions must not reference the removed orient tool")
	}
	if lines := strings.Count(guidanceMarkdown, "\n"); lines > 20 {
		t.Errorf("guidanceMarkdown should be ≤20 lines, got %d", lines)
	}
}

// TestGuidanceMarkdownIsSingleSource asserts every tool's instructions file
// receives the same guidance body, so tuning it is a one-place edit.
func TestGuidanceMarkdownIsSingleSource(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI, ToolOpencode}}
	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, rel := range []string{"CLAUDE.md", ".cursorrules", "AGENTS.md"} {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if !strings.Contains(string(data), guidanceMarkdown) {
			t.Errorf("%s does not contain the canonical guidanceMarkdown", rel)
		}
	}
}

func TestClaudeMDShortenedIdempotent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# My Project\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if c := strings.Count(content, markerStart); c != 1 {
		t.Errorf("expected 1 marker start after 3 runs, got %d", c)
	}
	if c := strings.Count(content, markerEnd); c != 1 {
		t.Errorf("expected 1 marker end after 3 runs, got %d", c)
	}
	if !strings.HasPrefix(content, "# My Project\n") {
		t.Error("user content before markers was lost")
	}
}

func TestAgentFileContent(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(root, &bytes.Buffer{}, claudeCodeOnly()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".claude/agents/deep-explore.md"))
	if err != nil {
		t.Fatalf("read agent file: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		"name: deep-explore",
		"ToolSearch",
		"sense_graph",
		"sense_search",
		"sense_blast",
		"sense_conventions",
		"sense_status",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("agent file missing %q", want)
		}
	}
}

func TestAgentWriteErrorMkdir(t *testing.T) {
	root := t.TempDir()
	// Create .claude/agents as a file so MkdirAll fails.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "agents"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeAgents(root)
	if err == nil {
		t.Fatal("expected error when agents exists as a file")
	}
}

func TestAgentWriteErrorFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Create agent filename as a directory so WriteFile fails.
	if err := os.MkdirAll(filepath.Join(dir, "deep-explore.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := writeAgents(root)
	if err == nil {
		t.Fatal("expected error when agent filename is a directory")
	}
}

func TestClaudeCodeAgentWriteError(t *testing.T) {
	root := t.TempDir()
	// Create .claude/agents as a file so writeAgents MkdirAll fails
	// inside configureClaudeCode.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "agents"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := configureClaudeCode(root)
	if err == nil {
		t.Fatal("expected error when agents dir is blocked")
	}
	if !strings.Contains(err.Error(), "write .claude/agents") {
		t.Errorf("error = %q, want mention of .claude/agents", err)
	}
}

// --- resolveTools / configureTool / printSetupSummary branch coverage ---

func TestResolveToolsCurrentOnly(t *testing.T) {
	// CurrentOnly routes through DetectCurrent, which always returns one tool.
	tools := resolveTools(&Options{CurrentOnly: true})
	if len(tools) != 1 {
		t.Fatalf("resolveTools(CurrentOnly) = %d tools, want 1", len(tools))
	}
}

func TestResolveToolsFallsBackWhenNoneDetected(t *testing.T) {
	// Strip every detection signal so DetectAll finds nothing, forcing the
	// Claude Code fallback branch.
	t.Setenv("CLAUDE_CODE", "")
	t.Setenv("CURSOR_TRACE_ID", "")
	t.Setenv("CURSOR_SESSION_ID", "")
	t.Setenv("OPENCODE", "")
	t.Setenv("PATH", "")
	t.Setenv("HOME", t.TempDir())

	tools := resolveTools(nil)
	if len(tools) != 1 || tools[0] != ToolClaudeCode {
		t.Fatalf("resolveTools(nil) with no tools detected = %v, want [claude-code]", tools)
	}
}

func TestConfigureToolUnknown(t *testing.T) {
	_, err := configureTool(t.TempDir(), Tool("bogus-tool"))
	if err == nil {
		t.Fatal("expected error for unknown tool")
	}
	if !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("error = %q, want mention of unknown tool", err)
	}
}

func TestPrintSetupSummaryEmpty(t *testing.T) {
	var buf bytes.Buffer
	printSetupSummary(&buf, &Result{})
	if buf.Len() != 0 {
		t.Errorf("printSetupSummary with no tools wrote %q, want nothing", buf.String())
	}
}

// --- configureClaudeCode error-path coverage ---

func TestConfigureClaudeCodeSettingsError(t *testing.T) {
	root := t.TempDir()
	// .claude as a regular file makes writeClaudeSettings' MkdirAll fail,
	// after writeMCPJSON has already succeeded.
	if err := os.WriteFile(filepath.Join(root, ".claude"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := configureClaudeCode(root)
	if err == nil {
		t.Fatal("expected error when .claude is a file")
	}
	if !strings.Contains(err.Error(), ".claude/settings.json") {
		t.Errorf("error = %q, want mention of settings.json", err)
	}
}

func TestConfigureClaudeCodeClaudeMDError(t *testing.T) {
	root := t.TempDir()
	// CLAUDE.md as a directory makes writeClaudeMD fail, after .mcp.json and
	// .claude/settings.json have been written.
	if err := os.MkdirAll(filepath.Join(root, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureClaudeCode(root)
	if err == nil {
		t.Fatal("expected error when CLAUDE.md is a directory")
	}
	if !strings.Contains(err.Error(), "CLAUDE.md") {
		t.Errorf("error = %q, want mention of CLAUDE.md", err)
	}
}

func TestConfigureClaudeCodeSkillsError(t *testing.T) {
	root := t.TempDir()
	// Pre-create .claude so settings.json writes fine, but block the skills
	// subdir by occupying its path with a file.
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "skills"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := configureClaudeCode(root)
	if err == nil {
		t.Fatal("expected error when .claude/skills is a file")
	}
	if !strings.Contains(err.Error(), ".claude/skills") {
		t.Errorf("error = %q, want mention of skills", err)
	}
}

// --- configureCursor error-path coverage ---

func TestConfigureCursorMCPError(t *testing.T) {
	root := t.TempDir()
	// .cursor as a file makes writeCursorMCPJSON's MkdirAll fail.
	if err := os.WriteFile(filepath.Join(root, ".cursor"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := configureCursor(root)
	if err == nil {
		t.Fatal("expected error when .cursor is a file")
	}
	if !strings.Contains(err.Error(), ".cursor/mcp.json") {
		t.Errorf("error = %q, want mention of cursor mcp.json", err)
	}
}

func TestConfigureCursorRulesError(t *testing.T) {
	root := t.TempDir()
	// .cursorrules as a directory makes writeCursorRules fail, after
	// .cursor/mcp.json was written successfully.
	if err := os.MkdirAll(filepath.Join(root, ".cursorrules"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureCursor(root)
	if err == nil {
		t.Fatal("expected error when .cursorrules is a directory")
	}
	if !strings.Contains(err.Error(), ".cursorrules") {
		t.Errorf("error = %q, want mention of .cursorrules", err)
	}
}

// --- configureCodexCLI error-path coverage ---

func TestConfigureCodexMCPError(t *testing.T) {
	root := t.TempDir()
	// .mcp.json as a directory makes writeMCPJSON's readJSONFile fail.
	if err := os.MkdirAll(filepath.Join(root, ".mcp.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureCodexCLI(root)
	if err == nil {
		t.Fatal("expected error when .mcp.json is a directory")
	}
	if !strings.Contains(err.Error(), ".mcp.json") {
		t.Errorf("error = %q, want mention of .mcp.json", err)
	}
}

func TestConfigureCodexConfigTOMLError(t *testing.T) {
	root := t.TempDir()
	// .codex/config.toml as a directory makes writeCodexConfigTOML's
	// os.ReadFile fail with a non-NotExist error before any file is written.
	if err := os.MkdirAll(filepath.Join(root, ".codex", "config.toml"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureCodexCLI(root)
	if err == nil {
		t.Fatal("expected error when .codex/config.toml is a directory")
	}
	if !strings.Contains(err.Error(), ".codex/config.toml") {
		t.Errorf("error = %q, want mention of .codex/config.toml", err)
	}
}

func TestConfigureCodexConfigDirCreateError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
	root := t.TempDir()
	// A read-only root lets ReadFile report NotExist but makes MkdirAll of
	// .codex fail, exercising the directory-create error branch.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })

	_, err := writeCodexConfigTOML(root)
	if err == nil {
		t.Fatal("expected error when .codex cannot be created")
	}
}

func TestConfigureCodexAgentsError(t *testing.T) {
	root := t.TempDir()
	// AGENTS.md as a directory makes writeAgentsMD fail, after .mcp.json
	// was written successfully.
	if err := os.MkdirAll(filepath.Join(root, "AGENTS.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := configureCodexCLI(root)
	if err == nil {
		t.Fatal("expected error when AGENTS.md is a directory")
	}
	if !strings.Contains(err.Error(), "AGENTS.md") {
		t.Errorf("error = %q, want mention of AGENTS.md", err)
	}
}

// --- writeMCPJSON / writeClaudeSettings error-path coverage ---

func TestWriteMCPJSONWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("write-permission checks are bypassed when running as root")
	}
	root := t.TempDir()
	// An empty, read-only .mcp.json: readJSONFile succeeds (empty -> {}),
	// but writeJSONFile's truncating open is denied.
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte{}, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := writeMCPJSON(root)
	if err == nil {
		t.Fatal("expected error writing to a read-only .mcp.json")
	}
}

func TestWriteClaudeSettingsReadError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid JSON makes readJSONFile fail (and back up the file).
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("not json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := writeClaudeSettings(root)
	if err == nil {
		t.Fatal("expected error when settings.json is invalid JSON")
	}
}

func TestWriteClaudeSettingsWriteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("write-permission checks are bypassed when running as root")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Empty, read-only settings.json: readJSONFile succeeds, writeJSONFile fails.
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte{}, 0o444); err != nil {
		t.Fatal(err)
	}
	_, err := writeClaudeSettings(root)
	if err == nil {
		t.Fatal("expected error writing to a read-only settings.json")
	}
}

func TestBackupOnInvalidJSON(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(path, []byte("not json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, &bytes.Buffer{}, claudeCodeOnly())
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	backup := path + ".bak"
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("expected backup file: %v", err)
	}
}
