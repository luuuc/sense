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
func TestBlastQueryUsesCoveringIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "plan.db")

	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Open a read-only handle to the same DB for EXPLAIN — the
	// Adapter's public API doesn't expose EXPLAIN, and reaching into
	// the unexported db is brittle. modernc.org/sqlite registers the
	// "sqlite" driver, already loaded transitively via Open above.
	plan, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open for EXPLAIN: %v", err)
	}
	t.Cleanup(func() { _ = plan.Close() })

	const q = `EXPLAIN QUERY PLAN
	SELECT source_id FROM sense_edges WHERE target_id = ? AND kind = ?`

	rows, err := plan.QueryContext(ctx, q, 1, "calls")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	// EXPLAIN QUERY PLAN returns four columns: id, parent, notused,
	// detail. We care about detail — that's the human-readable plan
	// description ("SEARCH sense_edges USING COVERING INDEX ...").
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

	joined := strings.Join(plans, " | ")
	if !strings.Contains(joined, "USING COVERING INDEX idx_sense_edges_target") {
		t.Errorf("BFS query not covered by idx_sense_edges_target\nplan: %s", joined)
	}
}
