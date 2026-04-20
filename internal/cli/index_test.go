package cli

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
	_ "modernc.org/sqlite"
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
