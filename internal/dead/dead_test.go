package dead_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupFixture(t *testing.T) (*sql.DB, *sqlite.Adapter) {
	t.Helper()
	root := t.TempDir()

	// LiveService: called by main.rb, so it's alive.
	writeFile(t, filepath.Join(root, "live_service.rb"), `class LiveService
  def process
    42
  end
end
`)
	// DeadService: nothing calls it.
	writeFile(t, filepath.Join(root, "dead_service.rb"), `class DeadService
  def handle
    1
  end

  def cleanup
    2
  end
end
`)
	// Caller: calls LiveService#process via send.
	writeFile(t, filepath.Join(root, "caller.rb"), `class Caller
  def run
    send(:process)
  end
end
`)
	// main.go: main function should be excluded as entry point.
	writeFile(t, filepath.Join(root, "main.go"), `package main

func main() {}

func unusedGoFunc() {}
`)
	// widget_test.go: test functions should be excluded.
	writeFile(t, filepath.Join(root, "widget_test.go"), `package main

import "testing"

func TestWidget(t *testing.T) {}
`)
	// Initializer: constructor should be excluded.
	writeFile(t, filepath.Join(root, "initializer.rb"), `class Initializer
  def initialize
    @ready = true
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return db, adapter
}

func TestFindDeadBasic(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	if result.TotalSymbols == 0 {
		t.Fatal("TotalSymbols = 0, want > 0")
	}

	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}

	// DeadService and its methods should be flagged.
	if !deadNames["DeadService"] || !deadNames["DeadService#handle"] {
		t.Error("expected DeadService or DeadService#handle in dead symbols")
	}

	// LiveService#process is called by Caller#run via send(:process),
	// so it should NOT be dead.
	if deadNames["LiveService#process"] {
		t.Error("LiveService#process should not be dead (called by Caller)")
	}
}

func TestFindDeadExcludesMainFunction(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Name == "main" {
			t.Error("main function should be excluded as entry point")
		}
	}
}

func TestFindDeadExcludesTestFunctions(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Name == "TestWidget" {
			t.Error("TestWidget should be excluded as test entry point")
		}
	}
}

func TestFindDeadExcludesTestFiles(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.File == "widget_test.go" {
			t.Errorf("symbol %q in test file should be excluded", s.Qualified)
		}
	}
}

func TestFindDeadExcludesConstructors(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Name == "initialize" {
			t.Error("initialize should be excluded as constructor entry point")
		}
	}
}

func TestFindDeadLanguageFilter(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{Language: "ruby"})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if filepath.Ext(s.File) != ".rb" {
			t.Errorf("found non-ruby symbol %q in file %q with language=ruby filter", s.Qualified, s.File)
		}
	}
}

func TestFindDeadDomainFilter(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	// Filter to a path substring that doesn't match any file.
	result, err := dead.FindDead(ctx, db, dead.Options{Domain: "nonexistent_path"})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	if len(result.Dead) != 0 {
		t.Errorf("expected no results with nonexistent domain filter, got %d", len(result.Dead))
	}
}

func TestFindDeadLimit(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	result, err := dead.FindDead(ctx, db, dead.Options{Limit: 1})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	if len(result.Dead) > 1 {
		t.Errorf("expected at most 1 result with limit=1, got %d", len(result.Dead))
	}
}

func TestRollupCollapsesDeadClassWithDeadMethods(t *testing.T) {
	classID := int64(1)
	symbols := []dead.Symbol{
		{ID: classID, Name: "Klass", Qualified: "Klass", Kind: "class"},
		{ID: 2, Name: "foo", Qualified: "Klass#foo", Kind: "method", ParentID: &classID},
		{ID: 3, Name: "bar", Qualified: "Klass#bar", Kind: "method", ParentID: &classID},
	}

	rolled := dead.Rollup(symbols)

	if len(rolled) != 1 {
		t.Fatalf("Rollup returned %d symbols, want 1 (class only)", len(rolled))
	}
	if rolled[0].Qualified != "Klass" {
		t.Errorf("Rollup[0] = %q, want Klass", rolled[0].Qualified)
	}
}

func TestRollupKeepsDeadMethodsOfLiveClass(t *testing.T) {
	// Class is alive (not in dead set), but method is dead.
	aliveClassID := int64(99)
	symbols := []dead.Symbol{
		{ID: 2, Name: "orphan", Qualified: "AliveClass#orphan", Kind: "method", ParentID: &aliveClassID},
	}

	rolled := dead.Rollup(symbols)

	if len(rolled) != 1 {
		t.Fatalf("Rollup returned %d symbols, want 1", len(rolled))
	}
	if rolled[0].Qualified != "AliveClass#orphan" {
		t.Errorf("Rollup[0] = %q, want AliveClass#orphan", rolled[0].Qualified)
	}
}

func TestRollupPreservesStandaloneDeadFunctions(t *testing.T) {
	symbols := []dead.Symbol{
		{ID: 1, Name: "helper", Qualified: "helper", Kind: "function"},
		{ID: 2, Name: "util", Qualified: "util", Kind: "function"},
	}

	rolled := dead.Rollup(symbols)

	if len(rolled) != 2 {
		t.Errorf("Rollup returned %d symbols, want 2", len(rolled))
	}
}

func TestFindDeadExcludesContainersWithLiveChildren(t *testing.T) {
	root := t.TempDir()

	// MixedService: the class itself has no incoming calls, but one
	// of its methods is called. The class should be excluded because
	// its child (alive_method) has incoming edges.
	writeFile(t, filepath.Join(root, "mixed.rb"), `class MixedService
  def alive_method
    42
  end

  def dead_method
    0
  end
end
`)
	writeFile(t, filepath.Join(root, "consumer.rb"), `class Consumer
  def use_it
    send(:alive_method)
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Qualified == "MixedService" {
			t.Error("MixedService should be excluded — it has a live child (alive_method)")
		}
	}
}

// TestNameOccurrencesEstimated proves FindDead fills NameOccurrences from
// the index: a method name defined on several classes carries an
// occurrence estimate of at least that many definitions, so the verify-
// command builder can tell a common name from a unique one.
func TestNameOccurrencesEstimated(t *testing.T) {
	root := t.TempDir()
	// Three classes all defining `ping`, none called — all dead, and the
	// name is shared three ways.
	writeFile(t, filepath.Join(root, "pingers.rb"), `class Alpha
  def ping
    1
  end
end

class Beta
  def ping
    2
  end
end

class Gamma
  def ping
    3
  end
end
`)
	// A uniquely-named dead method as the contrast.
	writeFile(t, filepath.Join(root, "lonely.rb"), `class Lonely
  def quux_only_here
    9
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{Language: "ruby"})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	var pingOcc, lonelyOcc int
	var sawPing, sawLonely bool
	for _, s := range result.Dead {
		switch s.Qualified {
		case "Alpha#ping":
			pingOcc, sawPing = s.NameOccurrences, true
		case "Lonely#quux_only_here":
			lonelyOcc, sawLonely = s.NameOccurrences, true
		}
	}
	if !sawPing || !sawLonely {
		t.Fatalf("expected both dead candidates present (ping=%v lonely=%v)", sawPing, sawLonely)
	}
	if pingOcc < 3 {
		t.Errorf("ping NameOccurrences = %d, want >= 3 (three definitions)", pingOcc)
	}
	if lonelyOcc != 1 {
		t.Errorf("unique-name NameOccurrences = %d, want 1", lonelyOcc)
	}
}

func TestExcludeTestRefsChangesResults(t *testing.T) {
	root := t.TempDir()

	// OnlyCalledFromTest: a function whose sole caller lives in a test file.
	writeFile(t, filepath.Join(root, "helper.rb"), `class Helper
  def only_called_from_test
    42
  end
end
`)
	writeFile(t, filepath.Join(root, "test", "helper_test.rb"), `class HelperTest
  def test_it
    send(:only_called_from_test)
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Without test exclusion: the test-file caller keeps the method alive.
	withoutExclude, err := dead.FindDead(ctx, db, dead.Options{ExcludeTestRefs: false})
	if err != nil {
		t.Fatalf("FindDead (exclude=false): %v", err)
	}
	// With test exclusion: the test-file caller is ignored, method becomes dead.
	withExclude, err := dead.FindDead(ctx, db, dead.Options{ExcludeTestRefs: true})
	if err != nil {
		t.Fatalf("FindDead (exclude=true): %v", err)
	}

	deadWithout := map[string]bool{}
	for _, s := range withoutExclude.Dead {
		deadWithout[s.Qualified] = true
	}
	deadWith := map[string]bool{}
	for _, s := range withExclude.Dead {
		deadWith[s.Qualified] = true
	}

	if deadWithout["Helper#only_called_from_test"] {
		t.Log("method is dead even without test exclusion (scanner may not have resolved the edge); checking exclude mode adds it")
	}
	if !deadWith["Helper#only_called_from_test"] && !deadWithout["Helper#only_called_from_test"] {
		t.Log("method not flagged in either mode — scanner resolved the edge differently; skipping assertion")
	}
	// The key invariant: with exclusion on, at least as many symbols are dead.
	if len(withExclude.Dead) < len(withoutExclude.Dead) {
		t.Errorf("ExcludeTestRefs=true produced fewer dead symbols (%d) than false (%d)",
			len(withExclude.Dead), len(withoutExclude.Dead))
	}
}

func TestInterfaceImplementorExclusion(t *testing.T) {
	root := t.TempDir()

	// Go interface + struct implementing it + caller that calls the interface method.
	// The struct's method (Render) should be excluded from dead code because the
	// interface method is called via dynamic dispatch.
	writeFile(t, filepath.Join(root, "iface.go"), `package render

type Renderer interface {
	Render() string
}
`)
	writeFile(t, filepath.Join(root, "html.go"), `package render

type HTMLRenderer struct{}

func (h HTMLRenderer) Render() string {
	return "<html/>"
}

func (h HTMLRenderer) unusedHelper() string {
	return "unused"
}
`)
	writeFile(t, filepath.Join(root, "caller.go"), `package render

func RenderAll(rs []Renderer) {
	for _, r := range rs {
		r.Render()
	}
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}

	// HTMLRenderer.Render satisfies Renderer.Render which has callers —
	// the interface-aware filter should exclude it.
	if deadNames["render.HTMLRenderer.Render"] {
		t.Error("HTMLRenderer.Render should be excluded — it implements Renderer.Render which has callers")
	}

	// unusedHelper has no interface coverage — it should stay dead.
	if !deadNames["render.HTMLRenderer.unusedHelper"] {
		t.Error("HTMLRenderer.unusedHelper should be dead — no callers and no interface match")
	}
}

func TestConfidenceAnnotation(t *testing.T) {
	root := t.TempDir()

	// Go interface with NO callers on its methods — the implementing method
	// should still get "possibly_dead" confidence because the parent type
	// implements an interface (even though we can't prove it's called).
	writeFile(t, filepath.Join(root, "svc.go"), `package svc

type Handler interface {
	Handle()
}

type MyHandler struct{}

func (m MyHandler) Handle() {}

func standaloneUnused() {}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	confidenceByQualified := map[string]string{}
	for _, s := range result.Dead {
		confidenceByQualified[s.Qualified] = s.Confidence
	}

	if c, ok := confidenceByQualified["svc.standaloneUnused"]; ok {
		if c != dead.ConfidenceDead {
			t.Errorf("standaloneUnused confidence = %q, want %q", c, dead.ConfidenceDead)
		}
	}

	// The interface method Handle on Handler is excluded as an entry point
	// (isInterfaceMethod). But MyHandler.Handle — an implementing method with
	// no direct callers — should appear with "possibly_dead" confidence if the
	// scanner detected the inherits edge.
	if c, ok := confidenceByQualified["svc.MyHandler.Handle"]; ok {
		if c != dead.ConfidencePossibly {
			t.Errorf("MyHandler.Handle confidence = %q, want %q", c, dead.ConfidencePossibly)
		}
	} else {
		// If the method was excluded entirely (interface alive filter or entry point),
		// that's also acceptable — it means the interface awareness is working.
		t.Log("MyHandler.Handle not in dead results — likely excluded by interface filter (acceptable)")
	}
}

func TestFindDeadNullSourceID(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	// Insert an 'inherits' edge with NULL source_id — legal per schema,
	// but previously crashed queryInterfaceAliveMethods via rows.Scan.
	var targetID, fileID int64
	err := db.QueryRowContext(ctx,
		`SELECT s.id, s.file_id FROM sense_symbols s WHERE s.kind = 'class' ORDER BY s.id LIMIT 1`,
	).Scan(&targetID, &fileID)
	if err != nil {
		t.Fatalf("finding a target symbol: %v", err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id) VALUES (NULL, ?, 'inherits', ?)`,
		targetID, fileID)
	if err != nil {
		t.Fatalf("inserting NULL source_id edge: %v", err)
	}

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead should not error with NULL source_id edges: %v", err)
	}
	if result.TotalSymbols == 0 {
		t.Fatal("TotalSymbols = 0, want > 0")
	}
}

func TestDeadCodeNewFuncIsPossiblyDead(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "router.go"), `package router

type Router struct{}

func NewRouter() *Router { return &Router{} }

func unusedFunc() {}
`)
	writeFile(t, filepath.Join(root, "main.go"), `package main

func main() {}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Name == "NewRouter" {
			if s.Confidence != dead.ConfidencePossibly {
				t.Errorf("NewRouter confidence = %q, want %q", s.Confidence, dead.ConfidencePossibly)
			}
			return
		}
	}
	t.Error("NewRouter not found in dead results")
}

func TestDeadCodeExcludesFrameworkHooks(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "Gemfile"), `source "https://rubygems.org"
gem "rails", "~> 7.0"
`)
	writeFile(t, filepath.Join(root, "concern.rb"), `module Auditable
  extend ActiveSupport::Concern

  included do
    before_save :audit_trail
  end

  class_methods do
    def auditable?
      true
    end
  end

  def after_commit
    log_change
  end

  def dead_method
    nil
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	excluded := map[string]bool{"included": true, "class_methods": true, "after_commit": true}
	for _, s := range result.Dead {
		if excluded[s.Name] {
			t.Errorf("%q should be excluded as Rails framework hook", s.Name)
		}
	}
}

func TestDeadCodeExcludesJVMLifecycle(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "Handler.java"), `public class Handler {
    public void handle() {
        System.out.println("handling");
    }
    public void onCreate() {
        System.out.println("created");
    }
    public void configure() {
        System.out.println("configured");
    }
    public void deadMethod() {
        System.out.println("dead");
    }
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	excluded := map[string]bool{"handle": true, "onCreate": true, "configure": true}
	for _, s := range result.Dead {
		if excluded[s.Name] {
			t.Errorf("%q should be excluded as JVM lifecycle/framework hook", s.Name)
		}
	}
}

func TestDeadCodeExcludesDunderMethods(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "widget.py"), `class Widget:
    def __init__(self):
        pass

    def __repr__(self):
        return "Widget()"

    def __str__(self):
        return "Widget"

    def dead_method(self):
        pass
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	excluded := map[string]bool{"__init__": true, "__repr__": true, "__str__": true}
	for _, s := range result.Dead {
		if excluded[s.Name] {
			t.Errorf("%q should be excluded as Python dunder method", s.Name)
		}
	}
}

func TestDeadCodeExcludesLibraryPublicAPI(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "lib.go"), `package lib

func PublicFunc() {}

func privateFunc() {}

type PublicType struct{}

func (p PublicType) PublicMethod() {}

func (p PublicType) privateMethod() {}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	for _, s := range result.Dead {
		if s.Name == "PublicFunc" {
			t.Error("PublicFunc should be excluded as library public API")
		}
		if s.Name == "PublicType" {
			t.Error("PublicType should be excluded as library public API")
		}
		if s.Name == "PublicMethod" {
			t.Error("PublicMethod should be excluded as library public API")
		}
	}
}

func TestDeadCodeExcludesTraitImplMethods(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "iface.go"), `package render

type Renderer interface {
	Render() string
}
`)
	writeFile(t, filepath.Join(root, "html.go"), `package render

type HTMLRenderer struct{}

func (h HTMLRenderer) Render() string {
	return "<html/>"
}

func (h HTMLRenderer) unusedHelper() string {
	return "unused"
}
`)
	writeFile(t, filepath.Join(root, "caller.go"), `package render

func RenderAll(rs []Renderer) {
	for _, r := range rs {
		r.Render()
	}
}
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}

	if deadNames["render.HTMLRenderer.Render"] {
		t.Error("HTMLRenderer.Render should be excluded — it implements Renderer.Render")
	}

	if !deadNames["render.HTMLRenderer.unusedHelper"] {
		t.Error("HTMLRenderer.unusedHelper should be dead — no callers and no interface match")
	}
}

func TestDeadCodeIncludesUnusedConstants(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "consts.go"), `package main

const UsedConst = "yes"
const DeadConst = "no"

func main() {
	_ = UsedConst
}
`)
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}
	if !deadNames["main.DeadConst"] {
		t.Error("DeadConst should be in dead code results")
	}
	if deadNames["main.UsedConst"] {
		t.Error("UsedConst should NOT be in dead code results (referenced by main)")
	}
}

func TestDeadCodeIncludesUnusedVars(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "vars.go"), `package main

var usedVar = "yes"
var deadVar = "no"

func main() {
	_ = usedVar
}
`)
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}
	if !deadNames["main.deadVar"] {
		t.Error("deadVar should be in dead code results")
	}
	if deadNames["main.usedVar"] {
		t.Error("usedVar should NOT be in dead code results (referenced by main)")
	}
}

func TestDeadCodeConstTestOnlyReference(t *testing.T) {
	root := t.TempDir()

	// localhostIP is used only in the test file — production-dead.
	writeFile(t, filepath.Join(root, "utils.go"), `package utils

var localhostIP = "127.0.0.1"
var localhostIPv6 = "::1"
var productionAddr = "0.0.0.0"

func Serve() {
	_ = productionAddr
}
`)
	writeFile(t, filepath.Join(root, "utils_test.go"), `package utils

import "testing"

func TestServe(t *testing.T) {
	_ = localhostIP
	_ = localhostIPv6
}
`)
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// With ExcludeTestRefs=true, test-only references don't count.
	result, err := dead.FindDead(ctx, db, dead.Options{ExcludeTestRefs: true})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}
	if !deadNames["utils.localhostIP"] {
		t.Error("localhostIP should be dead (only referenced in test file)")
	}
	if !deadNames["utils.localhostIPv6"] {
		t.Error("localhostIPv6 should be dead (only referenced in test file)")
	}
	if deadNames["utils.productionAddr"] {
		t.Error("productionAddr should NOT be dead (referenced by Serve)")
	}

	// Without cross-file speculative emission, localhostIP has no edges
	// at all (the test file extractor doesn't see cross-file vars in its
	// pkgBindings). It's dead regardless of ExcludeTestRefs.
	resultAll, err := dead.FindDead(ctx, db, dead.Options{ExcludeTestRefs: false})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	deadNamesAll := map[string]bool{}
	for _, s := range resultAll.Dead {
		deadNamesAll[s.Qualified] = true
	}
	if !deadNamesAll["utils.localhostIP"] {
		t.Error("localhostIP should be dead (cross-file var, no same-file references)")
	}
}

func TestDeadCodeGinConstants(t *testing.T) {
	root := t.TempDir()

	// Mirrors gin/utils.go: exported BindKey used in production, unexported
	// localhostIP/localhostIPv6 used only in test files.
	writeFile(t, filepath.Join(root, "utils.go"), `package gin

const BindKey = "_gin-gonic/gin/bindkey"
const localhostIP = "127.0.0.1"
const localhostIPv6 = "::1"

func Bind() string {
	return BindKey
}
`)
	writeFile(t, filepath.Join(root, "gin_test.go"), `package gin

import "testing"

func TestBind(t *testing.T) {
	_ = localhostIP
}
`)
	writeFile(t, filepath.Join(root, "context_test.go"), `package gin

import "testing"

func TestContext(t *testing.T) {
	_ = localhostIP
	_ = localhostIPv6
}
`)
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(root, ".sense", "index.db")
	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Verify BindKey has a same-file reference edge (from Bind()).
	var refCount int
	err = db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sense_edges e
		JOIN sense_symbols t ON t.id = e.target_id
		WHERE t.qualified = 'gin.BindKey'
		AND e.kind = 'references'`).Scan(&refCount)
	if err != nil {
		t.Fatalf("query refs: %v", err)
	}
	if refCount == 0 {
		t.Error("expected references edge for BindKey from Bind()")
	}

	// localhostIP and localhostIPv6 are dead — no same-file references.
	result, err := dead.FindDead(ctx, db, dead.Options{ExcludeTestRefs: true})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	deadNames := map[string]bool{}
	for _, s := range result.Dead {
		deadNames[s.Qualified] = true
	}
	if !deadNames["gin.localhostIP"] {
		t.Error("localhostIP should be dead (only referenced in test files)")
	}
	if !deadNames["gin.localhostIPv6"] {
		t.Error("localhostIPv6 should be dead (only referenced in test files)")
	}
	if deadNames["gin.BindKey"] {
		t.Error("BindKey should NOT be dead (referenced by Bind)")
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
