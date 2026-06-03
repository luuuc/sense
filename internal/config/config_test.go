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

func TestEmbeddingsEnabledDefault(t *testing.T) {
	c := &Config{}
	if !c.EmbeddingsEnabled() {
		t.Error("EmbeddingsEnabled() should default to true")
	}
}

func TestEmbeddingsEnabledExplicitTrue(t *testing.T) {
	v := true
	c := &Config{Embeddings: EmbeddingsConfig{Enabled: &v}}
	if !c.EmbeddingsEnabled() {
		t.Error("EmbeddingsEnabled() should be true when explicitly set")
	}
}

func TestEmbeddingsEnabledExplicitFalse(t *testing.T) {
	v := false
	c := &Config{Embeddings: EmbeddingsConfig{Enabled: &v}}
	if c.EmbeddingsEnabled() {
		t.Error("EmbeddingsEnabled() should be false when explicitly disabled")
	}
}

func TestIsEmbeddingsEnabled_EnvFalse(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	root := t.TempDir()
	if IsEmbeddingsEnabled(root) {
		t.Error("IsEmbeddingsEnabled should return false when SENSE_EMBEDDINGS=false")
	}
}

func TestIsEmbeddingsEnabled_EnvZero(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "0")
	root := t.TempDir()
	if IsEmbeddingsEnabled(root) {
		t.Error("IsEmbeddingsEnabled should return false when SENSE_EMBEDDINGS=0")
	}
}

func TestIsEmbeddingsEnabled_EnvTrue(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "true")
	root := t.TempDir()
	if !IsEmbeddingsEnabled(root) {
		t.Error("IsEmbeddingsEnabled should return true when SENSE_EMBEDDINGS=true")
	}
}

func TestIsEmbeddingsEnabled_NoEnv(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "")
	root := t.TempDir()
	// No config file, default is true
	if !IsEmbeddingsEnabled(root) {
		t.Error("IsEmbeddingsEnabled should default to true with no env and no config")
	}
}

func TestIsEmbeddingsEnabled_ConfigFalse(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "")
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "embeddings:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsEmbeddingsEnabled(root) {
		t.Error("IsEmbeddingsEnabled should return false from config")
	}
}

func TestLoadEmbeddingsFromFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "embeddings:\n  enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.EmbeddingsEnabled() {
		t.Error("EmbeddingsEnabled should be false from file")
	}
}

func TestLoadWatchDebounceMs(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "scan:\n  watch_debounce_ms: 500\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if c.Scan.WatchDebounceMs != 500 {
		t.Errorf("WatchDebounceMs = %d, want 500", c.Scan.WatchDebounceMs)
	}
}

func TestWatchEnabledDefault(t *testing.T) {
	c := &Config{}
	if !c.WatchEnabled() {
		t.Error("watch should default to enabled")
	}
}

func TestWatchEnabledExplicit(t *testing.T) {
	f := false
	if (&Config{Watch: &f}).WatchEnabled() {
		t.Error("watch: false should disable")
	}
	tr := true
	if !(&Config{Watch: &tr}).WatchEnabled() {
		t.Error("watch: true should enable")
	}
}

func TestIsWatchEnabledEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SENSE_WATCH", "false")
	if IsWatchEnabled(root) {
		t.Error("SENSE_WATCH=false should disable")
	}
	t.Setenv("SENSE_WATCH", "true")
	if !IsWatchEnabled(root) {
		t.Error("SENSE_WATCH=true should enable")
	}
}

func TestIsWatchEnabledConfigFile(t *testing.T) {
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "config.yml"), []byte("watch: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsWatchEnabled(root) {
		t.Error("watch: false in config.yml should disable")
	}
}

// A broken config.yml must not silently turn a feature off: both watch and
// embeddings fall back to enabled when Load fails to parse the file. This
// pins the promise in IsWatchEnabled/IsEmbeddingsEnabled's doc comments.
func TestEnvOrConfigBoolUnparseableConfigDefaultsToEnabled(t *testing.T) {
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "config.yml"), []byte("watch: [unterminated"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !IsWatchEnabled(root) {
		t.Error("unparseable config.yml should default watch to enabled")
	}
	if !IsEmbeddingsEnabled(root) {
		t.Error("unparseable config.yml should default embeddings to enabled")
	}
}
