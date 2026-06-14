package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteOpencodePluginCreatesFile(t *testing.T) {
	root := t.TempDir()

	wrote, err := writeOpencodePlugin(root)
	if err != nil {
		t.Fatalf("writeOpencodePlugin: %v", err)
	}
	if !wrote {
		t.Fatal("writeOpencodePlugin returned false, want true")
	}

	path := filepath.Join(root, ".opencode", "plugin", "sense.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	body := string(data)
	// The two adoption levers must be present: grep/glob redirect and the
	// post-read nudge, both pointing at the Sense MCP tools.
	for _, want := range []string{
		"tool.execute.before",
		`tool === "grep"`,
		`tool === "glob"`,
		`tool === "read"`,
		"sense_graph",
		"sense_search",
		"sense_blast",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("plugin missing %q", want)
		}
	}
}

func TestWriteOpencodePluginIdempotent(t *testing.T) {
	root := t.TempDir()
	if _, err := writeOpencodePlugin(root); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := writeOpencodePlugin(root); err != nil {
		t.Fatalf("second write: %v", err)
	}
	path := filepath.Join(root, ".opencode", "plugin", "sense.js")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "export const sense") {
		t.Error("plugin export missing after re-run")
	}
}

func TestWriteOpencodePluginMkdirFails(t *testing.T) {
	root := t.TempDir()
	// Pre-create .opencode/plugin as a regular file so MkdirAll fails.
	if err := os.MkdirAll(filepath.Join(root, ".opencode"), 0o755); err != nil {
		t.Fatalf("seed .opencode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".opencode", "plugin"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	if _, err := writeOpencodePlugin(root); err == nil {
		t.Fatal("expected error when .opencode/plugin is a file, got nil")
	}
}

func TestWriteOpencodePluginWriteFails(t *testing.T) {
	root := t.TempDir()
	// Make the target path a directory so os.WriteFile on sense.js fails while
	// MkdirAll on .opencode/plugin succeeds.
	if err := os.MkdirAll(filepath.Join(root, ".opencode", "plugin", "sense.js"), 0o755); err != nil {
		t.Fatalf("seed dir blocker: %v", err)
	}
	if _, err := writeOpencodePlugin(root); err == nil {
		t.Fatal("expected error when sense.js is a directory, got nil")
	}
}

func TestConfigureOpencodeIncludesPlugin(t *testing.T) {
	root := t.TempDir()
	tr, err := configureOpencode(root)
	if err != nil {
		t.Fatalf("configureOpencode: %v", err)
	}
	// The plugin file must be reported and present on disk.
	found := false
	for _, f := range tr.Files {
		if f == ".opencode/plugin/sense.js" {
			found = true
		}
	}
	if !found {
		t.Errorf("configureOpencode did not report the plugin file; got %v", tr.Files)
	}
	if _, err := os.Stat(filepath.Join(root, ".opencode", "plugin", "sense.js")); err != nil {
		t.Errorf("plugin file not written: %v", err)
	}
}
