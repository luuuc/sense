package scan_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// quietOpts returns scan.Options that discard warnings — the common
// case for tests that only care about Result values or the summary
// line, not diagnostic output.
func quietOpts(root string) scan.Options {
	return scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}
}

func TestScanCreatesIndex(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go", "pkg/b.go", "pkg/sub/c.go"})

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want 3", res.Files)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected %s to exist: %v", dbPath, err)
	}
}

func TestScanSkipsDotPrefixedDirs(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{
		"main.go",
		"cmd/binary.go",
		".git/HEAD",
		".git/objects/abc",
		".vscode/settings.json",
	})

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2 (should skip .git and .vscode)", res.Files)
	}
}

func TestScanIsRerunnable(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go", "b.go"})

	ctx := context.Background()

	first, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}

	second, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if first.Files != second.Files {
		t.Errorf("file count drifted between runs: first=%d second=%d",
			first.Files, second.Files)
	}
	if first.Symbols != second.Symbols {
		t.Errorf("symbol count drifted between runs: first=%d second=%d",
			first.Symbols, second.Symbols)
	}
	if second.Files != 2 {
		t.Errorf("second.Files = %d, want 2", second.Files)
	}

	// A fresh open after the two scans confirms the adapter released its
	// file locks cleanly.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open after re-run: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestScanSchemaApplied(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go"})

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open on scanned db: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	got, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query on scanned db: %v", err)
	}
	// a.go is a valid Go file — the fixture writes `package a` — so
	// we expect at least the implicit package symbol to materialise
	// (or none, since we don't emit package symbols). Zero symbols is
	// a valid state. The schema check is that Query succeeded.
	_ = got
}

func TestScanOutputFormat(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"x.go", "y.go"})

	var buf bytes.Buffer
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &buf,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Summary line format: "scanned N files (I indexed, K changed, M skipped) in Xms"
	pattern := regexp.MustCompile(`^scanned 2 files \(\d+ indexed, \d+ changed, \d+ skipped\) in \S+\n\z`)
	if !pattern.MatchString(buf.String()) {
		t.Fatalf("output does not match summary pattern\nhave: %q\nwant: scanned 2 files (I indexed, K changed, M skipped) in D\\n",
			buf.String())
	}
}

func TestScanRespectsCustomSense(t *testing.T) {
	root := t.TempDir()
	sense := t.TempDir() // deliberately outside root
	buildTree(t, root, []string{"a.go"})

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Sense:    sense,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sense, "index.db")); err != nil {
		t.Errorf("custom Sense index missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".sense", "index.db")); err == nil {
		t.Error("default <Root>/.sense/index.db was created despite custom Sense option")
	}
}

func TestScanErrorsOnInvalidRoot(t *testing.T) {
	parent := t.TempDir()
	notADir := filepath.Join(parent, "regular-file")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	_, err := scan.Run(context.Background(), scan.Options{
		Root:     notADir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err == nil {
		t.Fatal("expected error when Root is a regular file, got nil")
	}
}

// TestScanWritesSymbols is the integration test for card 10: given a
// repo with valid source, sense scan should produce a non-empty
// sense_symbols table with correctly resolved parent relationships
// and at least one edge. This is what makes the pitch's acceptance
// criterion meaningful.
func TestScanWritesSymbols(t *testing.T) {
	root := t.TempDir()

	// One Go file covering: package-level function, const, struct,
	// and method-with-receiver — enough to exercise parent resolution.
	goSrc := `package demo

const Greeting = "hi"

type User struct {
	Name string
}

func (u User) Greet() string {
	return Greeting
}

func New() User {
	return User{}
}
`
	writeFile(t, filepath.Join(root, "demo.go"), goSrc)

	// One Ruby file with inheritance so we get an edge in addition to
	// symbols — pins the edge-resolution path end-to-end.
	rubySrc := `class Base
  def hello
  end
end

class Child < Base
  def greet
  end
end
`
	writeFile(t, filepath.Join(root, "mix.rb"), rubySrc)

	ctx := context.Background()
	res, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatal("no symbols written")
	}
	if res.Edges == 0 {
		t.Fatal("no edges written; expected Child → Base inherits edge from Ruby fixture")
	}
	if res.Indexed != 2 {
		t.Errorf("Indexed = %d, want 2 (one .go + one .rb)", res.Indexed)
	}

	// Open the index directly and assert structural properties.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Query all symbols; expect at least Greet, New, Greeting, User,
	// Base, Child, hello, greet.
	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byName := map[string]model.Symbol{}
	for _, s := range all {
		byName[s.Qualified] = s
	}
	for _, want := range []string{
		"demo.Greeting", "demo.User", "demo.New",
		"demo.User.Greet", // method qualified through receiver
		"Base", "Child",
	} {
		if _, ok := byName[want]; !ok {
			t.Errorf("symbol %q not found", want)
		}
	}

	// Parent resolution: demo.User.Greet should carry ParentID pointing
	// to demo.User — card 10's sort-by-length strategy guarantees the
	// parent is written first and the pointer resolves within the same
	// file.
	greet := byName["demo.User.Greet"]
	user := byName["demo.User"]
	if user.ID == 0 {
		t.Fatal("demo.User has no ID; Query didn't hydrate")
	}
	if greet.ParentID == nil {
		t.Error("demo.User.Greet.ParentID is nil, want demo.User.ID")
	} else if *greet.ParentID != user.ID {
		t.Errorf("demo.User.Greet.ParentID = %d, want %d", *greet.ParentID, user.ID)
	}

	// Edge: confirm Child inherits from Base. ReadSymbol returns the
	// full context including outbound edges.
	child := byName["Child"]
	ctxSym, err := a.ReadSymbol(ctx, child.ID)
	if err != nil {
		t.Fatalf("ReadSymbol(Child): %v", err)
	}
	found := false
	for _, e := range ctxSym.Outbound {
		if e.Edge.Kind == model.EdgeInherits && e.Target.Qualified == "Base" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Child has no inherits→Base edge; got outbound=%+v", ctxSym.Outbound)
	}
}

// TestScanResolvesCrossFileEdges proves the two-pass resolution
// pass: before this card, edges whose target lived in a sibling file
// were silently dropped. Now an inherits edge from child.rb →
// base.rb's Base must resolve and land in sense_edges.
func TestScanResolvesCrossFileEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "base.rb"), "class Base\n  def hello\n  end\nend\n")
	writeFile(t, filepath.Join(root, "child.rb"), "class Child < Base\n  def greet\n  end\nend\n")

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Edges == 0 {
		t.Fatalf("no edges resolved; expected Child→Base cross-file inherits")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byName := map[string]model.Symbol{}
	for _, s := range all {
		byName[s.Qualified] = s
	}
	child, ok := byName["Child"]
	if !ok {
		t.Fatal("Child symbol missing")
	}
	base, ok := byName["Base"]
	if !ok {
		t.Fatal("Base symbol missing")
	}
	if child.FileID == base.FileID {
		t.Fatalf("fixture regression: Base and Child share a file (id=%d); cross-file cannot be proven", child.FileID)
	}

	sym, err := a.ReadSymbol(ctx, child.ID)
	if err != nil {
		t.Fatalf("ReadSymbol(Child): %v", err)
	}
	var found bool
	for _, e := range sym.Outbound {
		if e.Edge.Kind == model.EdgeInherits && e.Target.Qualified == "Base" && e.Target.ID == base.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Child→Base inherits edge not resolved across files; outbound=%+v", sym.Outbound)
	}
}

// TestScanResolvesAmbiguityByLowestID pins the placeholder resolver's
// deterministic tie-break: when multiple files declare the same
// qualified name, an edge targeting that name resolves to the
// lowest-ID match (= the file visited earliest by filepath.WalkDir,
// which walks lexically). Card 7 will replace this with scope-aware
// preference; this test guards the contract until then so a silent
// regression in Card 7 surfaces as a test change rather than a graph
// drift.
func TestScanResolvesAmbiguityByLowestID(t *testing.T) {
	root := t.TempDir()
	// Lexical order: a_base.rb visited first → lowest Base symbol id.
	writeFile(t, filepath.Join(root, "a_base.rb"), "class Base\n  def a\n  end\nend\n")
	writeFile(t, filepath.Join(root, "b_base.rb"), "class Base\n  def b\n  end\nend\n")
	writeFile(t, filepath.Join(root, "child.rb"), "class Child < Base\nend\n")

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var aBase, bBase, child model.Symbol
	for _, s := range all {
		switch s.Qualified {
		case "Base":
			if aBase.ID == 0 || s.ID < aBase.ID {
				bBase = aBase
				aBase = s
			} else {
				bBase = s
			}
		case "Child":
			child = s
		}
	}
	if aBase.ID == 0 || bBase.ID == 0 {
		t.Fatalf("expected two Base symbols, got aBase=%d bBase=%d", aBase.ID, bBase.ID)
	}
	if child.ID == 0 {
		t.Fatal("Child symbol missing")
	}

	sym, err := a.ReadSymbol(ctx, child.ID)
	if err != nil {
		t.Fatalf("ReadSymbol(Child): %v", err)
	}
	var resolvedTo int64
	for _, e := range sym.Outbound {
		if e.Edge.Kind == model.EdgeInherits {
			resolvedTo = e.Target.ID
			break
		}
	}
	if resolvedTo != aBase.ID {
		t.Errorf("Child inherits resolved to %d, want lowest-id Base=%d (the other was %d)", resolvedTo, aBase.ID, bBase.ID)
	}
}

// TestScanDropsUnresolvedEdges pins the flag-not-persist contract
// for unresolved edges: the row is dropped from sense_edges, the
// Result counts it under Unresolved, and a human-readable warning
// lands on the Warnings writer so a user debugging why their graph
// is sparse can see which target names went unmatched.
func TestScanDropsUnresolvedEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orphan.rb"), "class Orphan < NonExistent\nend\n")

	ctx := context.Background()
	var warnings bytes.Buffer
	res, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: &warnings,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Edges != 0 {
		t.Errorf("Edges = %d, want 0 (target NonExistent has no symbol)", res.Edges)
	}
	if res.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1 (the Orphan → NonExistent inherits edge)", res.Unresolved)
	}
	if !strings.Contains(warnings.String(), `unresolved target "NonExistent"`) {
		t.Errorf("expected unresolved-target warning, got: %q", warnings.String())
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var orphan model.Symbol
	for _, s := range all {
		if s.Qualified == "Orphan" {
			orphan = s
		}
	}
	if orphan.ID == 0 {
		t.Fatal("Orphan symbol missing")
	}
	sym, err := a.ReadSymbol(ctx, orphan.ID)
	if err != nil {
		t.Fatalf("ReadSymbol: %v", err)
	}
	if len(sym.Outbound) != 0 {
		t.Errorf("Orphan has outbound edges = %+v, want none", sym.Outbound)
	}
}

// TestScanEdgeConfidenceFlowsEndToEnd pins the confidence policy
// from emit to storage: a Ruby `send(:name)` dynamic-dispatch
// callsite is emitted with extract.ConfidenceDynamic (0.7); the
// resolver's unqualified-name fallback finds `Greeter#say` by name,
// and the row written to sense_edges carries confidence 0.7. This
// test locks the contract so a silent change in any layer
// (extractor, resolver, scan write path) shows up as a test
// failure rather than graph drift.
func TestScanEdgeConfidenceFlowsEndToEnd(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "greeter.rb"), `class Greeter
  def say
  end

  def dispatch
    send(:say)
  end
end
`)

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Edges == 0 {
		t.Fatalf("no edges resolved; expected Greeter#dispatch → Greeter#say")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var dispatch, say model.Symbol
	for _, s := range all {
		switch s.Qualified {
		case "Greeter#dispatch":
			dispatch = s
		case "Greeter#say":
			say = s
		}
	}
	if dispatch.ID == 0 || say.ID == 0 {
		t.Fatalf("symbols missing: dispatch=%d say=%d", dispatch.ID, say.ID)
	}

	sym, err := a.ReadSymbol(ctx, dispatch.ID)
	if err != nil {
		t.Fatalf("ReadSymbol(dispatch): %v", err)
	}
	var found bool
	for _, e := range sym.Outbound {
		if e.Target.ID != say.ID {
			continue
		}
		found = true
		if e.Edge.Kind != model.EdgeCalls {
			t.Errorf("dispatch→say edge Kind = %q, want %q", e.Edge.Kind, model.EdgeCalls)
		}
		if e.Edge.Confidence != extract.ConfidenceDynamic {
			t.Errorf("dispatch→say Confidence = %v, want %v (extract.ConfidenceDynamic)",
				e.Edge.Confidence, extract.ConfidenceDynamic)
		}
	}
	if !found {
		t.Error("expected Greeter#dispatch outbound edge to Greeter#say, none found")
	}
}

// TestScanUnresolvedWarningsCapped pins the warning-truncation
// behaviour: when more than maxUnresolvedWarnings unresolved edges
// accumulate, the first N land on the Warnings writer and a single
// "... and M more" summary line closes the stream. Result.Unresolved
// still reflects the full count.
func TestScanUnresolvedWarningsCapped(t *testing.T) {
	root := t.TempDir()
	// Thirty distinct unresolved targets — one inherits edge per
	// class, each pointing at a name that has no matching symbol.
	var src strings.Builder
	const total = 30
	for i := 0; i < total; i++ {
		fmt.Fprintf(&src, "class C%d < Missing%d\nend\n\n", i, i)
	}
	writeFile(t, filepath.Join(root, "orphans.rb"), src.String())

	var warnings bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: &warnings,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Unresolved != total {
		t.Errorf("Unresolved = %d, want %d", res.Unresolved, total)
	}
	lines := strings.Count(warnings.String(), "warn: unresolved target")
	if lines == total {
		t.Errorf("warnings not capped: %d per-edge lines, want cap around 20", lines)
	}
	if !strings.Contains(warnings.String(), "and 10 more unresolved targets omitted") {
		t.Errorf("expected truncation summary in warnings, got: %q", warnings.String())
	}
}

// TestScanEmitsTestsEdgesByFilenameConvention pins Card 12's test-
// association contract: a `widget.go` / `widget_test.go` pair in
// the same directory produces one `tests` edge per symbol in
// widget.go, sourced from a symbol in widget_test.go, with the
// canonical 0.8 confidence.
func TestScanEmitsTestsEdgesByFilenameConvention(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "widget.go"), `package widget

type Widget struct{}

func New() *Widget { return &Widget{} }
`)
	writeFile(t, filepath.Join(root, "widget_test.go"), `package widget

import "testing"

func TestNew(t *testing.T) {
	_ = New()
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var widget, newSym, testNew model.Symbol
	for _, s := range all {
		switch s.Qualified {
		case "widget.Widget":
			widget = s
		case "widget.New":
			newSym = s
		case "widget.TestNew":
			testNew = s
		}
	}
	if widget.ID == 0 || newSym.ID == 0 || testNew.ID == 0 {
		t.Fatalf("symbols missing: Widget=%d New=%d TestNew=%d", widget.ID, newSym.ID, testNew.ID)
	}

	// Expect tests edges landing on both top-level impl symbols
	// (Widget and New), sourced from a test-file symbol.
	for _, target := range []model.Symbol{widget, newSym} {
		sym, err := a.ReadSymbol(ctx, target.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(%q): %v", target.Qualified, err)
		}
		var testsEdge *model.EdgeRef
		for i := range sym.Inbound {
			if sym.Inbound[i].Edge.Kind == model.EdgeTests {
				testsEdge = &sym.Inbound[i]
				break
			}
		}
		if testsEdge == nil {
			t.Errorf("%q missing inbound tests edge", target.Qualified)
			continue
		}
		if testsEdge.Target.FileID != testNew.FileID {
			t.Errorf("%q tests edge source lives in file %d, want test file %d",
				target.Qualified, testsEdge.Target.FileID, testNew.FileID)
		}
		if testsEdge.Edge.Confidence != 0.8 {
			t.Errorf("%q tests edge confidence = %v, want 0.8", target.Qualified, testsEdge.Edge.Confidence)
		}
	}
}

// TestScanTolerantOfInvalidSource proves a single unparseable file
// doesn't abort the scan — just logs a warning and moves on.
func TestScanTolerantOfInvalidSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "good.go"), "package good\n\nfunc Hello() {}\n")
	// Broken Go file — tree-sitter still returns a tree with ERROR nodes,
	// and the extractor emits what it can. The scan should carry on.
	writeFile(t, filepath.Join(root, "broken.go"), "package broken\n\nfunc incompl")

	var summary, warnings bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &summary,
		Warnings: &warnings,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2", res.Files)
	}
	if !strings.Contains(warnings.String(), "parse errors present") {
		t.Errorf("expected parse-errors warning on Warnings writer, got: %q", warnings.String())
	}
	// Summary writer must be clean — warnings must not leak here.
	if strings.Contains(summary.String(), "parse errors") {
		t.Errorf("summary writer leaked warning text: %q", summary.String())
	}
}

// TestScan_IncrementalSkipsUnchanged confirms that a second scan with
// no file changes skips all files (hash match) and still produces the
// same index state.
func TestScan_IncrementalSkipsUnchanged(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")

	ctx := context.Background()
	first, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Changed != 1 {
		t.Errorf("first.Changed = %d, want 1", first.Changed)
	}

	second, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Skipped != 1 {
		t.Errorf("second.Skipped = %d, want 1", second.Skipped)
	}
	if second.Changed != 0 {
		t.Errorf("second.Changed = %d, want 0", second.Changed)
	}
}

// TestScan_DeletedFileCascade creates a fixture, scans, deletes a file,
// re-scans, and asserts no orphan rows in sense_symbols or sense_edges
// pointing at the deleted file.
func TestScan_DeletedFileCascade(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "base.rb"), "class Base\n  def hello\n  end\nend\n")
	writeFile(t, filepath.Join(root, "child.rb"), "class Child < Base\n  def greet\n  end\nend\n")

	ctx := context.Background()
	first, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Symbols == 0 {
		t.Fatal("no symbols after first scan")
	}

	// Delete base.rb and re-scan.
	if err := os.Remove(filepath.Join(root, "base.rb")); err != nil {
		t.Fatal(err)
	}
	second, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Removed != 1 {
		t.Errorf("second.Removed = %d, want 1", second.Removed)
	}

	// Open the index and verify no orphan symbols for base.rb.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, s := range all {
		if s.Qualified == "Base" || s.Qualified == "Base#hello" {
			t.Errorf("orphan symbol %q still in index after base.rb deleted", s.Qualified)
		}
	}

	// Verify no orphan edges — Child→Base inherits should be gone
	// since the target symbol was cascade-deleted.
	var child model.Symbol
	for _, s := range all {
		if s.Qualified == "Child" {
			child = s
		}
	}
	if child.ID != 0 {
		sym, err := a.ReadSymbol(ctx, child.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(Child): %v", err)
		}
		for _, e := range sym.Outbound {
			if e.Edge.Kind == model.EdgeInherits {
				t.Errorf("orphan edge Child→%s still in index", e.Target.Qualified)
			}
		}
	}
}

// TestScan_IgnoreExcludesFiles confirms that .senseignore and config
// ignore patterns exclude files from the scan.
func TestScan_IgnoreExcludesFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.rb"), "class App\nend\n")
	writeFile(t, filepath.Join(root, "vendor", "lib.rb"), "class Lib\nend\n")
	writeFile(t, filepath.Join(root, ".senseignore"), "vendor/\n")

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// vendor/ should be excluded — only app.rb should be indexed.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, s := range all {
		if s.Qualified == "Lib" {
			t.Error("Lib from vendor/ should not be in the index")
		}
	}
	if res.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 (only app.rb)", res.Indexed)
	}
}

// TestScan_SenseignoreRemovesFromIndex confirms that adding a path to
// .senseignore after an initial scan causes that file to be removed from
// the index on the next scan.
func TestScan_SenseignoreRemovesFromIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.rb"), "class App\nend\n")
	writeFile(t, filepath.Join(root, "extras", "lib.rb"), "class Lib\nend\n")

	ctx := context.Background()
	first, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if first.Indexed != 2 {
		t.Fatalf("first.Indexed = %d, want 2", first.Indexed)
	}

	// Now add .senseignore and re-scan.
	writeFile(t, filepath.Join(root, ".senseignore"), "extras/\n")
	second, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if second.Removed != 1 {
		t.Errorf("second.Removed = %d, want 1 (extras/lib.rb)", second.Removed)
	}
}

// TestScan_SizeCapSkipsLargeFiles confirms that files above the
// configured max_file_size_kb are not indexed.
func TestScan_SizeCapSkipsLargeFiles(t *testing.T) {
	root := t.TempDir()
	// Create config with a tiny size cap (1 KB).
	senseDir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(senseDir, "config.yml"), "scan:\n  max_file_size_kb: 1\n")

	// Small file — under the cap.
	writeFile(t, filepath.Join(root, "small.rb"), "class Small\nend\n")

	// Large file — over the 1 KB cap.
	big := "class Big\n" + strings.Repeat("  # padding\n", 200) + "end\n"
	writeFile(t, filepath.Join(root, "big.rb"), big)

	ctx := context.Background()
	var warnings bytes.Buffer
	res, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: &warnings,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 (only small.rb)", res.Indexed)
	}
	if !strings.Contains(warnings.String(), "big.rb: skipped") {
		t.Errorf("expected size-cap skip warning, got: %q", warnings.String())
	}
}

// TestScan_SecondScanIsFast is the acceptance criterion: a second
// sense scan on an unchanged repo completes in under 500ms.
func TestScan_SecondScanIsFast(t *testing.T) {
	root := t.TempDir()
	// Create a non-trivial fixture: 50 Go files with functions.
	for i := 0; i < 50; i++ {
		name := fmt.Sprintf("pkg%d.go", i)
		src := fmt.Sprintf("package pkg\n\nfunc F%d() {}\n", i)
		writeFile(t, filepath.Join(root, name), src)
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	start := time.Now()
	second, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	elapsed := time.Since(start)

	if second.Skipped != 50 {
		t.Errorf("Skipped = %d, want 50", second.Skipped)
	}
	if second.Changed != 0 {
		t.Errorf("Changed = %d, want 0", second.Changed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("second scan took %s, want < 500ms", elapsed)
	}
	t.Logf("second scan: %s (skipped=%d changed=%d)", elapsed, second.Skipped, second.Changed)
}

// ---- helpers ----

// buildTree creates the given relative file paths under root. Go
// files get a minimal `package <name>` body so tree-sitter parses
// cleanly. Non-Go files get placeholder content.
func buildTree(t *testing.T, root string, files []string) {
	t.Helper()
	for _, rel := range files {
		content := []byte("content")
		if strings.HasSuffix(rel, ".go") {
			// Package name derived from the file's parent directory
			// (or "pkg" for root-level files) — valid Go regardless.
			pkg := filepath.Base(filepath.Dir(rel))
			if pkg == "." || pkg == "/" {
				pkg = "pkg"
			}
			content = []byte("package " + pkg + "\n")
		}
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
