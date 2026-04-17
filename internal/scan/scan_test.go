package scan_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestScanCreatesIndex(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go", "pkg/b.go", "pkg/sub/c.go"})

	res, err := scan.Run(context.Background(), scan.Options{
		Root:   root,
		Output: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected %s to exist: %v", dbPath, err)
	}
}

func TestScanSkipsDotPrefixedDirs(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{
		"main.go",
		"cmd/binary.go",
		".git/HEAD",
		".git/objects/abc",
		".vscode/settings.json",
	})

	res, err := scan.Run(context.Background(), scan.Options{
		Root:   root,
		Output: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2 (should skip .git and .vscode)", res.Files)
	}
}

func TestScanIsRerunnable(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go", "b.go"})

	ctx := context.Background()

	first, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	second, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if first.Files != second.Files {
		t.Errorf("file count drifted between runs: first=%d second=%d",
			first.Files, second.Files)
	}
	// Also assert the absolute count so a regression that returns 0 on
	// every run doesn't slip past the equality check.
	if second.Files != 2 {
		t.Errorf("second.Files = %d, want 2", second.Files)
	}

	// A fresh open after the two scans confirms the adapter released its
	// file locks cleanly.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open after re-run: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestScanSchemaApplied(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go"})

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open on scanned db: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Querying against an empty, schema-applied index should succeed and
	// return no rows — proves the tables exist and are readable without
	// needing a Write* side-effect first.
	got, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query on scanned db: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Query on zero-write scan returned %d rows, want 0", len(got))
	}
}

func TestScanOutputFormat(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"x.go", "y.go"})

	var buf bytes.Buffer
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:   root,
		Output: &buf,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	out := buf.String()
	const prefix = "2 files in "
	if !strings.HasPrefix(out, prefix) || !strings.HasSuffix(out, "\n") {
		t.Fatalf("output = %q, want \"%s<duration>\\n\"", out, prefix)
	}
	duration := strings.TrimSuffix(strings.TrimPrefix(out, prefix), "\n")
	if duration == "" {
		t.Errorf("duration is empty in %q", out)
	}
}

func TestScanRespectsCustomSense(t *testing.T) {
	root := t.TempDir()
	sense := t.TempDir() // deliberately outside root
	buildTree(t, root, []string{"a.go"})

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:   root,
		Sense:  sense,
		Output: &bytes.Buffer{},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sense, "index.db")); err != nil {
		t.Errorf("custom Sense index missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".sense", "index.db")); err == nil {
		t.Error("default <Root>/.sense/index.db was created despite custom Sense option")
	}
}

func TestScanErrorsOnInvalidRoot(t *testing.T) {
	// Root points at a regular file, so MkdirAll("<file>/.sense") fails
	// with "not a directory". Proves scan.Run surfaces filesystem errors
	// rather than swallowing them.
	parent := t.TempDir()
	notADir := filepath.Join(parent, "regular-file")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := scan.Run(context.Background(), scan.Options{
		Root:   notADir,
		Output: &bytes.Buffer{},
	})
	if err == nil {
		t.Fatal("expected error when Root is a regular file, got nil")
	}
}

// buildTree creates the given relative file paths under root, each with
// placeholder content. Intermediate directories are created as needed.
func buildTree(t *testing.T, root string, files []string) {
	t.Helper()
	for _, rel := range files {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte("content"), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}
