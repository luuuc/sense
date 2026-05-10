package hook

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestPreCompactEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sense", "index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	var buf bytes.Buffer
	code := Run("pre-compact", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("empty index should produce {}, got %q", buf.String())
	}
}

func TestPreCompactPopulated(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("pre-compact", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() == "{}\n" {
		t.Error("populated index should produce a message, not {}")
	}
	if !strings.Contains(buf.String(), "Sense Index Summary") {
		t.Errorf("message should contain 'Sense Index Summary', got %q", buf.String())
	}
}
