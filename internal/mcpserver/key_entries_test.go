package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupKeyEntriesFixture(t *testing.T) *sqlite.Adapter {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()

	fid1, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/router/router.go", Language: "go",
		Hash: "aaa", Symbols: 3, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fid2, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/handler/handler.go", Language: "go",
		Hash: "bbb", Symbols: 2, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	fid3, err := adapter.WriteFile(ctx, &model.File{
		Path: "internal/middleware/auth.go", Language: "go",
		Hash: "ccc", Symbols: 1, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Engine: type referenced from multiple files
	engineID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Engine", Qualified: "router.Engine",
		Kind: "type", LineStart: 10, LineEnd: 30,
		Snippet: "type Engine struct { pool sync.Pool }",
	})
	if err != nil {
		t.Fatal(err)
	}

	// RouterGroup: type referenced from another file
	groupID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "RouterGroup", Qualified: "router.RouterGroup",
		Kind: "type", LineStart: 32, LineEnd: 50,
		Snippet: "type RouterGroup struct { basePath string }",
	})
	if err != nil {
		t.Fatal(err)
	}

	// HandleRequest: caller of Engine
	handleID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "HandleRequest", Qualified: "handler.HandleRequest",
		Kind: "function", LineStart: 5, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	// AuthMiddleware: caller of Engine
	authID, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid3, Name: "AuthMiddleware", Qualified: "middleware.AuthMiddleware",
		Kind: "function", LineStart: 1, LineEnd: 15,
	})
	if err != nil {
		t.Fatal(err)
	}

	intPtr := func(v int) *int { return &v }
	edges := []model.Edge{
		// HandleRequest → Engine (from fid2)
		{SourceID: model.Int64Ptr(handleID), TargetID: engineID, Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(10)},
		// AuthMiddleware → Engine (from fid3)
		{SourceID: model.Int64Ptr(authID), TargetID: engineID, Kind: model.EdgeCalls, FileID: fid3, Line: intPtr(5)},
		// Engine → RouterGroup (from fid1)
		{SourceID: model.Int64Ptr(engineID), TargetID: groupID, Kind: model.EdgeCalls, FileID: fid1, Line: intPtr(15)},
		// HandleRequest → RouterGroup (from fid2)
		{SourceID: model.Int64Ptr(handleID), TargetID: groupID, Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(12)},
	}
	for _, e := range edges {
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	return adapter
}

func TestBuildKeyEntries(t *testing.T) {
	adapter := setupKeyEntriesFixture(t)
	ctx := context.Background()

	entries, err := buildKeyEntries(ctx, adapter, "", 10)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) == 0 {
		t.Fatal("expected at least one key entry")
	}

	// Engine should appear (type with 2 distinct file refs)
	found := map[string]int{}
	for i, e := range entries {
		found[e.Name] = i
		if e.Kind == "" {
			t.Errorf("entry %q has empty kind", e.Name)
		}
	}

	idx, ok := found["router.Engine"]
	if !ok {
		t.Fatalf("expected router.Engine in results, got %v", entries)
	}
	engine := entries[idx]
	if engine.Snippet == "" {
		t.Error("expected snippet for Engine")
	}
	if engine.References < 2 {
		t.Errorf("expected Engine references >= 2, got %d", engine.References)
	}
	// Should have callers populated
	if len(engine.Callers) == 0 {
		t.Error("expected callers for Engine")
	}
	// Callers should be qualified names
	for _, c := range engine.Callers {
		if c == "" {
			t.Error("caller name should not be empty")
		}
	}
}

func TestBuildKeyEntriesDomainFilter(t *testing.T) {
	adapter := setupKeyEntriesFixture(t)
	ctx := context.Background()

	entries, err := buildKeyEntries(ctx, adapter, "internal/router", 10)
	if err != nil {
		t.Fatal(err)
	}

	// With domain filter, should only return symbols under that path
	for _, e := range entries {
		if e.Name != "router.Engine" && e.Name != "router.RouterGroup" {
			t.Errorf("unexpected symbol %q outside domain internal/router", e.Name)
		}
	}
}

func TestBuildKeyEntriesLimit(t *testing.T) {
	adapter := setupKeyEntriesFixture(t)
	ctx := context.Background()

	entries, err := buildKeyEntries(ctx, adapter, "", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) > 1 {
		t.Errorf("expected at most 1 entry with limit=1, got %d", len(entries))
	}
}

func TestBuildKeyEntriesEmptyDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	entries, err := buildKeyEntries(ctx, adapter, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for empty db, got %d", len(entries))
	}
}
