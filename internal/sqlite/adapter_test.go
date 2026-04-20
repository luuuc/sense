package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/index/indextest"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
	_ "modernc.org/sqlite"
)

func TestAdapterConformance(t *testing.T) {
	indextest.RunConformance(t, func(t *testing.T) index.Index {
		t.Helper()
		path := filepath.Join(t.TempDir(), "index.db")
		a, err := sqlite.Open(context.Background(), path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return a
	})
}

func TestOpenFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("MissingParentDirectory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "does-not-exist", "index.db")
		if _, err := sqlite.Open(ctx, path); err == nil {
			t.Fatal("Open with missing parent directory should error, got nil")
		}
	})

	t.Run("PathIsExistingDirectory", func(t *testing.T) {
		// The tempdir itself is a directory; opening it as a DB file must fail.
		if _, err := sqlite.Open(ctx, t.TempDir()); err == nil {
			t.Fatal("Open with path pointing at a directory should error, got nil")
		}
	})
}

func TestSchemaVersionMismatchRebuilds(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	// Create a DB with an older schema version and some data.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.ExecContext(ctx, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, path TEXT UNIQUE, language TEXT, hash TEXT, symbols INTEGER, indexed_at TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT, qualified TEXT, kind TEXT, visibility TEXT, parent_id INTEGER, line_start INTEGER, line_end INTEGER, docstring TEXT, complexity INTEGER, snippet TEXT, UNIQUE(file_id, qualified));
		INSERT INTO sense_files VALUES (1, 'old.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z');
	`)
	// Stamp an old version that differs from current SchemaVersion.
	_, _ = db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion-1))
	_ = db.Close()

	// Open via sqlite.Open — should detect mismatch and rebuild.
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if !a.Rebuilt {
		t.Fatal("expected Rebuilt=true after schema version mismatch")
	}

	// Old data should be gone (tables were dropped and recreated empty).
	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected 0 files after rebuild, got %d", len(paths))
	}

	// PRAGMA user_version should be 0 (not stamped until scan completes).
	var ver int
	_ = a.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver)
	if ver != 0 {
		t.Errorf("user_version after rebuild = %d, want 0", ver)
	}

	// StampSchemaVersion should set it correctly.
	if err := a.StampSchemaVersion(ctx); err != nil {
		t.Fatalf("StampSchemaVersion: %v", err)
	}
	_ = a.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&ver)
	if ver != sqlite.SchemaVersion {
		t.Errorf("user_version after stamp = %d, want %d", ver, sqlite.SchemaVersion)
	}
}

func TestFreshDBNotRebuilt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	if a.Rebuilt {
		t.Fatal("fresh DB should not have Rebuilt=true")
	}
}

func TestWriteReadMetaRoundTrip(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	// Missing key returns empty string.
	val, err := a.ReadMeta(ctx, "no_such_key")
	if err != nil {
		t.Fatalf("ReadMeta missing: %v", err)
	}
	if val != "" {
		t.Errorf("ReadMeta missing = %q, want empty", val)
	}

	// Write and read back.
	if err := a.WriteMeta(ctx, "embedding_model", "all-MiniLM-L6-v2"); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}
	val, err = a.ReadMeta(ctx, "embedding_model")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if val != "all-MiniLM-L6-v2" {
		t.Errorf("ReadMeta = %q, want all-MiniLM-L6-v2", val)
	}

	// Upsert overwrites.
	if err := a.WriteMeta(ctx, "embedding_model", "new-model-v2"); err != nil {
		t.Fatalf("WriteMeta upsert: %v", err)
	}
	val, err = a.ReadMeta(ctx, "embedding_model")
	if err != nil {
		t.Fatalf("ReadMeta after upsert: %v", err)
	}
	if val != "new-model-v2" {
		t.Errorf("ReadMeta after upsert = %q, want new-model-v2", val)
	}
}

func TestReopenPreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	first, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f := &model.File{
		Path:      "app/models/persist.rb",
		Language:  "ruby",
		Hash:      "persistence",
		IndexedAt: time.Unix(1_700_000_000, 0).UTC(),
	}
	fileID, err := first.WriteFile(ctx, f)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	symID, err := first.WriteSymbol(ctx, &model.Symbol{
		FileID:    fileID,
		Name:      "PersistMe",
		Qualified: "App::PersistMe",
		Kind:      model.KindClass,
		LineStart: 1,
		LineEnd:   10,
	})
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("second Close: %v", err)
		}
	})

	got, err := second.ReadSymbol(ctx, symID)
	if err != nil {
		t.Fatalf("ReadSymbol after reopen: %v", err)
	}
	if got.Symbol.Qualified != "App::PersistMe" {
		t.Errorf("Qualified = %q, want App::PersistMe", got.Symbol.Qualified)
	}
	if got.File.Path != f.Path {
		t.Errorf("File.Path = %q, want %q", got.File.Path, f.Path)
	}
	if !got.File.IndexedAt.Equal(f.IndexedAt) {
		t.Errorf("File.IndexedAt = %v, want %v", got.File.IndexedAt, f.IndexedAt)
	}
}
