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

	tr.Record("sense.search", "auth", 5, 4000)
	tr.Record("sense.graph", "User", 3, 2400)

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

	tr.Record("sense.search", "auth", 5, 4000)
	tr.Record("sense.blast", "User", 10, 8000)
	tr.Record("sense.graph", "Order", 3, 2400)

	top := tr.TopQuery()
	if top == nil {
		t.Fatal("expected non-nil top query")
	}
	if top.Tool != "sense.blast" {
		t.Errorf("top tool = %q, want sense.blast", top.Tool)
	}
	if top.TokensSaved != 8000 {
		t.Errorf("top tokens_saved = %d, want 8000", top.TokensSaved)
	}
}

func TestTrackerLifetimeFlush(t *testing.T) {
	db := openTestDB(t)
	tr := NewTracker(db)

	tr.Record("sense.search", "auth", 5, 4000)
	tr.Record("sense.graph", "User", 3, 2400)
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

func TestTrackerLifetimeAccumulates(t *testing.T) {
	db := openTestDB(t)

	// First session
	tr1 := NewTracker(db)
	tr1.Record("sense.search", "auth", 5, 4000)
	tr1.Close()

	// Second session
	tr2 := NewTracker(db)
	tr2.Record("sense.graph", "User", 3, 2400)

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
