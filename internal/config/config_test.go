package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	root := t.TempDir()
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Scan.MaxFileSizeKB != DefaultMaxFileSizeKB {
		t.Errorf("MaxFileSizeKB = %d, want %d", c.Scan.MaxFileSizeKB, DefaultMaxFileSizeKB)
	}
	if len(c.Ignore) != 0 {
		t.Errorf("Ignore = %v, want empty", c.Ignore)
	}
}

func TestLoadFromFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := `ignore:
  - vendor/
  - node_modules/
scan:
  max_file_size_kb: 256
`
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ignore) != 2 {
		t.Fatalf("Ignore len = %d, want 2", len(c.Ignore))
	}
	if c.Ignore[0] != "vendor/" || c.Ignore[1] != "node_modules/" {
		t.Errorf("Ignore = %v", c.Ignore)
	}
	if c.Scan.MaxFileSizeKB != 256 {
		t.Errorf("MaxFileSizeKB = %d, want 256", c.Scan.MaxFileSizeKB)
	}
}

func TestLoadZeroMaxFileSizeUsesDefault(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("scan:\n  max_file_size_kb: 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Scan.MaxFileSizeKB != DefaultMaxFileSizeKB {
		t.Errorf("MaxFileSizeKB = %d, want default %d", c.Scan.MaxFileSizeKB, DefaultMaxFileSizeKB)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(":\n\t::bad"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error on invalid YAML")
	}
}
