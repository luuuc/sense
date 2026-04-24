package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	res, err := Run(root, &buf)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !res.MCPJSON {
		t.Error("expected MCPJSON = true")
	}
	if !res.ClaudeSettings {
		t.Error("expected ClaudeSettings = true")
	}
	if !res.ClaudeMD {
		t.Error("expected ClaudeMD = true")
	}
	if res.Skills != 3 {
		t.Errorf("Skills = %d, want 3", res.Skills)
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

	if !strings.Contains(buf.String(), "AI tool integration:") {
		t.Error("expected summary output")
	}
}

func TestRunIdempotent(t *testing.T) {
	root := t.TempDir()
	var buf bytes.Buffer

	if _, err := Run(root, &buf); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// Read files after first run.
	mcp1, _ := os.ReadFile(filepath.Join(root, ".mcp.json"))
	settings1, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	claude1, _ := os.ReadFile(filepath.Join(root, "CLAUDE.md"))

	buf.Reset()
	if _, err := Run(root, &buf); err != nil {
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

	if _, err := Run(root, &bytes.Buffer{}); err != nil {
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

	if _, err := Run(root, &bytes.Buffer{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(root, ".claude/settings.json"))
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse settings.json: %v", err)
	}

	hooks, _ := m["hooks"].(map[string]any)
	preToolUse, _ := hooks["PreToolUse"].([]any)

	// Should have both: the existing Bash hook and the new Sense Grep|Glob hook.
	if len(preToolUse) != 2 {
		t.Errorf("PreToolUse hooks = %d, want 2 (existing + sense)", len(preToolUse))
	}
}

func TestClaudeMDMarkerReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "CLAUDE.md")

	// First run: creates CLAUDE.md with markers.
	if _, err := Run(root, &bytes.Buffer{}); err != nil {
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
	if _, err := Run(root, &bytes.Buffer{}); err != nil {
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

	if _, err := Run(root, &bytes.Buffer{}); err != nil {
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

	if _, err := Run(root, &bytes.Buffer{}); err != nil {
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

func TestBackupOnInvalidJSON(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(path, []byte("not json{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Run(root, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}

	backup := path + ".bak"
	if _, err := os.Stat(backup); err != nil {
		t.Errorf("expected backup file: %v", err)
	}
}
