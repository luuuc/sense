package profile

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openDBWithSchema opens a fresh sqlite DB and applies the given schema (which
// may omit tables on purpose to exercise query-failure paths).
func openDBWithSchema(t *testing.T, schema string) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if schema != "" {
		if _, err := db.Exec(schema); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestComputeCountSymbolsError exercises the first query-error path: the
// sense_symbols table does not exist.
func TestComputeCountSymbolsError(t *testing.T) {
	db := openDBWithSchema(t, "")
	_, err := Compute(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when sense_symbols is missing")
	}
	if !strings.Contains(err.Error(), "count symbols") {
		t.Errorf("error = %q, want it to mention count symbols", err)
	}
}

// TestComputeCountEdgesError exercises the second query-error path: symbols
// exist but sense_edges does not.
func TestComputeCountEdgesError(t *testing.T) {
	db := openDBWithSchema(t, `
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT);
	`)
	_, err := Compute(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when sense_edges is missing")
	}
	if !strings.Contains(err.Error(), "count edges") {
		t.Errorf("error = %q, want it to mention count edges", err)
	}
}

// TestComputeLanguageBreakdownError exercises the language-breakdown wrap in
// Compute (and the QueryContext-error path in queryLangSymbolCounts) by leaving
// sense_files out so the JOIN fails.
func TestComputeLanguageBreakdownError(t *testing.T) {
	db := openDBWithSchema(t, `
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT);
		CREATE TABLE sense_edges (id INTEGER PRIMARY KEY, source_id INTEGER, target_id INTEGER, kind TEXT);
	`)
	_, err := Compute(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when sense_files is missing")
	}
	if !strings.Contains(err.Error(), "language breakdown") {
		t.Errorf("error = %q, want it to mention language breakdown", err)
	}
}

// TestQueryLangSymbolCountsQueryError drives the QueryContext failure path
// directly: no sense_files table for the JOIN.
func TestQueryLangSymbolCountsQueryError(t *testing.T) {
	db := openDBWithSchema(t, `
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT);
	`)
	_, err := queryLangSymbolCounts(context.Background(), db)
	if err == nil {
		t.Fatal("expected error when sense_files is missing")
	}
}

// TestQueryLangSymbolCountsScanError drives the Scan failure path: a row is
// returned but f.language is NULL, which cannot be scanned into a string.
func TestQueryLangSymbolCountsScanError(t *testing.T) {
	db := openDBWithSchema(t, `
		CREATE TABLE sense_files (id INTEGER PRIMARY KEY, language TEXT);
		CREATE TABLE sense_symbols (id INTEGER PRIMARY KEY, file_id INTEGER, name TEXT);
	`)
	ctx := context.Background()
	// language is left NULL; a matching symbol forces one grouped row.
	if _, err := db.ExecContext(ctx, `INSERT INTO sense_files (id, language) VALUES (1, NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO sense_symbols (id, file_id, name) VALUES (1, 1, 'x')`); err != nil {
		t.Fatal(err)
	}
	_, err := queryLangSymbolCounts(ctx, db)
	if err == nil {
		t.Fatal("expected scan error on NULL language")
	}
}

// TestStoreExecError exercises the Store insert-error path: sense_meta is
// missing so the very first ExecContext fails.
func TestStoreExecError(t *testing.T) {
	db := openDBWithSchema(t, "")
	err := Store(context.Background(), db, &Profile{Tier: TierSmall})
	if err == nil {
		t.Fatal("expected error when sense_meta is missing")
	}
	if !strings.Contains(err.Error(), "store profile_tier") {
		t.Errorf("error = %q, want it to mention store profile_tier", err)
	}
}

// TestReadMetaIntEmpty covers the empty-value branch of readMetaInt (key absent
// from sense_meta yields 0).
func TestReadMetaIntEmpty(t *testing.T) {
	db := openTestDB(t)
	if got := readMetaInt(context.Background(), db, "missing_key"); got != 0 {
		t.Errorf("readMetaInt(missing) = %d, want 0", got)
	}
}
