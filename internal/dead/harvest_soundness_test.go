package dead_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
)

// TestDeadVoicesReadHarvestedSetsFromSharedFixture is the read-side end of the
// harvested-name contract that 27-06 asserts on the write side. Both scan the
// SAME multi-language fixture (scantest.HarvestFixture): 27-06's
// TestHarvestFixturePartitionsByLanguage proves scan PARTITIONS each language's
// names into the right per-feature sense_meta set; this proves the dead-code
// voices READ each set back under its own key, so the right open-world reason
// fires for each symbol. A reader that bled across keys (read rust_exports under
// the cgo key, say) would drop or change the reason here, breaking the test.
//
// Sharing one fixture keeps the write/read no-leakage contract a single property
// asserted at both ends rather than two copies that can drift. The fixture
// cannot be scanned from an internal `package dead` test — dead → scantest →
// scan → setup → mcpio → dead is an import cycle — so this end lives in the
// external test package and observes the readers through FindDead's verdicts;
// the per-language reader's no-leakage primitive is pinned directly as a unit in
// meta_readers_internal_test.go.
func TestDeadVoicesReadHarvestedSetsFromSharedFixture(t *testing.T) {
	repo := scantest.NewRepo(t, scantest.HarvestFixture)
	repo.Scan(scan.Options{})

	dbPath := filepath.Join(repo.Root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	res, err := dead.FindDead(context.Background(), db, dead.Options{Limit: 100000})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	reasonByQualified := map[string]string{}
	verdictByQualified := map[string]dead.Verdict{}
	for _, f := range res.Findings {
		verdictByQualified[f.Symbol.Qualified] = f.Verdict
		if f.Reason != nil {
			reasonByQualified[f.Symbol.Qualified] = f.Reason.Code
		}
	}

	// Each row: a fixture symbol, the sense_meta set whose read keeps it
	// open-world, and the reason code that proves that set was read under its
	// own key. Every symbol is zero-edge in this tiny tree, so without the
	// harvested signal it would fall to `dead`; the open-world reason is the
	// observable proof the reader delivered the right set.
	cases := []struct {
		qualified  string // symbol's qualified name in the fixture
		metaKey    string // the sense_meta key the read came from
		wantReason string
	}{
		{"ffi.DoThing", "cgo_exports", "go_cgo"},
		{"exported", "rust_exports", "rust_ffi"},
		{"it_works", "rust_test_symbols", "rust_test"},
		{"retained", "rust_allow_dead", "rust_allow_dead"},
		{"T::m", "rust_trait_impl_methods", "rust_trait_impl"},
		{"Widget", "ts_decorated", "ts_decorator"},
		{"index", "py_routes", "py_route"},
		{"Greeter", "langspec_annotated", "ls_annotated"},
	}

	for _, tc := range cases {
		if v, ok := verdictByQualified[tc.qualified]; !ok {
			t.Errorf("%s: expected a finding, got none (read of %s lost?)", tc.qualified, tc.metaKey)
			continue
		} else if v == dead.VerdictDead {
			t.Errorf("%s: classified dead — the %s read did not keep it open-world", tc.qualified, tc.metaKey)
		}
		if got := reasonByQualified[tc.qualified]; got != tc.wantReason {
			t.Errorf("%s: reason = %q, want %q (set %s read under the wrong key?)",
				tc.qualified, got, tc.wantReason, tc.metaKey)
		}
	}
}
