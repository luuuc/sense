package scan_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
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

	// First-run setup writes .mcp.json and CLAUDE.md, so the second
	// scan visits more files. The important invariants: indexed count
	// is stable and nothing was re-indexed.
	if first.Indexed != second.Indexed {
		t.Errorf("indexed count drifted between runs: first=%d second=%d",
			first.Indexed, second.Indexed)
	}
	if first.Symbols != second.Symbols {
		t.Errorf("symbol count drifted between runs: first=%d second=%d",
			first.Symbols, second.Symbols)
	}
	if second.Changed != 0 {
		t.Errorf("second.Changed = %d, want 0 (nothing should be re-indexed)", second.Changed)
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

func TestScanSecondRunSkipsSetup(t *testing.T) {
	root := t.TempDir()
	buildTree(t, root, []string{"a.go"})
	ctx := context.Background()

	// First run: setup fires, writes config files.
	var firstOut bytes.Buffer
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &firstOut, Warnings: io.Discard}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !strings.Contains(firstOut.String(), "Configuring") {
		t.Fatal("first run should print setup summary")
	}

	// Second run: setup should NOT fire.
	var secondOut bytes.Buffer
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &secondOut, Warnings: io.Discard}); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if strings.Contains(secondOut.String(), "Configuring") {
		t.Error("second run should not print setup summary")
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

	// Banner line "Indexing <root>..." followed by summary "scanned N files (...) in Xms"
	// optionally followed by edges, then the first-run AI tool integration summary.
	pattern := regexp.MustCompile(`^Indexing .+\.\.\.\nscanned 2 files \(\d+ indexed, \d+ changed, \d+ skipped\) in \S+\n(edges: \d+ resolved, \d+ unresolved, \d+ ambiguous\n)?`)
	if !pattern.MatchString(buf.String()) {
		t.Fatalf("output does not match summary pattern\nhave: %q",
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
// for unresolved edges: the row is dropped from sense_edges and the
// Result counts it under Unresolved.
func TestScanDropsUnresolvedEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "orphan.rb"), "class Orphan < NonExistent\nend\n")

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Edges != 0 {
		t.Errorf("Edges = %d, want 0 (target NonExistent has no symbol)", res.Edges)
	}
	if res.Unresolved != 1 {
		t.Errorf("Unresolved = %d, want 1 (the Orphan → NonExistent inherits edge)", res.Unresolved)
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

// TestScanEmitsTestsEdgeByRailsMirrorTree verifies that a Rails-style
// cross-directory spec file (spec/models/user_spec.rb) produces tests
// edges targeting the impl symbol in app/models/user.rb.
func TestScanEmitsTestsEdgeByRailsMirrorTree(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "app", "models", "user.rb"), "class User\nend\n")
	writeFile(t, filepath.Join(root, "spec", "models", "user_spec.rb"),
		"RSpec.describe User do\n  it 'works' do\n  end\nend\n")

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
	var user model.Symbol
	for _, s := range all {
		if s.Qualified == "User" {
			user = s
		}
	}
	if user.ID == 0 {
		t.Fatal("User symbol missing")
	}

	sym, err := a.ReadSymbol(ctx, user.ID)
	if err != nil {
		t.Fatalf("ReadSymbol: %v", err)
	}
	var found bool
	for _, ref := range sym.Inbound {
		if ref.Edge.Kind == model.EdgeTests {
			found = true
			break
		}
	}
	if !found {
		t.Error("User missing inbound tests edge from spec/models/user_spec.rb")
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

	var summary bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &summary,
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2", res.Files)
	}
	if res.Warnings == 0 {
		t.Error("expected at least one warning for broken.go parse errors")
	}
	// Summary should contain the hint line pointing to warnings.log.
	if !strings.Contains(summary.String(), "warnings — see .sense/warnings.log") {
		t.Errorf("summary missing warning hint line, got: %q", summary.String())
	}
	// But individual warning details must not leak into the summary.
	if strings.Contains(summary.String(), "parse errors") {
		t.Errorf("summary writer leaked warning text: %q", summary.String())
	}
	// Warnings log file should exist with grouped content.
	logPath := filepath.Join(root, ".sense", "warnings.log")
	logContent, lerr := os.ReadFile(logPath)
	if lerr != nil {
		t.Fatalf("expected warnings.log, got error: %v", lerr)
	}
	if !strings.Contains(string(logContent), "parse failed") {
		t.Errorf("warnings.log missing 'parse failed' group, got:\n%s", logContent)
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
	res, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Indexed != 1 {
		t.Errorf("Indexed = %d, want 1 (only small.rb)", res.Indexed)
	}
	if res.Warnings == 0 {
		t.Error("expected at least one warning for big.rb size skip")
	}
	logPath := filepath.Join(root, ".sense", "warnings.log")
	logContent, lerr := os.ReadFile(logPath)
	if lerr != nil {
		t.Fatalf("expected warnings.log, got error: %v", lerr)
	}
	if !strings.Contains(string(logContent), "file too large") {
		t.Errorf("warnings.log missing 'file too large' group, got:\n%s", logContent)
	}
}

// TestScan_SecondScanIsFast is the acceptance criterion: a second
// sense scan on an unchanged repo completes in under 1s.
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
	if elapsed > 1*time.Second {
		t.Errorf("second scan took %s, want < 1s", elapsed)
	}
	t.Logf("second scan: %s (skipped=%d changed=%d)", elapsed, second.Skipped, second.Changed)
}

func TestScanParallelDeterminism(t *testing.T) {
	root := t.TempDir()

	// Build a non-trivial tree with multiple languages so the parallel
	// parse exercises different extractors concurrently.
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\nfunc helper() {}\n")
	writeFile(t, filepath.Join(root, "lib/math.go"), "package lib\n\nfunc Add(a, b int) int { return a + b }\nfunc Sub(a, b int) int { return a - b }\n")
	writeFile(t, filepath.Join(root, "lib/strings.go"), "package lib\n\nfunc Concat(a, b string) string { return a + b }\n")
	writeFile(t, filepath.Join(root, "app.rb"), "class App\n  def run; end\n  def stop; end\nend\n")
	writeFile(t, filepath.Join(root, "models/user.rb"), "class User\n  def name; end\n  def email; end\nend\n")
	writeFile(t, filepath.Join(root, "index.ts"), "export function greet(name: string): string { return name; }\nexport function farewell(): void {}\n")
	writeFile(t, filepath.Join(root, "utils.py"), "def parse(data):\n    pass\n\ndef validate(data):\n    pass\n")

	ctx := context.Background()
	snapshots := make([]string, 2)

	for i := range snapshots {
		senseDir := filepath.Join(root, fmt.Sprintf(".sense-%d", i))
		_, err := scan.Run(ctx, scan.Options{
			Root:     root,
			Sense:    senseDir,
			Output:   io.Discard,
			Warnings: io.Discard,
		})
		if err != nil {
			t.Fatalf("scan %d: %v", i, err)
		}

		idx, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		rows, err := idx.DB().QueryContext(ctx,
			`SELECT s.qualified, s.kind, f.path
			 FROM sense_symbols s JOIN sense_files f ON s.file_id = f.id
			 ORDER BY s.qualified, s.kind`)
		if err != nil {
			_ = idx.Close()
			t.Fatalf("query %d: %v", i, err)
		}
		var buf bytes.Buffer
		for rows.Next() {
			var qualified, kind, path string
			if err := rows.Scan(&qualified, &kind, &path); err != nil {
				_ = rows.Close()
				_ = idx.Close()
				t.Fatalf("scan row %d: %v", i, err)
			}
			fmt.Fprintf(&buf, "%s\t%s\t%s\n", qualified, kind, path)
		}
		_ = rows.Close()
		_ = idx.Close()
		snapshots[i] = buf.String()
	}

	if snapshots[0] != snapshots[1] {
		t.Errorf("parallel scan produced non-deterministic results:\n--- scan 0 ---\n%s\n--- scan 1 ---\n%s",
			snapshots[0], snapshots[1])
	}
	if snapshots[0] == "" {
		t.Error("expected symbols to be extracted, got empty result")
	}
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

func TestScan_AddSenseToGitignore(t *testing.T) {
	t.Run("adds .sense/ when .gitignore exists without it", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
		var out bytes.Buffer
		_, err := scan.Run(context.Background(), scan.Options{
			Root:     root,
			Output:   &out,
			Warnings: io.Discard,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		content, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
		if !strings.Contains(string(content), ".sense/") {
			t.Error(".gitignore should contain .sense/")
		}
		if !strings.Contains(out.String(), "added .sense/ to .gitignore") {
			t.Error("expected message about adding .sense/ to .gitignore")
		}
	})

	t.Run("no double blank line when .gitignore ends with newline", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n")
		_, err := scan.Run(context.Background(), scan.Options{
			Root:     root,
			Output:   io.Discard,
			Warnings: io.Discard,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		content, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
		if strings.Contains(string(content), "\n\n#") {
			t.Error(".gitignore should not have double blank line before comment")
		}
	})

	t.Run("skips when .sense/ already present", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
		writeFile(t, filepath.Join(root, ".gitignore"), "*.log\n.sense/\n")
		before, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
		_, err := scan.Run(context.Background(), scan.Options{
			Root:     root,
			Output:   io.Discard,
			Warnings: io.Discard,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		after, _ := os.ReadFile(filepath.Join(root, ".gitignore"))
		if string(before) != string(after) {
			t.Error(".gitignore should not be modified when .sense/ already present")
		}
	})

	t.Run("no-op when .gitignore absent", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
		_, err := scan.Run(context.Background(), scan.Options{
			Root:     root,
			Output:   io.Discard,
			Warnings: io.Discard,
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, ".gitignore")); err == nil {
			t.Error(".gitignore should not be created when absent")
		}
	})
}

func TestTemporalCouplingNoOpWithoutGit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.rb"), "class A; end\n")
	writeFile(t, filepath.Join(root, "sub", "b.rb"), "class B; end\n")

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Indexed < 2 {
		t.Fatalf("Indexed = %d, want >= 2", res.Indexed)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	edges, err := adapter.EdgesOfKind(ctx, model.EdgeTemporal)
	if err != nil {
		t.Fatalf("EdgesOfKind: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 temporal edges without git, got %d", len(edges))
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

func TestScan_EmptyProject(t *testing.T) {
	root := t.TempDir()
	// No files at all — walkTree should handle len(entries)==0 gracefully.
	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 0 {
		t.Errorf("Files = %d, want 0", res.Files)
	}
	if res.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0", res.Indexed)
	}
	if res.Symbols != 0 {
		t.Errorf("Symbols = %d, want 0", res.Symbols)
	}
}

func TestScan_EmptyProjectNoSupportedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "readme.txt"), "just a text file\n")
	writeFile(t, filepath.Join(root, "data.csv"), "a,b,c\n1,2,3\n")

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2 (counted but not indexed)", res.Files)
	}
	if res.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0 (no supported languages)", res.Indexed)
	}
}

func TestScan_ContextCancelled(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)),
			"package pkg\n\nfunc F() {}\n")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestScan_QuietSuppressesWarningHint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "good.go"), "package a\n\nfunc Hello() {}\n")
	writeFile(t, filepath.Join(root, "broken.go"), "package a\n\nfunc incompl")

	var out bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &out,
		Warnings: io.Discard,
		Quiet:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Warnings == 0 {
		t.Fatal("expected warnings from broken.go")
	}
	if bytes.Contains(out.Bytes(), []byte("warnings — see")) {
		t.Error("quiet mode should suppress warning hint in output")
	}
}

func TestScan_SatisfyInterfaces(t *testing.T) {
	root := t.TempDir()

	// Interface with two methods.
	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

type Greeter interface {
	Hello() string
	Goodbye() string
}
`)

	// Struct satisfying the interface.
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type EnglishGreeter struct{}

func (e *EnglishGreeter) Hello() string {
	return "hello"
}

func (e *EnglishGreeter) Goodbye() string {
	return "goodbye"
}
`)

	ctx := context.Background()
	_, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Look for an inherits edge from EnglishGreeter → Greeter.
	var greeterID int64
	var structID int64
	err = a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Greeter' AND kind = 'interface'`).Scan(&greeterID)
	if err != nil {
		t.Fatalf("query Greeter: %v", err)
	}
	err = a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'EnglishGreeter' AND kind = 'class'`).Scan(&structID)
	if err != nil {
		t.Fatalf("query EnglishGreeter: %v", err)
	}

	var count int
	err = a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE source_id = ? AND target_id = ? AND kind = 'inherits'`,
		structID, greeterID).Scan(&count)
	if err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if count == 0 {
		t.Error("expected inherits edge from EnglishGreeter → Greeter (interface satisfaction)")
	}
}

func TestScan_SatisfyInterfacesSkipsEmptyMethodInterface(t *testing.T) {
	// A marker interface (no methods) must not produce an inherits edge
	// from arbitrary structs — methodSetSatisfies returns true for the
	// empty required set, so the empty-methods skip at satisfy.go:139 is
	// what protects against universal "everyone implements Marker" noise.
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "marker.go"), `package mylib

type Marker interface{}

type Foo struct{}
func (f *Foo) Hello() string { return "hi" }
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

	var markerID, fooID int64
	if err := a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Marker' AND kind = 'interface'`).Scan(&markerID); err != nil {
		t.Fatalf("query Marker: %v", err)
	}
	if err := a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Foo' AND kind = 'class'`).Scan(&fooID); err != nil {
		t.Fatalf("query Foo: %v", err)
	}

	var count int
	if err := a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE source_id = ? AND target_id = ? AND kind = 'inherits'`,
		fooID, markerID).Scan(&count); err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if count != 0 {
		t.Errorf("Foo should not inherit empty-method Marker (got %d edges)", count)
	}
}

func TestScan_RunDefaultsRootAndIOWriters(t *testing.T) {
	// Cover the zero-value defaults in Run: Root="" → ".", Output=nil →
	// os.Stderr, Warnings=nil → os.Stderr (scan.go:109-111, 121-127).
	// Run from a tempdir so it doesn't touch the developer's tree.
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	// Redirect stderr so the default-output write doesn't pollute test output.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = origStderr
		_ = r.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, r) }()

	if _, err := scan.Run(context.Background(), scan.Options{}); err != nil {
		t.Fatalf("Run with zero options: %v", err)
	}
	_ = w.Close()
}

func TestScan_RunHonorsSENSEDIR(t *testing.T) {
	// Cover the SENSE_DIR env-override branch (scan.go:114-116) when
	// opts.Sense is empty.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	senseDir := filepath.Join(t.TempDir(), "sense-override")
	t.Setenv("SENSE_DIR", senseDir)
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(senseDir, "index.db")); err != nil {
		t.Errorf("expected index at SENSE_DIR override: %v", err)
	}
}

func TestScan_RunSkipsSymlinks(t *testing.T) {
	// Cover the symlink-skip branch in walkTree (scan.go:584-586).
	root := t.TempDir()
	target := filepath.Join(root, "real.go")
	writeFile(t, target, "package p\n")
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("expected 1 indexed file (symlink skipped), got %d", res.Files)
	}
}

func TestScan_RunHonorsSENSEMaxFileSize(t *testing.T) {
	// Cover the SENSE_MAX_FILE_SIZE env-override branch (scan.go:141-145).
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	t.Setenv("SENSE_MAX_FILE_SIZE", "256")
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestScan_TemporalCouplingInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()

	// Initialize a git repo with enough co-changing files.
	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitCmd("init")
	gitCmd("config", "user.email", "test@test.com")
	gitCmd("config", "user.name", "test")

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(root, "lib", "b.go"), "package lib\n\nfunc B() {}\n")

	// Create minCoChanges (3) commits where both files change together.
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(root, "pkg", "a.go"),
			fmt.Sprintf("package pkg\n\n// v%d\nfunc A() {}\n", i))
		writeFile(t, filepath.Join(root, "lib", "b.go"),
			fmt.Sprintf("package lib\n\n// v%d\nfunc B() {}\n", i))
		gitCmd("add", "-A")
		gitCmd("commit", "-m", fmt.Sprintf("co-change %d", i))
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Should have temporal edges for the co-changing files.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var edgeCount int
	err = a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE kind = 'temporal'`).Scan(&edgeCount)
	if err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if edgeCount == 0 {
		t.Error("expected temporal edges from co-changing files")
	}
}

func TestScan_DefaultIgnoredCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "export default {}\n")

	var out bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &out,
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DefaultIgnored == 0 {
		t.Error("expected node_modules to be counted as default-ignored")
	}
	if !bytes.Contains(out.Bytes(), []byte("skipped")) {
		t.Errorf("output should mention skipped directories: %q", out.String())
	}
}
