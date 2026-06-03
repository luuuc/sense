package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestOpenIndexRebuiltOnSchemaMismatch(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(senseDir, "index.db")

	// Create a DB with an old schema version.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
		CREATE TABLE sense_embeddings (symbol_id INTEGER PRIMARY KEY, vector BLOB);
		INSERT INTO sense_files VALUES (1, 'old.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z');
	`)
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion-1))
	_ = db.Close()

	adapter, err := OpenIndex(ctx, dir)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	if !adapter.Rebuilt {
		t.Fatal("expected Rebuilt=true when schema version mismatches")
	}

	// Old data should be gone.
	paths, err := adapter.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 files after rebuild, got %d", len(paths))
	}
}

func TestOpenIndexNotRebuiltWhenCurrent(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(senseDir, "index.db")

	// Create a DB with the current schema version.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT, file_id INTEGER, line INTEGER, confidence REAL);
		CREATE TABLE sense_embeddings (symbol_id INTEGER PRIMARY KEY, vector BLOB);
		CREATE TABLE sense_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		INSERT INTO sense_files VALUES (1, 'current.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z');
	`)
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion))
	_ = db.Close()

	adapter, err := OpenIndex(ctx, dir)
	if err != nil {
		t.Fatalf("OpenIndex: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	if adapter.Rebuilt {
		t.Fatal("expected Rebuilt=false when schema version matches")
	}

	// Data should still be there.
	paths, err := adapter.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 1 {
		t.Errorf("expected 1 file preserved, got %d", len(paths))
	}
}

func TestLoadFilePaths(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(ctx, `CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT)`); err != nil {
		t.Fatal(err)
	}

	// Empty input short-circuits to an empty map without querying.
	if got, err := LoadFilePaths(ctx, db, nil); err != nil || len(got) != 0 {
		t.Fatalf("LoadFilePaths(nil) = %v, %v; want empty map, nil", got, err)
	}

	// Seed more than one chunk (chunk size is 500) so the batching loop runs
	// for multiple iterations.
	const n = 600
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		id := int64(i + 1)
		ids[i] = id
		if _, err := db.ExecContext(ctx, `INSERT INTO sense_files (id, path) VALUES (?, ?)`, id, fmt.Sprintf("app/f%d.rb", id)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadFilePaths(ctx, db, ids)
	if err != nil {
		t.Fatalf("LoadFilePaths: %v", err)
	}
	if len(got) != n {
		t.Fatalf("got %d paths, want %d (multi-chunk)", len(got), n)
	}
	if got[1] != "app/f1.rb" || got[600] != "app/f600.rb" {
		t.Errorf("path mismatch: [1]=%q [600]=%q", got[1], got[600])
	}
}

func TestLoadFilePathsQueryError(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// No sense_files table — the query fails and the error must propagate.
	if _, err := LoadFilePaths(ctx, db, []int64{1, 2}); err == nil {
		t.Fatal("expected an error when sense_files is missing")
	}
}

func TestOpenIndexMissing(t *testing.T) {
	// A directory with no .sense/index.db returns ErrIndexMissing.
	_, err := OpenIndex(context.Background(), t.TempDir())
	if !errors.Is(err, ErrIndexMissing) {
		t.Fatalf("OpenIndex on empty dir = %v, want ErrIndexMissing", err)
	}
	// Empty dir defaults to "." — also missing an index here.
	if _, err := OpenIndex(context.Background(), ""); !errors.Is(err, ErrIndexMissing) {
		t.Fatalf("OpenIndex(\"\") = %v, want ErrIndexMissing", err)
	}
}
