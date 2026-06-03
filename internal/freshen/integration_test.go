package freshen

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestIntegrationWatchModeReindex(t *testing.T) {
	root := t.TempDir()

	// Write initial Go file with one function.
	initialContent := "package main\n\nfunc Hello() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(initialContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Initial full scan to populate the index.
	_, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Open adapter for incremental writes.
	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	// Build ignore matcher.
	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	// Create watcher.
	w, err := New(root, matcher)
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	defer func() { _ = w.Close() }()

	// Start debounce loop.
	batches := Loop(ctx, w, 100)

	// Parser cache for incremental scans.
	parsers := scan.NewParserCache()
	defer parsers.Close()

	// Modify the file to add a new function.
	updatedContent := "package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(updatedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for the batch, then process it.
	select {
	case batch := <-batches:
		if len(batch.Changed) == 0 {
			t.Fatal("expected changed files in batch")
		}

		pOpts := processOptions{
			root:           root,
			matcher:        matcher,
			maxFileSizeKB:  512,
			parsers:        parsers,
			idx:            adapter,
			log:            func(_ string, _ ...any) {},
			runIncremental: scan.RunIncremental,
			cancelEmbed:    func() {},
			startEmbed:     func() {},
		}

		if err := processBatch(ctx, batch, pOpts); err != nil {
			t.Fatalf("processBatch: %v", err)
		}

		// Verify the new symbol exists in the index.
		var count int
		err = adapter.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sense_symbols WHERE name = 'Goodbye'`).Scan(&count)
		if err != nil {
			t.Fatalf("query: %v", err)
		}
		if count != 1 {
			t.Errorf("Goodbye symbol count = %d, want 1", count)
		}

	case <-ctx.Done():
		t.Fatal("timed out waiting for batch")
	}
}
