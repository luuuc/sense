package dead

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/extract"
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

// TestStructuralQueryHelpersReturnRows covers the happy paths of the
// soundness-critical structural queries against a hand-built index — the rows
// the error-path tests above never exercise. These queries decide which
// zero-edge symbols are kept open-world (controller concerns, value objects,
// included modules, interface methods, test targets); a wrong result here is a
// false `dead` or a missed one, so the SELECTs are pinned, not assumed.
func TestStructuralQueryHelpersReturnRows(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_symbols(id INTEGER PRIMARY KEY, name TEXT, qualified TEXT, kind TEXT, parent_id INTEGER)`)
	mustExec(t, db, `CREATE TABLE sense_edges(kind TEXT, source_id INTEGER, target_id INTEGER)`)

	mustExec(t, db, `INSERT INTO sense_symbols(id, name, qualified, kind, parent_id) VALUES
		(1,  'FooController',   'FooController',   'class',     NULL),
		(2,  'Auditable',       'Auditable',       'module',    NULL),
		(5,  'Money',           'Money',           'class',     NULL),
		(10, 'Struct',          '`+extract.RubyCoreStruct+`', 'class',     NULL),
		(20, 'Serializer',      'Serializer',      'interface', NULL),
		(21, 'serialize',       'Serializer#serialize', 'method', 20),
		(30, 'Trackable',       'Trackable',       'module',    NULL),
		(99, 'PlainClass',      'PlainClass',      'class',     NULL),
		(40, 'tested_method',   'Thing#tested_method', 'method', NULL)`)
	mustExec(t, db, `INSERT INTO sense_edges(kind, source_id, target_id) VALUES
		('includes', 1,  2),
		('includes', 99, 30),
		('inherits', 5,  10),
		('tests',    50, 40)`)

	t.Run("controller concern modules", func(t *testing.T) {
		got, err := queryControllerConcernModuleIDs(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		// Only the include INTO a *Controller counts; the include into PlainClass
		// (module 30) must not leak in.
		assertIDSet(t, got, 2)
	})
	t.Run("value-object classes", func(t *testing.T) {
		got, err := queryValueObjectClassIDs(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		// The class inheriting the synthetic ruby-core:Struct base.
		assertIDSet(t, got, 5)
	})
	t.Run("included modules", func(t *testing.T) {
		got, err := queryIncludedModuleIDs(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		// Every includes target, regardless of the including class.
		assertIDSet(t, got, 2, 30)
	})
	t.Run("interface method names", func(t *testing.T) {
		got, err := queryInterfaceMethodNames(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 {
			t.Fatalf("got %v, want exactly {serialize}", got)
		}
		if _, ok := got["serialize"]; !ok {
			t.Errorf("got %v, want serialize", got)
		}
	})
	t.Run("tests targets", func(t *testing.T) {
		got, err := queryTestsTargets(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		assertIDSet(t, got, 40)
	})
}

// assertIDSet fails unless got holds exactly the given IDs.
func assertIDSet(t *testing.T, got map[int64]struct{}, want ...int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want exactly %v", got, want)
	}
	for _, id := range want {
		if _, ok := got[id]; !ok {
			t.Errorf("got %v, missing %d", got, id)
		}
	}
}

// The meta-reader tests (readFrameworks, readHarvestedLangs, readDispatchNames,
// the per-language no-leakage primitive, and the flat-set wiring) live in
// meta_readers_internal_test.go, beside the code under test. memDB and mustExec
// above are shared package-test helpers.
