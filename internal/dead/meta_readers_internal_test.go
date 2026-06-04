package dead

import (
	"context"
	"testing"
)

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

// TestReadMentionedNamesNoCrossLanguageLeakage pins the read-side soundness
// primitive: readNameSetMetaByLang must return each language's harvested names
// ONLY under that language, never bleeding one language's tokens into another's
// set. This is the unit-level mirror of 27-06's write-side partition test — the
// representative tokens (public_send/route/no_mangle) are the same ones the
// shared fixture emits per language, but the assertion is the structural
// no-leakage property, so it holds whatever tokens the fixture carries. A symbol
// may earn `dead` only against mentions from its OWN language, so a leak here
// would let one language's mentions wrongly keep another's symbol open-world (or
// the reverse, a false `dead`) — the highest-stakes failure the product has.
func TestReadMentionedNamesNoCrossLanguageLeakage(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)
	mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES
		('mentioned_names:ruby',   '["public_send","alpha"]'),
		('mentioned_names:python', '["route","beta"]'),
		('mentioned_names:rust',   '["no_mangle","gamma"]')`)

	got := readMentionedNames(ctx, db)
	if len(got) != 3 {
		t.Fatalf("readMentionedNames returned %d languages, want 3: %v", len(got), got)
	}

	// own[lang] is the token unique to that language; it must be present under
	// its own language and absent from every other language's set.
	own := map[string]string{"ruby": "public_send", "python": "route", "rust": "no_mangle"}
	for lang, token := range own {
		if _, ok := got[lang][token]; !ok {
			t.Errorf("%s set missing its own token %q: %v", lang, token, got[lang])
		}
		for other := range own {
			if other == lang {
				continue
			}
			if _, leaked := got[other][token]; leaked {
				t.Errorf("token %q (%s) leaked into the %s set: %v", token, lang, other, got[other])
			}
		}
	}
}

// flatHarvestSetField pairs a flat sense_meta key with an accessor for the Facts
// field buildFacts wires it into. It exists only for
// TestBuildFactsWiresFlatSetsByKey, which checks every flat read lands in the
// right field — the property the inline collapse must preserve.
var flatHarvestSetField = []struct {
	key   string
	field func(Facts) map[string]struct{}
}{
	{"cgo_exports", func(f Facts) map[string]struct{} { return f.CgoExportNames }},
	{"rust_exports", func(f Facts) map[string]struct{} { return f.RustExportNames }},
	{"rust_test_symbols", func(f Facts) map[string]struct{} { return f.RustTestSymbolNames }},
	{"rust_trait_impl_methods", func(f Facts) map[string]struct{} { return f.RustTraitImplMethodNames }},
	{"rust_allow_dead", func(f Facts) map[string]struct{} { return f.RustAllowDeadNames }},
	{"ts_decorated", func(f Facts) map[string]struct{} { return f.TSDecoratedNames }},
	{"ts_default_exports", func(f Facts) map[string]struct{} { return f.TSDefaultExportNames }},
	{"py_decorated", func(f Facts) map[string]struct{} { return f.PythonDecoratedNames }},
	{"py_routes", func(f Facts) map[string]struct{} { return f.PythonRouteNames }},
	{"py_django", func(f Facts) map[string]struct{} { return f.PythonDjangoNames }},
	{"py_all_exports", func(f Facts) map[string]struct{} { return f.PythonAllExportNames }},
	{"langspec_annotated", func(f Facts) map[string]struct{} { return f.LangspecAnnotatedNames }},
}

// TestBuildFactsWiresFlatSetsByKey is the precise pin for the flat-reader
// collapse: each flat harvested-name set must reach its own Facts field and no
// other. The integration test on the shared fixture proves the read path works
// end to end but can only observe the keys a verdict happens to expose; this
// seeds every flat key with a UNIQUE sentinel against a minimal schema and
// asserts each field carries exactly its own sentinel — so a field accidentally
// reading the wrong key (the one mistake the inline collapse could introduce) is
// caught here, not in production.
func TestBuildFactsWiresFlatSetsByKey(t *testing.T) {
	db := memDB(t)
	ctx := context.Background()

	// The empty index tables let buildFacts's structural queries succeed
	// (returning nothing); only sense_meta carries data.
	mustExec(t, db, `CREATE TABLE sense_meta(key TEXT PRIMARY KEY, value TEXT)`)
	mustExec(t, db, `CREATE TABLE sense_files(id INTEGER PRIMARY KEY, path TEXT, language TEXT)`)
	mustExec(t, db, `CREATE TABLE sense_symbols(id INTEGER PRIMARY KEY, name TEXT, qualified TEXT, kind TEXT, file_id INTEGER, parent_id INTEGER)`)
	mustExec(t, db, `CREATE TABLE sense_edges(kind TEXT, source_id INTEGER, target_id INTEGER, file_id INTEGER)`)

	// Each flat key gets a sentinel unique to it: "<key>#only".
	for _, fk := range flatHarvestSetField {
		mustExec(t, db, `INSERT INTO sense_meta(key, value) VALUES('`+fk.key+`', '["`+fk.key+`#only"]')`)
	}

	facts, err := buildFacts(ctx, db)
	if err != nil {
		t.Fatalf("buildFacts: %v", err)
	}

	for _, fk := range flatHarvestSetField {
		set := fk.field(facts)
		want := fk.key + "#only"
		if len(set) != 1 {
			t.Errorf("field for %q = %v, want exactly {%q}", fk.key, set, want)
			continue
		}
		if _, ok := set[want]; !ok {
			t.Errorf("field for %q = %v, want its own sentinel %q (wrong key wired?)", fk.key, set, want)
		}
	}
}
