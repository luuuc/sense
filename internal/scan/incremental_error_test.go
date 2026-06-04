package scan_test

import (
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestRunIncremental_PrepareStmtError covers RunIncremental's first failure
// guard: preparing the per-symbol statement fails on a closed index, so the
// run aborts with a wrapped error before any file is processed.
func TestRunIncremental_PrepareStmtError(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	_ = adapter.Close() // close so PrepareSymbolStmt fails

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	_, err = scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      io.Discard,
		Changed:       []string{"a.go"},
	})
	if err == nil {
		t.Fatal("expected error preparing symbol stmt on a closed index")
	}
}
