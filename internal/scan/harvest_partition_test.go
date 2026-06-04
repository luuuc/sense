package scan_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
)

// TestHarvestFixturePartitionsByLanguage scans the shared multi-language fixture
// through the real extractors and asserts the partition held end to end: every
// per-language mention set contains only names from that language's file, and
// each flat per-feature set carries exactly the name its single source produced.
// The unit-level TestPartitionHarvestedNamesNoCrossLanguageLeakage proves the
// routing function in isolation; this proves the same property survives the full
// parse → collect → partition → sense_meta write path against genuine grammars,
// which is the contract 27-09's dead-code voices read back.
func TestHarvestFixturePartitionsByLanguage(t *testing.T) {
	repo := scantest.NewRepo(t, scantest.HarvestFixture)
	repo.Scan(scan.Options{})

	meta := readMetaSets(t, filepath.Join(repo.Root, ".sense", "index.db"))

	// Per-language mention sets must not bleed across languages. We assert a
	// representative name unique to each language lands under its own key and
	// nowhere else.
	perLang := map[string]string{
		"mentioned_names:ruby":       "public_send", // Ruby-only token
		"mentioned_names:python":     "route",       // the .py file's decorator-factory name
		"mentioned_names:rust":       "no_mangle",
		"mentioned_names:typescript": "Component",
		"mentioned_names:java":       "Service",
	}
	for key, name := range perLang {
		assertMetaContains(t, meta, key, name)
		// The negative: that same token must appear under no OTHER language's set.
		for otherKey := range perLang {
			if otherKey == key {
				continue
			}
			assertMetaExcludes(t, meta, otherKey, name)
		}
	}

	// Flat per-feature sets: each carries exactly the name its one source emits.
	flat := map[string]string{
		"cgo_exports":             "DoThing",
		"rust_exports":            "exported",
		"rust_allow_dead":         "retained",
		"rust_test_symbols":       "it_works",
		"rust_trait_impl_methods": "m",
		"ts_decorated":            "Widget",
		"ts_default_exports":      "main",
		"py_decorated":            "index",
		"py_routes":               "home",
		"py_all_exports":          "index",
		"langspec_annotated":      "Greeter",
		"dispatch_names:ruby":     "handle",
	}
	for key, name := range flat {
		assertMetaContains(t, meta, key, name)
	}
}

func readMetaSets(t *testing.T, dbPath string) map[string][]string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	rows, err := db.Query(`SELECT key, value FROM sense_meta`)
	if err != nil {
		t.Fatalf("query meta: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string][]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatalf("scan meta: %v", err)
		}
		var names []string
		if json.Unmarshal([]byte(v), &names) == nil {
			out[k] = names
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("meta rows: %v", err)
	}
	return out
}

func assertMetaContains(t *testing.T, meta map[string][]string, key, name string) {
	t.Helper()
	for _, n := range meta[key] {
		if n == name {
			return
		}
	}
	t.Errorf("sense_meta[%q] = %v, want it to contain %q", key, meta[key], name)
}

func assertMetaExcludes(t *testing.T, meta map[string][]string, key, name string) {
	t.Helper()
	for _, n := range meta[key] {
		if n == name {
			t.Errorf("sense_meta[%q] = %v leaked %q (cross-language)", key, meta[key], name)
		}
	}
}
