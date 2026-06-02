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
	// The per-language meta readers swallow a query error (here, the missing
	// sense_meta table) and return an empty map rather than crashing — a missing
	// harvest signal fails closed, never fatal.
	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames without schema = %v, want empty", got)
	}
	if got := readMentionedNames(ctx, db); len(got) != 0 {
		t.Errorf("readMentionedNames without schema = %v, want empty", got)
	}
	if got := readHarvestedLangs(ctx, db); len(got) != 0 {
		t.Errorf("readHarvestedLangs without schema = %v, want empty", got)
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

// TestReadHarvestedLangsMeta covers the missing-row, corrupt-JSON, and valid
// branches of readHarvestedLangs against a real sense_meta table.
func TestReadHarvestedLangsMeta(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)

	if got := readHarvestedLangs(ctx, db); len(got) != 0 {
		t.Errorf("readHarvestedLangs (missing) = %v, want empty", got)
	}
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('harvested_langs', '{bad')`)
	if got := readHarvestedLangs(ctx, db); len(got) != 0 {
		t.Errorf("readHarvestedLangs (corrupt) = %v, want empty", got)
	}
	mustExec(t, db, `UPDATE sense_meta SET value='["ruby","go"]' WHERE key='harvested_langs'`)
	got := readHarvestedLangs(ctx, db)
	if _, ok := got["ruby"]; !ok {
		t.Error("readHarvestedLangs (valid) should contain ruby")
	}
	if _, ok := got["go"]; !ok {
		t.Error("readHarvestedLangs (valid) should contain go")
	}
}

// TestReadDispatchNamesMeta covers the per-language reader's branches: missing,
// a legacy union key (ignored), a corrupt per-language value (self-heals to an
// empty set), and a valid per-language value parsed under its language.
func TestReadDispatchNamesMeta(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)

	// Missing → empty.
	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames (missing) = %v, want empty", got)
	}
	// A legacy union key (no language suffix) does not match the per-language
	// GLOB, so it is ignored — a pre-feature index harvests no languages.
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('dispatch_names', '["legacy"]')`)
	if got := readDispatchNames(ctx, db); len(got) != 0 {
		t.Errorf("readDispatchNames (legacy union key) = %v, want empty (ignored)", got)
	}
	// Corrupt per-language value → that language is present with an empty set.
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('dispatch_names:ruby', '{bad')`)
	if got := readDispatchNames(ctx, db); len(got) != 1 || got["ruby"] == nil || len(got["ruby"]) != 0 {
		t.Errorf("readDispatchNames (corrupt ruby) = %v, want ruby present-but-empty", got)
	}
	// Valid per-language value → names parsed under the language.
	mustExec(t, db, `UPDATE sense_meta SET value='["process","perform"]' WHERE key='dispatch_names:ruby'`)
	if _, ok := readDispatchNames(ctx, db)["ruby"]["process"]; !ok {
		t.Error("readDispatchNames (valid) should contain ruby/process")
	}
	// An empty language suffix (the bare prefix plus a colon) is skipped — it
	// names no language, so it can never mark a harvest.
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('dispatch_names:', '["x"]')`)
	if _, ok := readDispatchNames(ctx, db)[""]; ok {
		t.Error("readDispatchNames must skip an empty language suffix")
	}
}
