package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCursorMCPJSON_Fresh(t *testing.T) {
	root := t.TempDir()
	created, err := writeCursorMCPJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected created=true for fresh write")
	}
	data, err := os.ReadFile(filepath.Join(root, ".cursor", "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"sense"`) {
		t.Error("mcp.json should contain sense server config")
	}
	if !strings.Contains(content, `"command": "sense"`) {
		t.Error("mcp.json should contain sense command")
	}
}

func TestWriteCursorMCPJSON_PreservesExisting(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)
	existing := `{"mcpServers":{"other":{"command":"other-tool"}},"someKey":"value"}`
	_ = os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(existing), 0o644)

	_, err := writeCursorMCPJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cursorDir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"other"`) {
		t.Error("existing 'other' server should be preserved")
	}
	if !strings.Contains(content, `"sense"`) {
		t.Error("sense server should be added")
	}
	if !strings.Contains(content, `"someKey"`) {
		t.Error("existing top-level key should be preserved")
	}
}

func TestWriteCursorMCPJSON_OverwritesStaleSense(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)
	existing := `{"mcpServers":{"sense":{"command":"old-sense","args":["old"]}},"other":"value"}`
	_ = os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte(existing), 0o644)

	_, err := writeCursorMCPJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(cursorDir, "mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"command": "sense"`) {
		t.Error("sense command should be updated")
	}
	if strings.Contains(content, `"old-sense"`) {
		t.Error("old sense config should be overwritten")
	}
	if !strings.Contains(content, `"other"`) {
		t.Error("existing 'other' key should be preserved")
	}
}

func TestWriteCursorMCPJSON_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	cursorDir := filepath.Join(root, ".cursor")
	_ = os.MkdirAll(cursorDir, 0o755)
	_ = os.WriteFile(filepath.Join(cursorDir, "mcp.json"), []byte("not json"), 0o644)

	_, err := writeCursorMCPJSON(root)
	if err == nil {
		t.Error("expected error for invalid JSON file")
	}
}
