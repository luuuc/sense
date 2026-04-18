package scan_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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

	// Output is the summary line only — warnings now live on a separate
	// writer, so the pattern no longer has to tolerate interleaved diagnostics.
	pattern := regexp.MustCompile(`^2 files, \d+ indexed, \d+ symbols, \d+ edges in \S+\n\z`)
	if !pattern.MatchString(buf.String()) {
		t.Fatalf("output does not match summary pattern\nhave: %q\nwant: 2 files, N indexed, N symbols, N edges in D\\n",
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
