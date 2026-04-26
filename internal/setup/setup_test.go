package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

	buf.Reset()
	if _, err := Run(root, &buf, claudeCodeOnly()); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings2, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	claude2, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))

	if string(mcp1) != string(mcp2) {
		t.Error(".mcp.json changed between runs")
	}
	if string(settings1) != string(settings2) {
		t.Error(".claude/settings.json changed between runs")
	}
	if string(claude1) != string(claude2) {
		t.Error("CLAUDE.md changed between runs")
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

func TestSettingsContainsPostToolUse(t *testing.T) {
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
	postToolUse, _ := hooks["PostToolUse"].([]any)
	if len(postToolUse) == 0 {
		t.Fatal("PostToolUse hook not found in settings")
	}

	entry, _ := postToolUse[0].(map[string]any)
	matcher, _ := entry["matcher"].(string)
	if matcher != "Write|Edit|NotebookEdit" {
		t.Errorf("PostToolUse matcher = %q, want Write|Edit|NotebookEdit", matcher)
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

	for _, path := range []string{".mcp.json", "AGENTS.md"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	data, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if !strings.Contains(string(data), markerStart) {
		t.Error("AGENTS.md missing sense markers")
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

	if _, err := Run(root, &bytes.Buffer{}, opts); err != nil {
		t.Fatalf("second Run: %v", err)
	}

	mcp2, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	agents2, _ := os.ReadFile(filepath.Join(root, "AGENTS.md"))

	if string(mcp1) != string(mcp2) {
		t.Error(".mcp.json changed between runs")
	}
	if string(agents1) != string(agents2) {
		t.Error("AGENTS.md changed between runs")
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

// --- Multi-tool integration tests ---

func TestMultiToolSetup(t *testing.T) {
	root := t.TempDir()
	opts := &Options{Tools: []Tool{ToolClaudeCode, ToolCursor, ToolCodexCLI}}

	var buf bytes.Buffer
	res, err := Run(root, &buf, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(res.Tools) != 3 {
		t.Fatalf("Tools = %d, want 3", len(res.Tools))
	}

	for _, path := range []string{
		".mcp.json",
		".claude/settings.json",
		"CLAUDE.md",
		".cursor/mcp.json",
		".cursorrules",
		"AGENTS.md",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	if !strings.Contains(buf.String(), "Claude Code, Cursor, and Codex CLI") {
		t.Errorf("summary should list all three tools, got: %s", buf.String())
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
