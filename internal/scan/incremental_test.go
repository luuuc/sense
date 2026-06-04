package scan_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestRunIncrementalUpdatesChangedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")
	writeFile(t, filepath.Join(root, "b.go"), "package a\n\nfunc World() {}\n")

	ctx := context.Background()

	// Initial full scan
	first, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}
	if first.Symbols == 0 {
		t.Fatal("no symbols after initial scan")
	}

	// Modify a.go to add a new function
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n")

	// Open adapter for incremental use
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &bytes.Buffer{},
		Changed:       []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("Changed = %d, want 1", res.Changed)
	}

	// Verify the new symbol exists in the index
	var count int
	err = adapter.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_symbols WHERE name = 'Goodbye'`).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Errorf("Goodbye symbol count = %d, want 1", count)
	}
}

func TestRunIncrementalRemovesDeletedFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Keep() {}\n")
	writeFile(t, filepath.Join(root, "b.go"), "package a\n\nfunc Remove() {}\n")

	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Delete b.go from disk
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &bytes.Buffer{},
		Removed:       []string{"b.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Removed != 1 {
		t.Errorf("Removed = %d, want 1", res.Removed)
	}

	// Verify b.go's symbols are gone
	var count int
	err = adapter.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_symbols WHERE name = 'Remove'`).Scan(&count)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Errorf("Remove symbol count = %d, want 0", count)
	}
}

func TestRunIncrementalSkipsUnchangedHash(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Stable() {}\n")

	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Don't modify the file — incremental should skip it
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &bytes.Buffer{},
		Changed:       []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if res.Changed != 0 {
		t.Errorf("Changed = %d, want 0", res.Changed)
	}
}

func TestRunIncrementalNilDefaults(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")

	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Modify a.go
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n")

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	// Call with nil Output, Warnings, and Parsers — should use defaults
	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Changed:       []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("Changed = %d, want 1", res.Changed)
	}
}

// TestRunIncrementalEmbeddingsEnabledNoChanges enters the EmbeddingsEnabled
// branch with an unchanged file (same hash → no changedFileIDs), proving
// that embedSymbols short-circuits cleanly and the flag-gated code path is
// exercised without spinning up ONNX.
func TestRunIncrementalEmbeddingsEnabledNoChanges(t *testing.T) {
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
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:              root,
		Idx:               adapter,
		Matcher:           matcher,
		MaxFileSizeKB:     512,
		EmbeddingsEnabled: true,
		Output:            io.Discard,
		Warnings:          &bytes.Buffer{},
		Changed:           []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Embedded != 0 {
		t.Errorf("Embedded = %d, want 0 (no changedFileIDs)", res.Embedded)
	}
}

// Suppress unused import warnings — sql is used via adapter.DB().
var _ = sql.ErrNoRows

func TestRunIncremental_ChangedFileDeletedFromDisk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Keep() {}\n")
	writeFile(t, filepath.Join(root, "b.go"), "package a\n\nfunc Gone() {}\n")

	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Delete b.go from disk but report it as changed (simulating a race
	// where the watcher saw a modify event but the file was then deleted).
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	var warnBuf bytes.Buffer
	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &warnBuf,
		Changed:       []string{"b.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	// File was deleted — should produce a warning, not a crash.
	if res.Warnings == 0 {
		t.Error("expected warning for file that was deleted between event and parse")
	}
	if res.Changed != 0 {
		t.Errorf("Changed = %d, want 0 (file was unreadable)", res.Changed)
	}
}

func TestRunIncremental_NilParsersCreatesTemporary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\nfunc World() {}\n")

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	// Parsers: nil — should create a temporary ParserCache internally.
	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      io.Discard,
		Parsers:       nil,
		Changed:       []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("Changed = %d, want 1", res.Changed)
	}
}
