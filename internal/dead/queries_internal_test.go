package dead

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// memDB opens a fresh in-memory SQLite handle for the error-path and meta
// tests below.
func memDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func mustExec(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestQueryErrorPathsNoSchema drives the index-query helpers against a DB
// with no tables, exercising their error-return paths (the happy paths are
// covered by the scan-backed tests). Without this, a query that silently
// swallowed an error would still pass the integration tests.
func TestQueryErrorPathsNoSchema(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()

	if _, err := queryValueObjectClassIDs(ctx, db); err == nil {
		t.Error("queryValueObjectClassIDs should error when sense_edges is missing")
	}
	if _, err := countSymbols(ctx, db, Options{}); err == nil {
		t.Error("countSymbols should error without schema")
	}
	if _, err := queryCandidates(ctx, db, Options{}); err == nil {
		t.Error("queryCandidates should error without schema")
	}
	if _, err := queryCandidates(ctx, db, Options{ExcludeTestRefs: true, Language: "ruby", Domain: "x"}); err == nil {
		t.Error("queryCandidates (all filters) should error without schema")
	}
	if _, err := queryTestsTargets(ctx, db); err == nil {
		t.Error("queryTestsTargets should error without schema")
	}
	if _, err := queryControllerConcernModuleIDs(ctx, db); err == nil {
		t.Error("queryControllerConcernModuleIDs should error without schema")
	}
	if _, err := queryIncludedModuleIDs(ctx, db); err == nil {
		t.Error("queryIncludedModuleIDs should error without schema")
	}
	if _, err := hasMainFunction(ctx, db, Options{Language: "go", Domain: "cmd"}); err == nil {
		t.Error("hasMainFunction should error without schema")
	}
	if _, err := findLiveContainers(ctx, db, []Symbol{{ID: 1, Kind: "class"}}); err == nil {
		t.Error("findLiveContainers should error without schema when a container candidate exists")
	}
	if _, err := buildFacts(ctx, db); err == nil {
		t.Error("buildFacts should error without schema")
	}
	if _, err := FindDead(ctx, db, Options{}); err == nil {
		t.Error("FindDead should error without schema")
	}

	// populateFindingNameOccurrences fans out into accumulateCounts, so a
	// missing sense_symbols table surfaces through both.
	finding := Finding{Symbol: Symbol{Name: "x"}, Verdict: VerdictDead}
	if err := populateFindingNameOccurrences(ctx, db, []Finding{finding}); err == nil {
		t.Error("populateFindingNameOccurrences should error when sense_symbols is missing")
	}
	// Empty finding set is a fast no-op, not an error.
	if err := populateFindingNameOccurrences(ctx, db, nil); err != nil {
		t.Errorf("populateFindingNameOccurrences(nil) = %v, want nil", err)
	}
}

// TestFindLiveContainersNoContainers covers the early return when no candidate
// is a class/module — there is nothing to query, so a schema-less DB is fine.
func TestFindLiveContainersNoContainers(t *testing.T) {
	got, err := findLiveContainers(context.Background(), memDB(t), []Symbol{{ID: 1, Kind: "method"}})
	if err != nil {
		t.Fatalf("findLiveContainers (no containers) = %v, want nil error", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d live containers, want 0", len(got))
	}
}

// TestReadFrameworksMeta covers the missing-row, corrupt-JSON, and valid
// branches of readFrameworks against a real sense_meta table.
func TestReadFrameworksMeta(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)

	if got := readFrameworks(ctx, db); len(got) != 0 {
		t.Errorf("readFrameworks (missing) = %v, want empty", got)
	}
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('frameworks', '{bad')`)
	if got := readFrameworks(ctx, db); len(got) != 0 {
		t.Errorf("readFrameworks (corrupt) = %v, want empty", got)
	}
	mustExec(t, db, `UPDATE sense_meta SET value='["Rails","Sidekiq"]' WHERE key='frameworks'`)
	if _, ok := readFrameworks(ctx, db)["Rails"]; !ok {
		t.Error("readFrameworks (valid) should contain Rails")
	}
}

// TestReadDispatchNamesMeta covers the missing-row, empty-value, corrupt-JSON,
// and valid branches of readDispatchNames.
func TestReadDispatchNamesMeta(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)

	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames (missing) = %v, want empty", got)
	}
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('dispatch_names', '')`)
	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames (empty) = %v, want empty", got)
	}
	mustExec(t, db, `UPDATE sense_meta SET value='{bad' WHERE key='dispatch_names'`)
	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames (corrupt) = %v, want empty", got)
	}
	mustExec(t, db, `UPDATE sense_meta SET value='["process","perform"]' WHERE key='dispatch_names'`)
	if _, ok := readDispatchNames(ctx, db)["process"]; !ok {
		t.Error("readDispatchNames (valid) should contain process")
	}
}
