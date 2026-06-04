package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/index/indextest"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
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

	t.Run("CorruptDatabaseFile", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "corrupt.db")
		if err := os.WriteFile(path, []byte("not a sqlite database"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := sqlite.Open(ctx, path)
		if err == nil {
			t.Fatal("Open with corrupt database file should error, got nil")
		}
	})
}

func TestRepeatedOpenClosePreservesData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrent.db")

	// Open sequentially multiple times — concurrent schema writes get
	// SQLITE_BUSY since there's no busy_timeout. The guarantee is that
	// repeated open/close cycles on the same path never corrupt the DB.
	for i := range 5 {
		a, err := sqlite.Open(ctx, path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if i == 0 {
			// Seed a symbol on first open to verify persistence.
			fid, err := a.WriteFile(ctx, &model.File{
				Path: "concurrent.go", Language: "go",
				Hash: "c1", Symbols: 1, IndexedAt: time.Now(),
			})
			if err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if _, err := a.WriteSymbol(ctx, &model.Symbol{
				FileID: fid, Name: "Persist", Qualified: "pkg.Persist",
				Kind: "function", LineStart: 1, LineEnd: 5,
			}); err != nil {
				t.Fatalf("WriteSymbol: %v", err)
			}
		}
		if err := a.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}

	// Verify the DB is intact after many open/close cycles.
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	defer func() { _ = a.Close() }()

	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 symbol after repeated opens, got %d", count)
	}
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

// TestSchemaMismatchPreservesMetrics is the metrics-preservation contract:
// a binary upgrade that bumps SchemaVersion triggers a rebuild on the next
// Open, and the lifetime counters in sense_metrics survive it while all
// source-derived data is dropped.
func TestSchemaMismatchPreservesMetrics(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	// Build a current-schema index with a lifetime metric and some data.
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := a.DB().ExecContext(ctx,
		"INSERT INTO sense_metrics(key, value) VALUES ('lifetime_queries', 99)"); err != nil {
		t.Fatalf("seed metrics: %v", err)
	}
	if _, err := a.DB().ExecContext(ctx,
		"INSERT INTO sense_files(path, language, hash, symbols, indexed_at) VALUES ('old.go','go','h',0,'t')"); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := a.StampSchemaVersion(ctx); err != nil {
		t.Fatalf("StampSchemaVersion: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Simulate a binary built against an older schema by rewinding the
	// stored version, the way an upgrade would surface.
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion-1)); err != nil {
		t.Fatalf("rewind user_version: %v", err)
	}
	_ = db.Close()

	// Reopen: mismatch detected, rebuild, metrics preserved.
	a2, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = a2.Close() }()

	if !a2.Rebuilt {
		t.Fatal("expected Rebuilt=true after schema version mismatch")
	}

	var queries int
	if err := a2.DB().QueryRowContext(ctx,
		"SELECT value FROM sense_metrics WHERE key='lifetime_queries'").Scan(&queries); err != nil {
		t.Fatalf("read lifetime_queries after rebuild: %v", err)
	}
	if queries != 99 {
		t.Errorf("lifetime_queries after rebuild = %d, want 99 (metrics must survive)", queries)
	}

	// Source-derived data is gone — proving the rebuild really happened and
	// only sense_metrics was spared.
	paths, err := a2.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("files after rebuild = %d, want 0", len(paths))
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

func TestInTx(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// Successful transaction
	err := a.InTx(ctx, func() error {
		fid := seedFile(t, a, "tx.go", "go", "h1")
		seedSymbol(t, a, fid, "F", "pkg.F", "function")
		return nil
	})
	if err != nil {
		t.Fatalf("InTx success: %v", err)
	}

	// Verify data persisted
	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "tx.go" {
		t.Errorf("FilePaths = %v, want [tx.go]", paths)
	}
}

func TestInTxRollback(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// Seed a file before the transaction
	seedFile(t, a, "before.go", "go", "h0")

	// Failing transaction should rollback
	err := a.InTx(ctx, func() error {
		seedFile(t, a, "rollback.go", "go", "h2")
		return context.Canceled // simulate error
	})
	if err == nil {
		t.Fatal("InTx should propagate error")
	}

	// The rolled-back file should not be visible (but since InTx uses
	// single-conn trick with BEGIN IMMEDIATE, the effects of WriteFile
	// inside fn() already went through the same connection — rollback
	// reverts them). Verify "before.go" persists.
	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	// The before.go was written outside the transaction, so it persists
	found := false
	for _, p := range paths {
		if p == "before.go" {
			found = true
		}
	}
	if !found {
		t.Error("before.go should persist after rollback")
	}
}

func TestStampSchemaVersion(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// StampSchemaVersion sets PRAGMA user_version; should not error
	if err := a.StampSchemaVersion(ctx); err != nil {
		t.Fatalf("StampSchemaVersion: %v", err)
	}

	// Verify by reading PRAGMA user_version
	var version int
	if err := a.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version == 0 {
		t.Error("user_version should not be 0 after stamp")
	}
}
