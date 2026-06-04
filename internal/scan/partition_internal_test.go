package scan

import "testing"

// TestPartitionHarvestedNamesNoCrossLanguageLeakage is the soundness proof for
// partitionHarvestedNames, the seam the dead-code arbiter's per-language
// reasoning rests on. It is written as a property, not one example: every one of
// the harvested-name kinds is exercised across several languages at once, and
// the assertion is the NEGATIVE — that no harvested name ever lands in a
// destination other than the one its source file feeds. A mis-routed set (a Ruby
// mention leaking into Python's set, a cgo export leaking into the rust set)
// silently corrupts dead-code soundness, so this test must fail loudly if the
// routing ever drifts.
func TestPartitionHarvestedNamesNoCrossLanguageLeakage(t *testing.T) {
	// Three files in distinct languages, each carrying a sentinel in every
	// name-set kind its emitter can produce. Sentinels are language-prefixed so
	// any leak is unambiguous in the failure message.
	ruby := &fileResult{
		Language:       "ruby",
		DispatchNames:  []string{"rb_dispatch"},
		MentionedNames: []string{"rb_mention"},
	}
	python := &fileResult{
		Language:       "python",
		DispatchNames:  []string{"py_dispatch"},
		MentionedNames: []string{"py_mention"},
		PyDecorated:    []string{"py_decorated"},
		PyRoutes:       []string{"py_route"},
		PyDjango:       []string{"py_django"},
		PyAllExports:   []string{"py_all"},
	}
	mixed := &fileResult{
		// A Go file carries cgo exports; the rust/ts/langspec flat sets are
		// folded here too so every flat kind is partitioned in one pass. Flat
		// sets are project-wide by construction (cgo is Go-only, etc.), so the
		// file's own Language does not gate them — what matters is that each set
		// receives only the names handed to it.
		Language:          "go",
		MentionedNames:    []string{"go_mention"},
		CgoExports:        []string{"cgo_export"},
		RustExports:       []string{"rust_export"},
		RustTestSymbols:   []string{"rust_test"},
		RustTraitMethods:  []string{"rust_trait"},
		RustAllowDead:     []string{"rust_allow_dead"},
		TSDecorated:       []string{"ts_decorated"},
		TSDefaultExports:  []string{"ts_default"},
		LangspecAnnotated: []string{"ls_annotated"},
	}

	h := &harness{}
	h.partitionHarvestedNames(ruby)
	h.partitionHarvestedNames(python)
	h.partitionHarvestedNames(mixed)

	// Per-language sets: each language's dispatch/mention names must live ONLY
	// under that language's key.
	assertByLang(t, "dispatchNames", h.dispatchNames, map[string][]string{
		"ruby":   {"rb_dispatch"},
		"python": {"py_dispatch"},
	})
	assertByLang(t, "mentionedNames", h.mentionedNames, map[string][]string{
		"ruby":   {"rb_mention"},
		"python": {"py_mention"},
		"go":     {"go_mention"},
	})

	// Flat sets: each receives exactly the names handed to it, nothing else.
	assertFlat(t, "cgoExports", h.cgoExports, "cgo_export")
	assertFlat(t, "rustExports", h.rustExports, "rust_export")
	assertFlat(t, "rustTestSymbols", h.rustTestSymbols, "rust_test")
	assertFlat(t, "rustTraitMethods", h.rustTraitMethods, "rust_trait")
	assertFlat(t, "rustAllowDead", h.rustAllowDead, "rust_allow_dead")
	assertFlat(t, "tsDecorated", h.tsDecorated, "ts_decorated")
	assertFlat(t, "tsDefaultExports", h.tsDefaultExports, "ts_default")
	assertFlat(t, "pyDecorated", h.pyDecorated, "py_decorated")
	assertFlat(t, "pyRoutes", h.pyRoutes, "py_route")
	assertFlat(t, "pyDjango", h.pyDjango, "py_django")
	assertFlat(t, "pyAllExports", h.pyAllExports, "py_all")
	assertFlat(t, "lsAnnotated", h.lsAnnotated, "ls_annotated")

	// The negative, stated globally: collect every name that landed anywhere and
	// confirm no sentinel reached a set it was never handed to. Each sentinel is
	// unique, so a single membership map is enough to catch any leak.
	want := map[string]string{ // sentinel → the one set it belongs in
		"rb_dispatch": "dispatchNames:ruby", "py_dispatch": "dispatchNames:python",
		"rb_mention": "mentionedNames:ruby", "py_mention": "mentionedNames:python", "go_mention": "mentionedNames:go",
		"cgo_export": "cgoExports", "rust_export": "rustExports", "rust_test": "rustTestSymbols",
		"rust_trait": "rustTraitMethods", "rust_allow_dead": "rustAllowDead",
		"ts_decorated": "tsDecorated", "ts_default": "tsDefaultExports",
		"py_decorated": "pyDecorated", "py_route": "pyRoutes", "py_django": "pyDjango", "py_all": "pyAllExports",
	}
	got := map[string][]string{}
	for lang, set := range h.dispatchNames {
		for n := range set {
			got[n] = append(got[n], "dispatchNames:"+lang)
		}
	}
	for lang, set := range h.mentionedNames {
		for n := range set {
			got[n] = append(got[n], "mentionedNames:"+lang)
		}
	}
	for name, set := range map[string]map[string]struct{}{
		"cgoExports": h.cgoExports, "rustExports": h.rustExports, "rustTestSymbols": h.rustTestSymbols,
		"rustTraitMethods": h.rustTraitMethods, "rustAllowDead": h.rustAllowDead, "tsDecorated": h.tsDecorated,
		"tsDefaultExports": h.tsDefaultExports, "pyDecorated": h.pyDecorated, "pyRoutes": h.pyRoutes,
		"pyDjango": h.pyDjango, "pyAllExports": h.pyAllExports, "lsAnnotated": h.lsAnnotated,
	} {
		for n := range set {
			got[n] = append(got[n], name)
		}
	}
	for sentinel, dest := range want {
		places := got[sentinel]
		if len(places) != 1 || places[0] != dest {
			t.Errorf("sentinel %q landed in %v, want exactly [%s] — cross-language leak", sentinel, places, dest)
		}
	}
}

func assertByLang(t *testing.T, label string, got map[string]map[string]struct{}, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: %d languages, want %d (%v)", label, len(got), len(want), got)
	}
	for lang, names := range want {
		set, ok := got[lang]
		if !ok {
			t.Errorf("%s: missing language %q", label, lang)
			continue
		}
		for _, n := range names {
			if _, ok := set[n]; !ok {
				t.Errorf("%s[%s]: missing %q", label, lang, n)
			}
		}
		if len(set) != len(names) {
			t.Errorf("%s[%s]: %d names, want %d (%v)", label, lang, len(set), len(names), set)
		}
	}
}

func assertFlat(t *testing.T, label string, got map[string]struct{}, want string) {
	t.Helper()
	if len(got) != 1 {
		t.Errorf("%s: %d names, want exactly 1 (%v)", label, len(got), got)
	}
	if _, ok := got[want]; !ok {
		t.Errorf("%s: missing %q (got %v)", label, want, got)
	}
}
