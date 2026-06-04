package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func seedKeySymbols(ctx context.Context, t *testing.T, a *sqlite.Adapter) {
	t.Helper()

	fid1, err := a.WriteFile(ctx, &model.File{
		Path: "internal/router/router.go", Language: "go",
		Hash: "aaa", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fid2, err := a.WriteFile(ctx, &model.File{
		Path: "internal/router/group.go", Language: "go",
		Hash: "bbb", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fid3, err := a.WriteFile(ctx, &model.File{
		Path: "internal/handler/handler.go", Language: "go",
		Hash: "ccc", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Key type: Engine (referenced from multiple files)
	engineID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Engine", Qualified: "router.Engine",
		Kind: "type", LineStart: 10, LineEnd: 30,
		Snippet: "type Engine struct { pool sync.Pool }",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Key type: RouterGroup
	groupID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "RouterGroup", Qualified: "router.RouterGroup",
		Kind: "type", LineStart: 1, LineEnd: 15,
		Snippet: "type RouterGroup struct { basePath string }",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Function (should be excluded by kind filter)
	handleID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid3, Name: "HandleRequest", Qualified: "handler.HandleRequest",
		Kind: "function", LineStart: 5, LineEnd: 20,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Builtin (should be excluded by name filter)
	_, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Close", Qualified: "router.Close",
		Kind: "type", LineStart: 32, LineEnd: 35,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Edges: Engine referenced from 3 files
	intPtr := func(v int) *int { return &v }
	edges := []model.Edge{
		{SourceID: model.Int64Ptr(handleID), TargetID: engineID, Kind: model.EdgeCalls, FileID: fid3, Line: intPtr(10)},
		{SourceID: model.Int64Ptr(groupID), TargetID: engineID, Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(5)},
		{SourceID: model.Int64Ptr(engineID), TargetID: groupID, Kind: model.EdgeCalls, FileID: fid1, Line: intPtr(15)},
		// HandleRequest → RouterGroup (from fid3)
		{SourceID: model.Int64Ptr(handleID), TargetID: groupID, Kind: model.EdgeCalls, FileID: fid3, Line: intPtr(12)},
	}
	for _, e := range edges {
		if _, err := a.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTopSymbolsByReach(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	results, err := a.TopSymbolsByReach(ctx, "internal/", 15)
	if err != nil {
		t.Fatal(err)
	}

	if len(results) < 2 {
		t.Fatalf("expected at least 2 key symbols, got %d", len(results))
	}

	found := map[string]sqlite.KeySymbol{}
	for _, r := range results {
		found[r.Qualified] = r
	}

	// Both Engine and RouterGroup referenced from 2 distinct files each
	if ks, ok := found["router.Engine"]; !ok {
		t.Error("expected router.Engine in results")
	} else {
		if ks.RefFiles < 2 {
			t.Errorf("expected Engine ≥2 ref files, got %d", ks.RefFiles)
		}
		if ks.Snippet == "" {
			t.Error("expected snippet to be populated")
		}
	}
	if _, ok := found["router.RouterGroup"]; !ok {
		t.Error("expected router.RouterGroup in results")
	}

	// Should not contain HandleRequest (function kind)
	if _, ok := found["handler.HandleRequest"]; ok {
		t.Error("functions should be excluded from key symbols")
	}
	// Should not contain Close (builtin name)
	if _, ok := found["router.Close"]; ok {
		t.Error("builtin names should be excluded from key symbols")
	}
}

func TestTopSymbolsByReach_DomainFilter(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	// Filter to handler/ — no types live there
	results, err := a.TopSymbolsByReach(ctx, "internal/handler/", 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for handler domain, got %d", len(results))
	}
}

func TestTopSymbolsByReach_ExcludesTestPaths(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	testPaths := []string{
		"internal/router/router_test.go",
		"src/test/java/com/example/TestUtil.java",
		"test/helpers/util.go",
		"app/spec/validators/check.rb",
		"spec/models/user_spec.rb",
		"lib/tests/integration.go",
		"tests/unit/handler.go",
		"internal/testdata/fixture.go",
		"internal/fixture/data.go",
		"internal/mock/client.go",
		"internal/mocks/store.go",
		"vendor/lib/dep.go",
	}

	prodFID, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/core/core.go", Language: "go",
		Hash: "ppp", Symbols: 1, IndexedAt: now,
	})
	prodSym, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: prodFID, Name: "Core", Qualified: "core.Core",
		Kind: "type", LineStart: 1, LineEnd: 10,
		Snippet: "type Core struct{}",
	})

	callerFID, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/api/api.go", Language: "go",
		Hash: "ccc", Symbols: 1, IndexedAt: now,
	})
	callerSym, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: callerFID, Name: "Handle", Qualified: "api.Handle",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(callerSym), TargetID: prodSym,
		Kind: model.EdgeCalls, FileID: callerFID, Line: intPtr(1),
	}); err != nil {
		t.Fatal(err)
	}

	for i, p := range testPaths {
		fid, _ := a.WriteFile(ctx, &model.File{
			Path: p, Language: "go", Hash: string(rune('a' + i)), Symbols: 1, IndexedAt: now,
		})
		sym, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: "Bad", Qualified: "bad.Bad" + string(rune('0'+i)),
			Kind: "class", LineStart: 1, LineEnd: 10,
		})
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerSym), TargetID: sym,
			Kind: model.EdgeCalls, FileID: callerFID, Line: intPtr(i + 10),
		}); err != nil {
			t.Fatal(err)
		}
	}

	results, err := a.TopSymbolsByReach(ctx, "", 50)
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range results {
		if r.Qualified != "core.Core" {
			t.Errorf("test symbol %q should be excluded", r.Qualified)
		}
	}
	if len(results) == 0 {
		t.Error("expected production symbol core.Core in results")
	}
}

func TestTopSymbolsByReach_DefaultLimit(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	results, err := a.TopSymbolsByReach(ctx, "internal/", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("default limit should still return results, got %d", len(results))
	}
}

func TestTopSymbolsByReach_LimitTruncates(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	results, err := a.TopSymbolsByReach(ctx, "internal/", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected exactly 1 result with limit=1, got %d", len(results))
	}
}

func TestTopSymbolsByReach_BuiltinFiltered(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	fid1, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/core/core.go", Language: "go",
		Hash: "aaa", Symbols: 2, IndexedAt: now,
	})
	fid2, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/api/api.go", Language: "go",
		Hash: "bbb", Symbols: 1, IndexedAt: now,
	})

	closeID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Close", Qualified: "core.Close",
		Kind: "type", LineStart: 1, LineEnd: 10,
		Snippet: "type Close struct{}",
	})
	callerID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Caller", Qualified: "api.Caller",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(callerID), TargetID: closeID,
		Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(3),
	}); err != nil {
		t.Fatal(err)
	}

	results, err := a.TopSymbolsByReach(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Qualified == "core.Close" {
			t.Error("builtin name 'Close' should be filtered out")
		}
	}
}

func TestTopCallers_DefaultLimit(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	results, _ := a.TopSymbolsByReach(ctx, "internal/", 15)
	if len(results) == 0 {
		t.Fatal("no results")
	}

	callers, err := a.TopCallers(ctx, results[0].ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(callers) == 0 {
		t.Fatal("default limit should still return callers")
	}
}

func TestTopSymbolsByReach_UnqualifiedName(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	fid1, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/core/core.go", Language: "go",
		Hash: "aaa", Symbols: 1, IndexedAt: now,
	})
	fid2, _ := a.WriteFile(ctx, &model.File{
		Path: "internal/api/api.go", Language: "go",
		Hash: "bbb", Symbols: 1, IndexedAt: now,
	})

	symID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid1, Name: "Widget", Qualified: "Widget",
		Kind: "type", LineStart: 1, LineEnd: 10,
	})
	callerID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Use", Qualified: "Use",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(callerID), TargetID: symID,
		Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(3),
	}); err != nil {
		t.Fatal(err)
	}

	results, err := a.TopSymbolsByReach(ctx, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range results {
		if r.Qualified == "Widget" {
			found = true
		}
	}
	if !found {
		t.Error("expected unqualified symbol 'Widget' in results")
	}
}

func TestTopCallers(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedKeySymbols(ctx, t, a)

	// Get Engine's ID
	results, err := a.TopSymbolsByReach(ctx, "internal/", 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("no results")
	}

	callers, err := a.TopCallers(ctx, results[0].ID, 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(callers) == 0 {
		t.Fatal("expected at least one caller for Engine")
	}

	// Should have handler.HandleRequest and router.RouterGroup as callers
	found := map[string]bool{}
	for _, c := range callers {
		found[c.Qualified] = true
		if c.File == "" {
			t.Error("caller file should be populated")
		}
	}
	if !found["handler.HandleRequest"] && !found["router.RouterGroup"] {
		t.Error("expected at least one of HandleRequest or RouterGroup as caller")
	}
}

func TestCallersOfTargets(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	fid, _ := a.WriteFile(ctx, &model.File{
		Path: "main.go", Language: "go", Hash: "aaa", Symbols: 4, IndexedAt: now,
	})
	aID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "A", Qualified: "pkg.A", Kind: "function", LineStart: 1, LineEnd: 5,
	})
	bID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "B", Qualified: "pkg.B", Kind: "function", LineStart: 10, LineEnd: 15,
	})
	cID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "C", Qualified: "pkg.C", Kind: "function", LineStart: 20, LineEnd: 25,
	})
	dID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "D", Qualified: "pkg.D", Kind: "function", LineStart: 30, LineEnd: 35,
	})

	// A calls B, C calls B, D calls B
	if _, err := a.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(aID), TargetID: bID, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(cID), TargetID: bID, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(22)}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteEdge(ctx, &model.Edge{SourceID: model.Int64Ptr(dID), TargetID: bID, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(32)}); err != nil {
		t.Fatal(err)
	}

	result, err := a.CallersOfTargets(ctx, []int64{bID}, aID, 10)
	if err != nil {
		t.Fatal(err)
	}
	callers := result[bID]
	if len(callers) != 2 {
		t.Fatalf("expected 2 callers (excluding A), got %d: %v", len(callers), callers)
	}
	callerSet := map[string]bool{}
	for _, c := range callers {
		callerSet[c] = true
	}
	if !callerSet["pkg.C"] || !callerSet["pkg.D"] {
		t.Errorf("expected pkg.C and pkg.D, got %v", callers)
	}
}

func TestCallersOfTargetsMaxPerTarget(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	fid, _ := a.WriteFile(ctx, &model.File{
		Path: "main.go", Language: "go", Hash: "aaa", Symbols: 10, IndexedAt: now,
	})
	targetID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Target", Qualified: "pkg.Target", Kind: "function", LineStart: 1, LineEnd: 5,
	})
	rootID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Root", Qualified: "pkg.Root", Kind: "function", LineStart: 10, LineEnd: 15,
	})

	// Create 5 callers (excluding root).
	for i := 0; i < 5; i++ {
		cID, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: fmt.Sprintf("C%d", i), Qualified: fmt.Sprintf("pkg.C%d", i),
			Kind: "function", LineStart: 20 + i*10, LineEnd: 25 + i*10,
		})
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(cID), TargetID: targetID, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(21 + i*10),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Request maxPerTarget=2: should only get 2 of the 5 callers.
	result, err := a.CallersOfTargets(ctx, []int64{targetID}, rootID, 2)
	if err != nil {
		t.Fatal(err)
	}
	callers := result[targetID]
	if len(callers) != 2 {
		t.Fatalf("expected 2 callers with maxPerTarget=2, got %d: %v", len(callers), callers)
	}
}

func TestCallersOfTargetsDefaultMaxPerTarget(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	now := time.Now()
	intPtr := func(v int) *int { return &v }

	fid, _ := a.WriteFile(ctx, &model.File{
		Path: "main.go", Language: "go", Hash: "aaa", Symbols: 3, IndexedAt: now,
	})
	targetID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Target", Qualified: "pkg.Target", Kind: "function", LineStart: 1, LineEnd: 5,
	})
	callerID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Caller", Qualified: "pkg.Caller", Kind: "function", LineStart: 10, LineEnd: 15,
	})
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(callerID), TargetID: targetID, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(11),
	}); err != nil {
		t.Fatal(err)
	}

	// maxPerTarget=0 takes the default cap (20), so the single caller is kept.
	result, err := a.CallersOfTargets(ctx, []int64{targetID}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result[targetID]) != 1 {
		t.Fatalf("expected 1 caller with default cap, got %d", len(result[targetID]))
	}
}

func TestCallersOfTargetsEmpty(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	result, err := a.CallersOfTargets(ctx, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		t.Errorf("expected nil for empty input, got %v", result)
	}
}
