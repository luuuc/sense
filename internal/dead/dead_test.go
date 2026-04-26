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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}
