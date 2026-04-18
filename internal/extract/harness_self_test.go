package extract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// stubExtractor emits one fixed symbol regardless of source — just
// enough to exercise the harness plumbing (parse → extract → normalise
// → compare) without depending on any real language implementation.
// It does NOT register with the extract registry; the meta-tests call
// runExtractor directly so they don't pollute the shared registry.
type stubExtractor struct{}

func (stubExtractor) Extract(_ *sitter.Tree, _ []byte, _ string, emit extract.Emitter) error {
	return emit.Symbol(extract.EmittedSymbol{
		Name:      "Stub",
		Qualified: "Stub",
		Kind:      model.KindClass,
		LineStart: 1,
		LineEnd:   1,
	})
}

// Grammar() uses Ruby arbitrarily — the stub ignores the parsed tree,
// so any grammar works. Ruby is cheap and already linked for grammars_test.
func (stubExtractor) Grammar() *sitter.Language { return grammars.Ruby() }
func (stubExtractor) Language() string          { return "stub" }
func (stubExtractor) Extensions() []string      { return []string{".stub"} }
func (stubExtractor) Tier() extract.Tier        { return extract.TierBasic }

// TestHarnessParseEmitRoundTrip drives runExtractor end-to-end against
// a synthetic extractor: harness bugs surface here, independent of any
// real extractor in cards 5+.
func TestHarnessParseEmitRoundTrip(t *testing.T) {
	got, err := runExtractor(stubExtractor{}, []byte("# irrelevant\n"), "stub.stub")
	if err != nil {
		t.Fatalf("runExtractor: %v", err)
	}
	if len(got.Symbols) != 1 {
		t.Fatalf("Symbols = %d, want 1", len(got.Symbols))
	}
	if got.Symbols[0].Qualified != "Stub" {
		t.Errorf("Symbols[0].Qualified = %q, want Stub", got.Symbols[0].Qualified)
	}
	if got.Edges == nil {
		t.Error("Edges nil — runExtractor should normalise to empty slice to match round-trip shape")
	}
	if len(got.Edges) != 0 {
		t.Errorf("Edges has %d entries, want 0", len(got.Edges))
	}
}

// TestHarnessGoldenWriteAndMismatchDetection verifies the two golden
// paths — writeGolden produces a parseable file containing the emitted
// data, and a mutated golden differs from fresh extraction output.
func TestHarnessGoldenWriteAndMismatchDetection(t *testing.T) {
	tmp := t.TempDir()
	goldenPath := filepath.Join(tmp, "sample.golden.json")

	out, err := runExtractor(stubExtractor{}, []byte("# irrelevant\n"), "sample.stub")
	if err != nil {
		t.Fatalf("runExtractor: %v", err)
	}
	writeGolden(t, goldenPath, out)

	b, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Contains(b, []byte(`"Stub"`)) {
		t.Errorf("golden missing Stub symbol: %s", b)
	}

	// Post-writeGolden, nil slices must render as [] (empty arrays) for
	// stable JSON output — a regression here would flip-flop goldens
	// between `null` and `[]` across versions of the extractor.
	if !bytes.Contains(b, []byte(`"edges": []`)) {
		t.Errorf("nil Edges slice did not normalise to []: %s", b)
	}

	var parsed fixtureOutput
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("golden unparseable: %v", err)
	}

	// Mutate the golden and confirm a subsequent extraction diverges.
	mutated := strings.ReplaceAll(string(b), "Stub", "NotStub")
	if err := os.WriteFile(goldenPath, []byte(mutated), 0o644); err != nil {
		t.Fatal(err)
	}
	var want fixtureOutput
	if err := json.Unmarshal([]byte(mutated), &want); err != nil {
		t.Fatalf("mutated golden unparseable: %v", err)
	}
	fresh, err := runExtractor(stubExtractor{}, []byte("# irrelevant\n"), "sample.stub")
	if err != nil {
		t.Fatalf("runExtractor post-mutation: %v", err)
	}
	if want.Symbols[0].Qualified == fresh.Symbols[0].Qualified {
		t.Error("mutated golden still matches — the harness would pass a broken extractor")
	}
}
