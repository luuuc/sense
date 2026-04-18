package extract_test

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	// Blank-import every registered language so their init() hooks run.
	_ "github.com/luuuc/sense/internal/extract/languages"
)

// updateGoldens triggers `-update` mode: instead of failing on a diff,
// the runner overwrites the golden file with the current extractor
// output. Commit the resulting diff only after reviewing it — the whole
// point of golden files is that changes are conspicuous in review.
var updateGoldens = flag.Bool(
	"update",
	false,
	"regenerate .golden.json files instead of asserting against them",
)

// testdataRoot is where per-language fixtures live. A language's
// fixtures sit in <root>/<language>/. The runner walks that directory
// and treats every file whose extension an extractor claims as a test
// case; every *.golden.json is its companion expectation.
const testdataRoot = "testdata"

// TestFixtures runs every registered extractor against its testdata/.
// Discovery is driven by extract.Languages() so adding a new language
// only requires a new entry in internal/extract/all — no new test file.
//
// Each language directory is its own subtest. Missing goldens and
// orphan goldens are both errors unless -update is set, in which case
// goldens are created/updated in place.
func TestFixtures(t *testing.T) {
	for _, lang := range extract.Languages() {
		t.Run(lang, func(t *testing.T) {
			runLangFixtures(t, lang)
		})
	}
}

func runLangFixtures(t *testing.T, lang string) {
	t.Helper()

	ex := extract.ByLanguage(lang)
	if ex == nil {
		t.Fatalf("extractor %q disappeared between Languages() and ByLanguage()", lang)
	}

	dir := filepath.Join(testdataRoot, lang)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// A registered extractor without fixtures is a build-time
			// omission we want visible, not silent coverage loss.
			t.Fatalf("testdata/%s/ missing — every registered language needs fixtures", lang)
		}
		t.Fatalf("read %s: %v", dir, err)
	}

	// Classify entries into source files (by extension the extractor
	// claims) and golden files. Anything else is left alone — a reader
	// might drop a README in the directory.
	extSet := make(map[string]struct{}, len(ex.Extensions()))
	for _, e := range ex.Extensions() {
		extSet[strings.ToLower(e)] = struct{}{}
	}

	type fixture struct {
		src    string
		golden string
	}
	fixtures := map[string]fixture{}
	var orphanGoldens []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)

		if strings.HasSuffix(name, ".golden.json") {
			base := strings.TrimSuffix(name, ".golden.json")
			f := fixtures[base]
			f.golden = path
			fixtures[base] = f
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if _, ok := extSet[ext]; !ok {
			continue
		}
		base := strings.TrimSuffix(name, filepath.Ext(name))
		f := fixtures[base]
		f.src = path
		fixtures[base] = f
	}

	// Sort for deterministic subtest ordering.
	names := make([]string, 0, len(fixtures))
	for k := range fixtures {
		names = append(names, k)
	}
	sort.Strings(names)

	if len(names) == 0 {
		t.Fatalf("testdata/%s/ has no source fixtures", lang)
	}

	for _, name := range names {
		f := fixtures[name]
		t.Run(name, func(t *testing.T) {
			if f.src == "" {
				if !*updateGoldens {
					t.Fatalf("golden %s has no matching source file", f.golden)
				}
				// -update + orphan golden = the source was deleted on
				// purpose; remove the stale golden too.
				orphanGoldens = append(orphanGoldens, f.golden)
				return
			}

			srcBytes, err := os.ReadFile(f.src)
			if err != nil {
				t.Fatalf("read source %s: %v", f.src, err)
			}

			got, err := runExtractor(ex, srcBytes, f.src)
			if err != nil {
				t.Fatalf("extract %s: %v", f.src, err)
			}

			goldenPath := f.golden
			if goldenPath == "" {
				// The per-language directory is the canonical home for
				// a missing golden too; derive its path from the source.
				goldenPath = strings.TrimSuffix(f.src, filepath.Ext(f.src)) + ".golden.json"
			}

			if *updateGoldens {
				writeGolden(t, goldenPath, got)
				return
			}

			if f.golden == "" {
				t.Fatalf("%s missing — run `go test ./internal/extract -update` after reviewing extractor output", goldenPath)
			}

			wantBytes, err := os.ReadFile(f.golden)
			if err != nil {
				t.Fatalf("read golden %s: %v", f.golden, err)
			}
			var want fixtureOutput
			if err := json.Unmarshal(wantBytes, &want); err != nil {
				t.Fatalf("parse golden %s: %v", f.golden, err)
			}

			if !reflect.DeepEqual(got, want) {
				gotJSON, _ := json.MarshalIndent(got, "", "  ")
				wantJSON, _ := json.MarshalIndent(want, "", "  ")
				t.Errorf("%s mismatch\n--- want %s\n%s\n--- got\n%s",
					f.src, f.golden, wantJSON, gotJSON)
			}
		})
	}

	// Cleanup pass for -update: remove goldens whose source disappeared.
	// Log each removal so the diff produced by `-update` is
	// self-explanatory in review — silent deletions in test code are a
	// recipe for "why did my golden disappear?" confusion later.
	if *updateGoldens {
		for _, g := range orphanGoldens {
			if err := os.Remove(g); err != nil {
				t.Errorf("remove orphan %s: %v", g, err)
				continue
			}
			t.Logf("removed orphan golden %s (source deleted)", g)
		}
	}
}

// fixtureOutput is the on-disk golden schema. Keep the JSON field names
// stable: any rename is a golden-wide diff.
type fixtureOutput struct {
	Symbols []fixtureSymbol `json:"symbols"`
	Edges   []fixtureEdge   `json:"edges"`
}

type fixtureSymbol struct {
	Name       string `json:"name"`
	Qualified  string `json:"qualified"`
	Kind       string `json:"kind"`
	Visibility string `json:"visibility,omitempty"`
	Parent     string `json:"parent,omitempty"`
	LineStart  int    `json:"line_start"`
	LineEnd    int    `json:"line_end"`
}

type fixtureEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Line   int    `json:"line,omitempty"`
}

// runExtractor drives one extractor over one source buffer and returns
// the normalised, sorted output ready for comparison.
//
// The call into ex.Extract is wrapped in a recover() so one bad
// extractor panicking on a weird CST node fails just that fixture
// subtest, not the whole binary. Tree-sitter nodes are raw C memory
// under the hood and a nil-deref from a misidentified node type is a
// realistic shape of bug — we want the fixture that exposed it named
// in the output, not a stack trace with no file context.
func runExtractor(ex extract.Extractor, source []byte, path string) (out fixtureOutput, err error) {
	parser := sitter.NewParser()
	defer parser.Close()

	if err := parser.SetLanguage(ex.Grammar()); err != nil {
		return fixtureOutput{}, err
	}
	tree := parser.Parse(source, nil)
	if tree == nil {
		return fixtureOutput{}, errParseFailed
	}
	defer tree.Close()

	em := &collectingEmitter{}

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("extractor panicked on %s: %v", path, r)
			}
		}()
		err = ex.Extract(tree, source, path, em)
	}()
	if err != nil {
		return fixtureOutput{}, err
	}

	// Initialise as empty slices so the returned value matches the
	// shape writeGolden persists (which coerces nil → []). Without
	// this, a fixture that emits no edges round-trips as
	// reflect.DeepEqual(nil, []fixtureEdge{}) = false and fails
	// spuriously. Keep the initialisation here, not in the caller,
	// so every consumer of runExtractor sees the same shape.
	out.Symbols = []fixtureSymbol{}
	out.Edges = []fixtureEdge{}

	for _, s := range em.symbols {
		out.Symbols = append(out.Symbols, fixtureSymbol{
			Name:       s.Name,
			Qualified:  s.Qualified,
			Kind:       string(s.Kind),
			Visibility: s.Visibility,
			Parent:     s.ParentQualified,
			LineStart:  s.LineStart,
			LineEnd:    s.LineEnd,
		})
	}
	for _, e := range em.edges {
		fe := fixtureEdge{
			Source: e.SourceQualified,
			Target: e.TargetQualified,
			Kind:   string(e.Kind),
		}
		if e.Line != nil {
			fe.Line = *e.Line
		}
		out.Edges = append(out.Edges, fe)
	}

	// Stable order keeps goldens insensitive to extractor traversal
	// order. Sort by (qualified, kind, line_start) for symbols and
	// (source, target, kind, line) for edges.
	slices.SortFunc(out.Symbols, func(a, b fixtureSymbol) int {
		if a.Qualified != b.Qualified {
			return strings.Compare(a.Qualified, b.Qualified)
		}
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return a.LineStart - b.LineStart
	})
	slices.SortFunc(out.Edges, func(a, b fixtureEdge) int {
		if a.Source != b.Source {
			return strings.Compare(a.Source, b.Source)
		}
		if a.Target != b.Target {
			return strings.Compare(a.Target, b.Target)
		}
		if a.Kind != b.Kind {
			return strings.Compare(a.Kind, b.Kind)
		}
		return a.Line - b.Line
	})

	return out, nil
}

// collectingEmitter accumulates extractor output for the fixture runner.
// The scan harness has its own Emitter that writes to SQLite; this one
// just buffers, which is fine at fixture scale.
type collectingEmitter struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (c *collectingEmitter) Symbol(s extract.EmittedSymbol) error {
	c.symbols = append(c.symbols, s)
	return nil
}
func (c *collectingEmitter) Edge(e extract.EmittedEdge) error {
	c.edges = append(c.edges, e)
	return nil
}

func writeGolden(t *testing.T, path string, out fixtureOutput) {
	t.Helper()
	// Normalise to "null ⇒ empty slice" so the JSON reads the same
	// regardless of whether any symbols/edges were extracted.
	if out.Symbols == nil {
		out.Symbols = []fixtureSymbol{}
	}
	if out.Edges == nil {
		out.Edges = []fixtureEdge{}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal golden %s: %v", path, err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write golden %s: %v", path, err)
	}
}

var errParseFailed = errors.New("parse failed")
