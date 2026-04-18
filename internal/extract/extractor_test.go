package extract

import (
	"reflect"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// stubExtractor is a tiny Extractor used to exercise the registry without
// pulling a real grammar or running a parse. The Extract method never
// runs in these tests, only registry lookups do.
type stubExtractor struct {
	lang string
	exts []string
}

func (s stubExtractor) Extract(*sitter.Tree, []byte, string, Emitter) error { return nil }
func (s stubExtractor) Grammar() *sitter.Language                           { return nil }
func (s stubExtractor) Language() string                                    { return s.lang }
func (s stubExtractor) Extensions() []string                                { return s.exts }
func (s stubExtractor) Tier() Tier                                          { return TierBasic }

// withCleanRegistry swaps the package-level registry for a fresh one so a
// test can register without leaking into later tests.
func withCleanRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	origLang, origExt := byLang, byExt
	byLang = map[string]Extractor{}
	byExt = map[string]Extractor{}
	registryMu.Unlock()
	t.Cleanup(func() {
		registryMu.Lock()
		byLang, byExt = origLang, origExt
		registryMu.Unlock()
	})
}

func TestRegisterAndForExtension(t *testing.T) {
	withCleanRegistry(t)

	ruby := stubExtractor{lang: "ruby", exts: []string{".rb", ".RAKE"}}
	Register(ruby)

	if got := ForExtension(".rb"); got.Language() != "ruby" {
		t.Errorf("ForExtension(.rb) = %v, want ruby", got)
	}
	// Case-insensitive lookup: callers pass filepath.Ext output, which on a
	// file named "Rakefile.RAKE" would yield ".RAKE" — uppercase should
	// still resolve to the ruby extractor.
	if got := ForExtension(".rake"); got.Language() != "ruby" {
		t.Errorf("ForExtension(.rake) = %v, want ruby", got)
	}
	if got := ForExtension(".py"); got != nil {
		t.Errorf("ForExtension(.py) = %v, want nil", got)
	}
}

func TestByLanguageAndLanguages(t *testing.T) {
	withCleanRegistry(t)

	Register(stubExtractor{lang: "ruby", exts: []string{".rb"}})
	Register(stubExtractor{lang: "go", exts: []string{".go"}})
	Register(stubExtractor{lang: "python", exts: []string{".py"}})

	if got := ByLanguage("go"); got.Language() != "go" {
		t.Errorf("ByLanguage(go) = %v, want go", got)
	}
	if got := ByLanguage("nonexistent"); got != nil {
		t.Errorf("ByLanguage(nonexistent) = %v, want nil", got)
	}

	// Languages() must return in deterministic order so fixture tests
	// and status output don't flap with init() ordering.
	want := []string{"go", "python", "ruby"}
	if got := Languages(); !reflect.DeepEqual(got, want) {
		t.Errorf("Languages() = %v, want %v", got, want)
	}
}

func TestRegisterPanicsOnDuplicateLanguage(t *testing.T) {
	withCleanRegistry(t)
	Register(stubExtractor{lang: "ruby", exts: []string{".rb"}})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate language registration did not panic")
		}
	}()
	Register(stubExtractor{lang: "ruby", exts: []string{".rake"}})
}

func TestRegisterPanicsOnDuplicateExtension(t *testing.T) {
	withCleanRegistry(t)
	Register(stubExtractor{lang: "ruby", exts: []string{".rb"}})

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate extension claim did not panic")
		}
	}()
	Register(stubExtractor{lang: "crystal", exts: []string{".rb"}})
}
