package mcpio

import (
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/extract"
	_ "github.com/luuuc/sense/internal/extract/languages" // populate the extractor registry
)

func TestIndexCaveat(t *testing.T) {
	cases := []struct {
		file    string
		wantSub string // substring expected in the caveat; "" means empty result
	}{
		{"main.go", "method-on-field dispatch"},
		{"app/models/user.rb", "DiscoursePluginRegistry"},
		{"lib/tasks/cleanup.rake", "DiscoursePluginRegistry"},
		{"src/index.js", "edge-runtime mirror"},
		{"src/index.jsx", "edge-runtime mirror"},
		{"src/index.mjs", "edge-runtime mirror"},
		{"src/index.cjs", "edge-runtime mirror"},
		{"src/index.ts", "edge-runtime mirror"},
		{"src/index.tsx", "edge-runtime mirror"},
		{"app/server.py", "decorator-registered handlers"},
		{"src/Main.java", "ServiceLoader"},
		{"src/Main.kt", "ServiceLoader"},
		{"build.kts", "ServiceLoader"},
		// Case-insensitive extension match.
		{"src/Main.JAVA", "ServiceLoader"},
		{"src/index.TS", "edge-runtime mirror"},
		// Unknown extension → empty.
		{"README.md", ""},
		{"data.txt", ""},
		// No extension → empty.
		{"Makefile", ""},
		// Empty filename → empty.
		{"", ""},
	}

	for _, c := range cases {
		got := IndexCaveat(c.file)
		if c.wantSub == "" {
			if got != "" {
				t.Errorf("IndexCaveat(%q) = %q, want empty", c.file, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("IndexCaveat(%q) = %q, want substring %q", c.file, got, c.wantSub)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"a.go", "go"},
		{"a.rb", "ruby"},
		{"a.rake", "ruby"},
		{"a.js", "javascript"},
		{"a.jsx", "javascript"},
		{"a.mjs", "javascript"},
		{"a.cjs", "javascript"},
		{"a.ts", "typescript"},
		{"a.tsx", "typescript"},
		{"a.py", "python"},
		{"a.java", "java"},
		{"a.kt", "kotlin"},
		{"a.kts", "kotlin"},
		{"a.GO", "go"},
		{"weird.xyz", ""},
		{"no_extension", ""},
		{"", ""},
		// Dot in directory, no extension on filename.
		{"some.dir/file", ""},
	}

	for _, c := range cases {
		if got := detectLanguage(c.file); got != c.want {
			t.Errorf("detectLanguage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}

// TestIndexCaveatPHP is the repro for the facade disclosure gap. `sense blast` on a
// Laravel facade returns a partial dependent set, reports verdict "complete" with
// risk "low", and advises "act on it, do not re-grep" - while promising that any
// dynamic-dispatch residual "is in index_caveat". Measured on the pinned filament
// clone: blast on the FilamentAsset facade returned total_affected 12 with
// index_caveat ABSENT, while the index held 33 inbound `imports` edges and 34 files
// made static calls on it. The channel the advice line points at was structurally
// empty for every PHP file, because neither switch in this file had a php case.
func TestIndexCaveatPHP(t *testing.T) {
	const file = "app/Traits/Jobs.php"

	if got := detectLanguage(file); got != "php" {
		t.Fatalf("detectLanguage(%q) = %q, want %q", file, got, "php")
	}

	got := IndexCaveat(file)
	if got == "" {
		t.Fatal("IndexCaveat returned empty for a .php file: the advice line promises " +
			"the dynamic-dispatch residual is in index_caveat, so an empty caveat makes " +
			"blast's `complete` verdict unqualified on the most idiomatic Laravel construct")
	}
	// The blind spot that motivated the fix must be named concretely. Vague
	// uncertainty is what this file's doc comment explicitly rejects.
	for _, want := range []string{"facade", "container"} {
		if !strings.Contains(strings.ToLower(got), want) {
			t.Errorf("IndexCaveat(%q) = %q, want it to name %q", file, got, want)
		}
	}
}

// TestExtensionsInsideCoveredLanguages records the INTENT for the two extensions the
// parity gate surfaced, so a later reader can tell they were reasoned about rather than
// swept in. Both are the same language as an extension already covered - a .pyi stub is
// Python and a .gemspec is Ruby, evaluated by the same interpreter with the same dynamic
// features - so the existing caveat text is correct for them unchanged. Neither appears
// in any frozen scenario's gold, which is what bounded this change's exposure to zero.
func TestExtensionsInsideCoveredLanguages(t *testing.T) {
	if got, want := IndexCaveat("types.pyi"), IndexCaveat("types.py"); got != want {
		t.Errorf("a .pyi stub is Python: caveat = %q, want the Python caveat %q", got, want)
	}
	if got, want := IndexCaveat("sense.gemspec"), IndexCaveat("sense.rb"); got != want {
		t.Errorf("a .gemspec is Ruby: caveat = %q, want the Ruby caveat %q", got, want)
	}
}

// TestIndexCaveatUnchangedForOtherLanguages pins the five pre-existing caveats
// BYTE-EXACT. The php addition claims "no other language changes"; byte equality is
// precisely that claim, so it is asserted literally rather than by substring.
func TestIndexCaveatUnchangedForOtherLanguages(t *testing.T) {
	want := map[string]string{
		"main.go": "Static graph may miss: method-on-field dispatch (c.engine.X), function-value passing (handlers stored as fields), runtime init() registration, and interface satisfaction via blank identifier.",
		"user.rb": "Static graph may miss: plugin extensions (DiscoursePluginRegistry, add_to_class), prepended/included modules, method_missing dispatch, and ActiveSupport concern injection.",
		"i.ts":    "Static graph may miss: edge-runtime mirror files (.edge.*, route-modules/*), dynamic require / module.compiled wrappers, decorator-registered handlers, and build-template re-exports.",
		"s.py":    "Static graph may miss: decorator-registered handlers (Flask/FastAPI routes), __init_subclass__ / metaclass registration, importlib dynamic imports, and pytest fixture discovery.",
		"M.java":  "Static graph may miss: reflection-based dispatch, ServiceLoader / @AutoService registration, Spring/CDI dependency injection, annotation-processor-generated handlers, and dynamic proxy classes.",
	}
	for file, exp := range want {
		if got := IndexCaveat(file); got != exp {
			t.Errorf("IndexCaveat(%q) changed:\n got: %q\nwant: %q", file, got, exp)
		}
	}
}

// languagesWithoutCaveatYet lists registered languages that deliberately have no
// caveat prose. It exists so the gap is a VISIBLE, deliberate list rather than an
// empty string nobody notices: writing a caveat needs real per-language blind-spot
// knowledge, and inventing it would violate this file's "named patterns, never vague"
// rule. Entries leave this list as their stack gets benched; nothing is added without
// a decision, because TestCaveatCoversEveryRegisteredLanguage fails otherwise.
var languagesWithoutCaveatYet = map[string]bool{
	"c": true, "cpp": true, "csharp": true, "erb": true, "rust": true, "scala": true,
}

// TestCaveatCoversEveryRegisteredLanguage is the anti-drift gate. detectLanguage is a
// hand-maintained extension table that duplicates the extractor registry's canonical
// ext->language mapping, and it drifted: `.php` was absent entirely, and `.pyi` and
// `.gemspec` were absent even though python and ruby both HAVE caveat prose. Delegating
// to extract.ForExtension at runtime was considered and rejected - the registry is only
// populated by importing extract/languages, which pulls every tree-sitter grammar into
// a package the linter designates pure core. A test-time parity check buys the same
// single-source-of-truth guarantee with no runtime coupling.
//
// This test imports the registry deliberately: a new extractor now either gets a
// caveat or gets an explicit entry above. Silence is no longer an option.
func TestCaveatCoversEveryRegisteredLanguage(t *testing.T) {
	for _, lang := range extract.Languages() {
		e := extract.ByLanguage(lang)
		for _, ext := range e.Extensions() {
			file := "sample" + ext
			got := detectLanguage(file)
			if languagesWithoutCaveatYet[lang] {
				continue // known, deliberate gap
			}
			if got == "" {
				t.Errorf("detectLanguage(%q) = \"\" but %q is a registered language with "+
					"caveat prose: the extension table drifted from the extractor registry",
					file, lang)
				continue
			}
			if IndexCaveat(file) == "" {
				t.Errorf("IndexCaveat(%q) = \"\" for registered language %q (detected as %q)",
					file, lang, got)
			}
		}
	}
}

// TestNoCaveatGapListIsHonest keeps the escape hatch from rotting: an entry that is no
// longer registered, or that has since gained a caveat, must be removed. A stale
// exception list is how a deny-by-default gate quietly becomes allow-by-default.
func TestNoCaveatGapListIsHonest(t *testing.T) {
	registered := map[string]bool{}
	for _, lang := range extract.Languages() {
		registered[lang] = true
	}
	for lang := range languagesWithoutCaveatYet {
		if !registered[lang] {
			t.Errorf("languagesWithoutCaveatYet has %q, which is not a registered language: remove it", lang)
			continue
		}
		exts := extract.ByLanguage(lang).Extensions()
		if len(exts) > 0 && IndexCaveat("sample"+exts[0]) != "" {
			t.Errorf("%q is listed as having no caveat, but IndexCaveat returns one: remove it from the list", lang)
		}
	}
}
