package scan_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestEmbedPending_SourceDeletedBeforeBackfill exercises the snippet-extension
// fallback in extendMethodSnippets: a function/method symbol whose source file
// was removed from disk after indexing but before the embedding backfill. The
// per-file readFileLines fails, the symbol keeps its single-line snippet, and
// EmbedPending still completes for every pending symbol rather than aborting.
func TestEmbedPending_SourceDeletedBeforeBackfill(t *testing.T) {
	useFakeEmbedder(t)

	root := t.TempDir()
	// Two files so one can be deleted while the other stays readable; both
	// contribute function symbols, so byFile is non-empty for both.
	writeFile(t, filepath.Join(root, "keep.go"), `package p

func Keep() error { return nil }
`)
	writeFile(t, filepath.Join(root, "gone.go"), `package p

func Gone() error { return nil }
`)

	ctx := context.Background()
	// Deferred scan: symbols indexed, embeddings pending.
	if _, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            os.Stderr,
		Warnings:          os.Stderr,
		EmbeddingsEnabled: true,
		Embed:             false,
	}); err != nil {
		t.Fatalf("deferred scan: %v", err)
	}

	// Remove one source file after indexing but before backfill.
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatalf("remove source: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	n, err := scan.EmbedPending(ctx, adapter, root)
	if err != nil {
		t.Fatalf("EmbedPending after source delete: %v", err)
	}
	if n == 0 {
		t.Error("expected EmbedPending to embed the surviving symbols")
	}

	// The kept file's symbol must still embed despite the deleted sibling.
	var have int
	if err := adapter.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sense_embeddings em
		JOIN sense_symbols s ON s.id = em.symbol_id
		WHERE s.name = 'Keep'`).Scan(&have); err != nil {
		t.Fatalf("query Keep embedding: %v", err)
	}
	if have == 0 {
		t.Error("expected Keep to be embedded even though gone.go was deleted")
	}
}

// TestEmbedPending_ExtendsMultiLineSnippet covers the body-line extension path
// in extendMethodSnippets: a multi-line function whose snippet is replaced with
// the first body lines read from source, including the end-of-file clamp when
// the function runs to the last line of the file.
func TestEmbedPending_ExtendsMultiLineSnippet(t *testing.T) {
	useFakeEmbedder(t)

	root := t.TempDir()
	// A function whose body spans several lines and reaches end-of-file, so
	// the read-and-join path runs and the end-of-slice clamp is taken.
	writeFile(t, filepath.Join(root, "calc.go"), `package p

func Compute(a, b int) int {
	x := a + b
	y := x * 2
	z := y - a
	return z
}`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            os.Stderr,
		Warnings:          os.Stderr,
		EmbeddingsEnabled: true,
		Embed:             false,
	}); err != nil {
		t.Fatalf("deferred scan: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	n, err := scan.EmbedPending(ctx, adapter, root)
	if err != nil {
		t.Fatalf("EmbedPending: %v", err)
	}
	if n == 0 {
		t.Fatal("expected the multi-line function to be embedded")
	}
}
