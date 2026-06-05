package benchmark

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

// TestBuildFixtureSmallSymbolCount drives the symsPerFile < 1 clamp: with a
// symbol budget smaller than the number of fixture files, the per-file
// division floors to zero and must be lifted to one so each file still gets a
// symbol.
func TestBuildFixtureSmallSymbolCount(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "small.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	fix, err := BuildFixture(ctx, adapter, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(fix.SymbolIDs) == 0 {
		t.Error("expected at least one symbol even with a budget of 1")
	}
}

// dropTable removes a schema table so the next write/read against it fails,
// letting the fixture's error-wrapping branches run.
func dropTable(t *testing.T, adapter *sqlite.Adapter, table string) {
	t.Helper()
	if _, err := adapter.DB().ExecContext(context.Background(), "DROP TABLE "+table); err != nil {
		t.Fatalf("drop %s: %v", table, err)
	}
}

func TestBuildFixtureWriteFileError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "nofiles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	dropTable(t, adapter, "sense_files")

	if _, err := BuildFixture(ctx, adapter, 100); err == nil {
		t.Fatal("expected error when sense_files is missing")
	}
}

func TestBuildFixtureWriteSymbolError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "nosymbols.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	dropTable(t, adapter, "sense_symbols")

	if _, err := BuildFixture(ctx, adapter, 100); err == nil {
		t.Fatal("expected error when sense_symbols is missing")
	}
}

func TestBuildFixtureWriteEdgeError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "noedges.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	dropTable(t, adapter, "sense_edges")

	if _, err := BuildFixture(ctx, adapter, 100); err == nil {
		t.Fatal("expected error when sense_edges is missing")
	}
}

func TestBuildFixtureWriteEmbeddingError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "noembeddings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	dropTable(t, adapter, "sense_embeddings")

	if _, err := BuildFixture(ctx, adapter, 100); err == nil {
		t.Fatal("expected error when sense_embeddings is missing")
	}
}
