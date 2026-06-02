package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// openForReset returns a fresh adapter whose schema is fully applied, so a
// reset can be exercised against a populated, current-shape database.
func openForReset(t *testing.T) *Adapter {
	t.Helper()
	ctx := context.Background()
	a, err := Open(ctx, filepath.Join(t.TempDir(), "reset.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func tableCount(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func objectExists(t *testing.T, db *sql.DB, typ, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type=? AND name=?", typ, name,
	).Scan(&n); err != nil {
		t.Fatalf("sqlite_master lookup %s %s: %v", typ, name, err)
	}
	return n > 0
}

// seedReset populates every derived table plus a sense_metrics counter so a
// reset has something to drop and something to preserve.
func seedReset(t *testing.T, a *Adapter) {
	t.Helper()
	ctx := context.Background()
	fid := seedFileInternal(t, a)
	sid := seedSymbolInternal(t, a, fid)
	if _, err := a.db.ExecContext(ctx,
		"INSERT INTO sense_edges(source_id, target_id, kind, file_id, line, confidence) VALUES (?,?,?,?,?,?)",
		sid, sid, "calls", fid, 1, 1.0); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	if _, err := a.db.ExecContext(ctx,
		"INSERT INTO sense_meta(key, value) VALUES ('embedding_model','test-model')"); err != nil {
		t.Fatalf("seed meta: %v", err)
	}
	if _, err := a.db.ExecContext(ctx,
		"INSERT INTO sense_metrics(key, value) VALUES ('lifetime_queries', 42), ('lifetime_tokens_saved', 1000)"); err != nil {
		t.Fatalf("seed metrics: %v", err)
	}
}

func seedFileInternal(t *testing.T, a *Adapter) int64 {
	t.Helper()
	res, err := a.db.Exec(
		"INSERT INTO sense_files(path, language, hash, symbols, indexed_at) VALUES ('a.go','go','h1',1,0)")
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedSymbolInternal(t *testing.T, a *Adapter, fid int64) int64 {
	t.Helper()
	res, err := a.db.Exec(
		"INSERT INTO sense_symbols(file_id, name, qualified, kind, line_start, line_end) VALUES (?,?,?,?,?,?)",
		fid, "A", "pkg.A", "class", 1, 10)
	if err != nil {
		t.Fatalf("seed symbol: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestResetPreservesMetrics is the core primitive contract: with
// preserve={sense_metrics}, the lifetime counters survive while every other
// sense_% table is dropped and recreated empty at the current schema.
func TestResetPreservesMetrics(t *testing.T) {
	ctx := context.Background()
	a := openForReset(t)
	seedReset(t, a)

	if err := resetSenseTables(ctx, a.db, map[string]bool{"sense_metrics": true}); err != nil {
		t.Fatalf("resetSenseTables: %v", err)
	}

	// sense_metrics rows survive untouched.
	if got := tableCount(t, a.db, "sense_metrics"); got != 2 {
		t.Errorf("sense_metrics rows after reset = %d, want 2", got)
	}
	var queries int
	if err := a.db.QueryRow(
		"SELECT value FROM sense_metrics WHERE key='lifetime_queries'").Scan(&queries); err != nil {
		t.Fatalf("read lifetime_queries: %v", err)
	}
	if queries != 42 {
		t.Errorf("lifetime_queries after reset = %d, want 42", queries)
	}

	// Every derived table exists and is empty.
	for _, tbl := range []string{"sense_files", "sense_symbols", "sense_edges", "sense_embeddings", "sense_meta"} {
		if !objectExists(t, a.db, "table", tbl) {
			t.Errorf("table %s missing after reset", tbl)
			continue
		}
		if got := tableCount(t, a.db, tbl); got != 0 {
			t.Errorf("%s rows after reset = %d, want 0", tbl, got)
		}
	}

	// FTS virtual table and its triggers are re-applied.
	if !objectExists(t, a.db, "table", "sense_symbols_fts") {
		t.Error("sense_symbols_fts missing after reset")
	}
	for _, trig := range []string{
		"sense_symbols_fts_insert", "sense_symbols_fts_delete",
		"sense_symbols_fts_update", "sense_symbols_fts_update_after",
	} {
		if !objectExists(t, a.db, "trigger", trig) {
			t.Errorf("FTS trigger %s missing after reset", trig)
		}
	}

	// user_version is reset to 0; StampSchemaVersion sets it after scan.
	var uv int
	if err := a.db.QueryRow("PRAGMA user_version").Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != 0 {
		t.Errorf("user_version after reset = %d, want 0", uv)
	}
}

// TestResetNilPreserveDropsMetrics confirms the preserve-set is the only thing
// that spares a table: with no preserve-set, sense_metrics is dropped like
// everything else. This is the behavior Open's schema-mismatch branch had
// before the metrics-preserving change.
func TestResetNilPreserveDropsMetrics(t *testing.T) {
	ctx := context.Background()
	a := openForReset(t)
	seedReset(t, a)

	if err := resetSenseTables(ctx, a.db, nil); err != nil {
		t.Fatalf("resetSenseTables: %v", err)
	}

	if got := tableCount(t, a.db, "sense_metrics"); got != 0 {
		t.Errorf("sense_metrics rows after nil-preserve reset = %d, want 0", got)
	}
}
