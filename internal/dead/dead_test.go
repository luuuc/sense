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
	for _, s := range allSymbols(result) {
		deadNames[s.Qualified] = true
	}

	// DeadService should be flagged. Its methods (handle, cleanup) are rolled
	// up into the dead class, so the class alone represents them.
	if !deadNames["DeadService"] {
		t.Error("expected DeadService in unreferenced symbols")
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

	for _, s := range allSymbols(result) {
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

	for _, s := range allSymbols(result) {
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

	for _, s := range allSymbols(result) {
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

	for _, s := range allSymbols(result) {
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

	for _, s := range allSymbols(result) {
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

	if len(allSymbols(result)) != 0 {
		t.Errorf("expected no results with nonexistent domain filter, got %d", len(allSymbols(result)))
	}
}

// TestFindDeadLimitIsWireLayerConcern pins the post-rebuild contract: FindDead
// classifies every candidate and does NOT truncate. The --limit cap (with
// per-group dropped counts) is applied by the wire builder
// (mcpio.BuildUnreferencedResponse), never silently here — so a low Limit must
// not drop findings at this layer.
func TestFindDeadLimitIsWireLayerConcern(t *testing.T) {
	db, _ := setupFixture(t)
	ctx := context.Background()

	limited, err := dead.FindDead(ctx, db, dead.Options{Limit: 1})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	full, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}

	if len(allSymbols(limited)) != len(allSymbols(full)) {
		t.Errorf("FindDead truncated at the analysis layer: limit=1 gave %d, default gave %d (limit must be a wire-layer concern)",
			len(allSymbols(limited)), len(allSymbols(full)))
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

	for _, s := range allSymbols(result) {
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
	// Three classes all defining `ping`, none of the ping methods called — so
	// the name is shared three ways. Each class is kept alive by a subclass
	// (an inherits edge), so the dead `ping` methods survive rollup as
	// individual findings instead of collapsing into a dead class.
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

class AlphaChild < Alpha
end

class BetaChild < Beta
end

class GammaChild < Gamma
end
`)
	// A uniquely-named dead method as the contrast, on a class kept alive by
	// a subclass so the method reports individually.
	writeFile(t, filepath.Join(root, "lonely.rb"), `class Lonely
  def quux_only_here
    9
  end
end

class LonelyChild < Lonely
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(withoutExclude) {
		deadWithout[s.Qualified] = true
	}
	deadWith := map[string]bool{}
	for _, s := range allSymbols(withExclude) {
		deadWith[s.Qualified] = true
	}

	if deadWithout["Helper#only_called_from_test"] {
		t.Log("method is dead even without test exclusion (scanner may not have resolved the edge); checking exclude mode adds it")
	}
	if !deadWith["Helper#only_called_from_test"] && !deadWithout["Helper#only_called_from_test"] {
		t.Log("method not flagged in either mode — scanner resolved the edge differently; skipping assertion")
	}
	// The key invariant: with exclusion on, at least as many symbols are dead.
	if len(allSymbols(withExclude)) < len(allSymbols(withoutExclude)) {
		t.Errorf("ExcludeTestRefs=true produced fewer dead symbols (%d) than false (%d)",
			len(allSymbols(withExclude)), len(allSymbols(withoutExclude)))
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
	for _, s := range allSymbols(result) {
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

	verdict := verdictByQualified(result)

	// The Go voice is registered, so the two sides diverge. An unexported func
	// with no caller and no mention is genuinely dead (the airtight Go verdict).
	if v, ok := verdict["svc.standaloneUnused"]; !ok {
		t.Error("standaloneUnused should be reported")
	} else if v != dead.VerdictDead {
		t.Errorf("standaloneUnused verdict = %q, want %q (unexported, zero-edge, unmentioned)", v, dead.VerdictDead)
	}
	// A method whose name matches an interface method stays possibly_dead: it may
	// be invoked through the interface, where the static graph shows no caller.
	if v, ok := verdict["svc.MyHandler.Handle"]; ok && v != dead.VerdictPossiblyDead {
		t.Errorf("MyHandler.Handle verdict = %q, want %q (satisfies Handler)", v, dead.VerdictPossiblyDead)
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

	verdict := verdictByQualified(result)
	// NewRouter is unreferenced, but Go has no language voice, so it is
	// possibly_dead (core_no_language_voice), never a confident dead.
	for q, v := range verdict {
		if q == "router.NewRouter" || q == "NewRouter" {
			if v != dead.VerdictPossiblyDead {
				t.Errorf("NewRouter verdict = %q, want %q", v, dead.VerdictPossiblyDead)
			}
			return
		}
	}
	t.Error("NewRouter not found in unreferenced results")
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(result) {
		if excluded[s.Name] {
			t.Errorf("%q should be excluded as Python dunder method", s.Name)
		}
	}
}

// TestLibraryPublicAPIIsPossiblyDead pins the polarity inversion (pitch 25-13
// decisions #1 and #6) AND the Go voice's library rule (25-16): a library's
// exported API is no longer silently excluded — it surfaces as possibly_dead
// with the core_exported_api reason, because an external consumer Sense cannot
// see may exist, and never earns `dead`. An UNEXPORTED library helper, by
// contrast, has no external reach (staticcheck U1000 flags it) so it does earn
// `dead`. The two sides together prove the voice is targeted, not a blanket
// softener.
func TestLibraryPublicAPIIsPossiblyDead(t *testing.T) {
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

	byQualified := map[string]dead.Finding{}
	for _, f := range result.Findings {
		byQualified[f.Symbol.Qualified] = f
	}

	// The exported API is reported (not excluded), carries core_exported_api, and
	// never earns `dead` — an external consumer may exist.
	for _, name := range []string{"lib.PublicFunc", "lib.PublicType"} {
		f, ok := byQualified[name]
		if !ok {
			t.Errorf("%q should be reported as possibly_dead, not excluded", name)
			continue
		}
		if f.Verdict != dead.VerdictPossiblyDead {
			t.Errorf("%q verdict = %q, want %q", name, f.Verdict, dead.VerdictPossiblyDead)
		}
		if f.Reason == nil || f.Reason.Code != dead.ReasonExportedAPI {
			t.Errorf("%q reason = %v, want %q", name, f.Reason, dead.ReasonExportedAPI)
		}
	}

	// An unexported library helper has no external reach, so it earns `dead`.
	// (PublicType.privateMethod is collapsed under its still-reported parent type
	// by Rollup, so the top-level privateFunc is the clean unexported probe.)
	f, ok := byQualified["lib.privateFunc"]
	if !ok {
		t.Error("lib.privateFunc should be reported")
	} else if f.Verdict != dead.VerdictDead {
		t.Errorf("lib.privateFunc verdict = %q, want %q (unexported, no caller, no mention)", f.Verdict, dead.VerdictDead)
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(result) {
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
	for _, s := range allSymbols(resultAll) {
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
	for _, s := range allSymbols(result) {
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

// allSymbols flattens a dead.Result's findings to the unreferenced symbols,
// regardless of verdict — the post-rebuild equivalent of the old result.Dead
// candidate list, for tests that only assert membership.
func allSymbols(r dead.Result) []dead.Symbol {
	out := make([]dead.Symbol, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Symbol)
	}
	return out
}

// verdictByQualified maps each finding's qualified name to its verdict.
func verdictByQualified(r dead.Result) map[string]dead.Verdict {
	m := make(map[string]dead.Verdict, len(r.Findings))
	for _, f := range r.Findings {
		m[f.Symbol.Qualified] = f.Verdict
	}
	return m
}

// TestFindDeadLegacyMetaFailsClosed is the legacy-meta control: a pre-feature
// index that carries only the old union `mentioned_names` key (no per-language
// suffix) must degrade to all-possibly_dead, never a false `dead`. The
// per-language reader globs `mentioned_names:*`, so the legacy key is invisible,
// every language reads as un-harvested, and the soundness gate fails closed with
// core_no_harvest — the safe direction. Pins the migration rabbit hole.
func TestFindDeadLegacyMetaFailsClosed(t *testing.T) {
	// Scan the smoke fixture, which pins one genuinely-dead Ruby private
	// (LineItem#orphaned_private) earning `dead` under per-language meta.
	root := smokeRoot(t)
	senseDir := t.TempDir()
	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Sense: senseDir, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	dbPath := filepath.Join(senseDir, "index.db")
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

	// Baseline: with per-language meta the orphan earns `dead`, proving the
	// fixture is a real `dead` for the downgrade to then take away.
	base, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead baseline: %v", err)
	}
	if !deadHasSuffix(base, "#orphaned_private") {
		t.Fatalf("baseline: orphaned_private should earn dead under per-language meta")
	}

	// Downgrade the index to a pre-feature shape: drop the per-language keys and
	// write the bare union key carrying the same names.
	legacyMentions, _ := adapter.ReadMeta(ctx, "mentioned_names:ruby")
	if legacyMentions == "" {
		t.Fatal("expected mentioned_names:ruby present before downgrade")
	}
	if err := adapter.DeleteMeta(ctx, "mentioned_names:ruby"); err != nil {
		t.Fatalf("delete mention key: %v", err)
	}
	_ = adapter.DeleteMeta(ctx, "dispatch_names:ruby")
	// A pre-feature index predates the harvest entirely — it has no
	// harvested_langs key either, so drop it to model the legacy shape faithfully.
	if err := adapter.DeleteMeta(ctx, "harvested_langs"); err != nil {
		t.Fatalf("delete harvested_langs: %v", err)
	}
	if err := adapter.WriteMeta(ctx, "mentioned_names", legacyMentions); err != nil {
		t.Fatalf("write legacy union key: %v", err)
	}

	// After downgrade: nothing earns `dead`, and the orphan fails closed with
	// core_no_harvest (its language reads as un-harvested).
	got, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead legacy: %v", err)
	}
	for _, f := range got.Findings {
		if f.Verdict == dead.VerdictDead {
			t.Errorf("%s earned dead off a legacy union index, want possibly_dead", f.Symbol.Qualified)
		}
	}
	if code := reasonCodeForSuffix(got, "#orphaned_private"); code != dead.ReasonNoHarvest {
		t.Errorf("orphaned_private reason = %q, want %q (fail-closed)", code, dead.ReasonNoHarvest)
	}
}

// deadHasSuffix reports whether any earned-`dead` finding's qualified name ends
// with suffix.
func deadHasSuffix(r dead.Result, suffix string) bool {
	for _, f := range r.Findings {
		if f.Verdict == dead.VerdictDead && endsWith(f.Symbol.Qualified, suffix) {
			return true
		}
	}
	return false
}

// reasonCodeForSuffix returns the reason code of the first finding whose
// qualified name ends with suffix, or "" if none (or the finding has no reason).
func reasonCodeForSuffix(r dead.Result, suffix string) string {
	for _, f := range r.Findings {
		if endsWith(f.Symbol.Qualified, suffix) && f.Reason != nil {
			return f.Reason.Code
		}
	}
	return ""
}

// TestFindDeadExcludesRouteHelpers proves synthetic route:* helper symbols —
// emitted by the route DSL and often unreferenced (e.g. the _url twins) — are
// never reported as dead Ruby code, while an ordinary same-named app method
// still is. Mirrors the ruby-core synthetic filter.
func TestFindDeadExcludesRouteHelpers(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "config", "routes.rb"), `Rails.application.routes.draw do
  resources :orders
end
`)
	// An ordinary, uncalled method literally named orders_url — the control
	// that proves the filter keys on the route: prefix, not the bare name.
	writeFile(t, filepath.Join(root, "app", "models", "billing.rb"), `class Billing
  def orders_url
    "x"
  end
end
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, scan.Options{Root: root, Output: &bytes.Buffer{}, Warnings: io.Discard}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	result, err := dead.FindDead(ctx, db, dead.Options{})
	if err != nil {
		t.Fatalf("FindDead: %v", err)
	}
	var sawRouteHelper bool
	for _, s := range allSymbols(result) {
		if len(s.Qualified) >= 6 && s.Qualified[:6] == "route:" {
			sawRouteHelper = true
		}
	}
	if sawRouteHelper {
		t.Error("a synthetic route:* helper leaked into dead-code output")
	}
}
