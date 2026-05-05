package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

// TestBlastQueryUsesCoveringIndex pins the covering-index guarantee
// that pitch 01-03 relies on for BFS performance. Without source_id
// in idx_sense_edges_target, SQLite's planner reports `USING INDEX`
// and then fetches each matching row to read source_id. With the
// covering form (target_id, kind, source_id) the planner reports
// `USING COVERING INDEX`, which is a measurable difference at the
// pitch's 30K-symbol scale.
//
// The test opens a fresh adapter (schema is applied at Open time)
// and issues an EXPLAIN QUERY PLAN against the canonical BFS shape:
// "given a target_id, find source_ids for kind='calls'." The
// surrounding Card-14 benchmark (BenchmarkBlast) closes the loop on
// wall-clock impact; this test is the structural gate that keeps
// the planner honest if someone later edits schema.sql and
// inadvertently removes source_id from the index.
func openPlanDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "plan.db")

	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	plan, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open for EXPLAIN: %v", err)
	}
	t.Cleanup(func() { _ = plan.Close() })
	return plan
}

func explainPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var plans []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		plans = append(plans, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("EXPLAIN QUERY PLAN returned no rows")
	}
	return strings.Join(plans, " | ")
}

func TestBlastQueryUsesCoveringIndex(t *testing.T) {
	plan := openPlanDB(t)
	joined := explainPlan(t, plan,
		`SELECT source_id FROM sense_edges WHERE target_id = ? AND kind = ?`, 1, "calls")

	if !strings.Contains(joined, "USING COVERING INDEX idx_sense_edges_target") {
		t.Errorf("BFS query not covered by idx_sense_edges_target\nplan: %s", joined)
	}
}

func TestSearchQueryUsesFTS5(t *testing.T) {
	plan := openPlanDB(t)
	joined := explainPlan(t, plan,
		`SELECT s.id, s.name, s.qualified, s.kind, s.file_id, s.line_start, s.snippet, -rank AS score
		 FROM sense_symbols_fts
		 JOIN sense_symbols s ON s.id = sense_symbols_fts.rowid
		 WHERE sense_symbols_fts MATCH ?
		 ORDER BY rank LIMIT ?`, "test", 10)

	if !strings.Contains(joined, "VIRTUAL TABLE") {
		t.Errorf("search query should use FTS5 virtual table\nplan: %s", joined)
	}
}

func TestSymbolLookupUsesQualifiedIndex(t *testing.T) {
	plan := openPlanDB(t)
	joined := explainPlan(t, plan,
		`SELECT id, file_id, name, kind, line_start, line_end FROM sense_symbols WHERE qualified = ?`, "pkg.Foo")

	if !strings.Contains(joined, "idx_sense_symbols_qualified") {
		t.Errorf("qualified lookup should use idx_sense_symbols_qualified\nplan: %s", joined)
	}
}

func TestEdgeSourceQueryUsesIndex(t *testing.T) {
	plan := openPlanDB(t)
	joined := explainPlan(t, plan,
		`SELECT target_id FROM sense_edges WHERE source_id = ? AND kind = ?`, 1, "calls")

	if !strings.Contains(joined, "USING INDEX") && !strings.Contains(joined, "USING COVERING INDEX") {
		t.Errorf("edge source query should use an index\nplan: %s", joined)
	}
}
