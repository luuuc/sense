package metrics

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sense_metrics (key TEXT PRIMARY KEY, value INTEGER NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTrackerSessionCounters(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)
	defer tr.Close()

	tr.Record("sense_search", "auth", 5, 4000, false)
	tr.Record("sense_graph", "User", 3, 2400, false)

	s := tr.Session()
	if s.Queries != 2 {
		t.Errorf("queries = %d, want 2", s.Queries)
	}
	if s.FileReadsAvoided != 8 {
		t.Errorf("file_reads_avoided = %d, want 8", s.FileReadsAvoided)
	}
	if s.TokensSaved != 6400 {
		t.Errorf("tokens_saved = %d, want 6400", s.TokensSaved)
	}
}

func TestTrackerTopQuery(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)
	defer tr.Close()

	if tr.TopQuery() != nil {
		t.Error("expected nil top query before any records")
	}

	tr.Record("sense_search", "auth", 5, 4000, false)
	tr.Record("sense_blast", "User", 10, 8000, false)
	tr.Record("sense_graph", "Order", 3, 2400, false)

	top := tr.TopQuery()
	if top == nil {
		t.Fatal("expected non-nil top query")
		return
	}
	if top.Tool != "sense_blast" {
		t.Errorf("top tool = %q, want sense_blast", top.Tool)
	}
	if top.TokensSaved != 8000 {
		t.Errorf("top tokens_saved = %d, want 8000", top.TokensSaved)
	}
}

func TestTrackerLifetimeFlush(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)

	tr.Record("sense_search", "auth", 5, 4000, false)
	tr.Record("sense_graph", "User", 3, 2400, false)
	tr.Close()

	// Verify data was flushed to SQLite
	var val int
	err := db.QueryRow(`SELECT value FROM sense_metrics WHERE key = 'lifetime_queries'`).Scan(&val)
	if err != nil {
		t.Fatal(err)
	}
	if val != 2 {
		t.Errorf("persisted queries = %d, want 2", val)
	}

	err = db.QueryRow(`SELECT value FROM sense_metrics WHERE key = 'lifetime_tokens_saved'`).Scan(&val)
	if err != nil {
		t.Fatal(err)
	}
	if val != 6400 {
		t.Errorf("persisted tokens_saved = %d, want 6400", val)
	}
}

func TestTrackerTextFallbackFired(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)
	defer tr.Close()

	tr.Record("sense_search", "CASCADE REFERENCES", 2, 1600, true)
	tr.Record("sense_search", "handleSearch", 5, 4000, false)
	tr.Record("sense_search", "indexed_at", 1, 800, true)

	s := tr.Session()
	if s.TextFallbackFired != 2 {
		t.Errorf("session text_fallback_fired = %d, want 2", s.TextFallbackFired)
	}
	if s.Queries != 3 {
		t.Errorf("session queries = %d, want 3", s.Queries)
	}
}

func TestTrackerTextFallbackPersisted(t *testing.T) {
	db := openTestDB(t)

	tr1 := NewTracker(db)
	tr1.Record("sense_search", "query", 1, 800, true)
	tr1.Close()

	var val int
	err := db.QueryRow(`SELECT value FROM sense_metrics WHERE key = 'lifetime_text_fallback_fired'`).Scan(&val)
	if err != nil {
		t.Fatal(err)
	}
	if val != 1 {
		t.Errorf("persisted text_fallback_fired = %d, want 1", val)
	}

	tr2 := NewTracker(db)
	tr2.Record("sense_search", "query2", 2, 1600, true)
	lt := tr2.Lifetime(context.Background())
	if lt.TextFallbackFired != 2 {
		t.Errorf("lifetime text_fallback_fired = %d, want 2", lt.TextFallbackFired)
	}
	tr2.Close()
}

func TestTrackerLifetimeAccumulates(t *testing.T) {
	db := openTestDB(t)

	// First session
	tr1 := NewTracker(db)
	tr1.Record("sense_search", "auth", 5, 4000, false)
	tr1.Close()

	// Second session
	tr2 := NewTracker(db)
	tr2.Record("sense_graph", "User", 3, 2400, false)

	ctx := context.Background()
	lt := tr2.Lifetime(ctx)
	if lt.Queries != 2 {
		t.Errorf("lifetime queries = %d, want 2", lt.Queries)
	}
	if lt.TokensSaved != 6400 {
		t.Errorf("lifetime tokens_saved = %d, want 6400", lt.TokensSaved)
	}
	tr2.Close()
}

// Write-through: a recorded query is persisted to SQLite immediately, without
// waiting for the 30s ticker or a graceful Close — so an abrupt restart can't
// drop it.
func TestTrackerWriteThroughPersistsWithoutClose(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)
	defer tr.Close()

	tr.Record("sense_search", "auth", 5, 4000, false)

	var val int
	if err := db.QueryRow(`SELECT value FROM sense_metrics WHERE key = 'lifetime_queries'`).Scan(&val); err != nil {
		t.Fatalf("expected lifetime_queries persisted without Close: %v", err)
	}
	if val != 1 {
		t.Errorf("persisted queries = %d, want 1 (write-through)", val)
	}
}

// A flush that fails (here: the table is missing) must not drop the delta — it
// is returned to the buffer and persisted by a later flush once the DB recovers.
func TestTrackerFlushFailureRetainsDelta(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)

	// Break the write path so the write-through flush in Record fails.
	if _, err := db.Exec(`DROP TABLE sense_metrics`); err != nil {
		t.Fatal(err)
	}
	tr.Record("sense_search", "auth", 5, 4000, false)

	// Recover the DB and flush again — the retained delta must land now.
	if _, err := db.Exec(`CREATE TABLE sense_metrics (key TEXT PRIMARY KEY, value INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	tr.Close()

	var val int
	if err := db.QueryRow(`SELECT value FROM sense_metrics WHERE key = 'lifetime_queries'`).Scan(&val); err != nil {
		t.Fatalf("retained delta should persist after recovery: %v", err)
	}
	if val != 1 {
		t.Errorf("persisted queries = %d, want 1 (delta retained across failed flush)", val)
	}
}
