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

func TestBuildKeyEntriesExcludesTestFiles(t *testing.T) {
	excludedPaths := []struct {
		name string
		path string
	}{
		{"go_test_suffix", "internal/router/router_test.go"},
		{"nested_test_dir", "src/test/java/com/example/TestUtil.java"},
		{"root_test_dir", "test/helpers/util.go"},
		{"nested_spec_dir", "app/spec/validators/check.rb"},
		{"root_spec_dir", "spec/models/user_spec.rb"},
		{"nested_tests_dir", "lib/tests/integration.go"},
		{"root_tests_dir", "tests/unit/handler.go"},
		{"testdata_dir", "internal/testdata/fixture.go"},
		{"fixture_dir", "internal/fixture/data.go"},
		{"mock_dir", "internal/mock/client.go"},
		{"mocks_dir", "internal/mocks/store.go"},
		{"vendor_dir", "vendor/lib/dep.go"},
	}

	allowedPaths := []string{
		"internal/contest/handler.go",
		"app/models/specification.rb",
	}

	for _, tc := range excludedPaths {
		t.Run("excludes_"+tc.name, func(t *testing.T) {
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
			intPtr := func(v int) *int { return &v }

			testFID, _ := adapter.WriteFile(ctx, &model.File{
				Path: tc.path, Language: "go", Hash: "ttt", Symbols: 1, IndexedAt: now,
			})
			testSym, _ := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: testFID, Name: "Bad", Qualified: "bad.Bad",
				Kind: "class", LineStart: 1, LineEnd: 10,
			})

			prodFID, _ := adapter.WriteFile(ctx, &model.File{
				Path: "internal/core/core.go", Language: "go", Hash: "ppp", Symbols: 1, IndexedAt: now,
			})
			prodSym, _ := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: prodFID, Name: "Core", Qualified: "core.Core",
				Kind: "type", LineStart: 1, LineEnd: 10,
			})

			callerFID, _ := adapter.WriteFile(ctx, &model.File{
				Path: "internal/api/api.go", Language: "go", Hash: "ccc", Symbols: 1, IndexedAt: now,
			})
			callerSym, _ := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: callerFID, Name: "Handle", Qualified: "api.Handle",
				Kind: "function", LineStart: 1, LineEnd: 5,
			})

			for _, targetID := range []int64{testSym, prodSym} {
				for j := 0; j < 3; j++ {
					if _, err := adapter.WriteEdge(ctx, &model.Edge{
						SourceID: model.Int64Ptr(callerSym), TargetID: targetID,
						Kind: model.EdgeCalls, FileID: callerFID, Line: intPtr(j + 1),
					}); err != nil {
						t.Fatal(err)
					}
				}
			}

			entries, err := buildKeyEntries(ctx, adapter, "", 10)
			if err != nil {
				t.Fatal(err)
			}

			for _, e := range entries {
				if e.Name == "bad.Bad" {
					t.Errorf("symbol from %q (%s) should be excluded", tc.path, tc.name)
				}
			}
		})
	}

	for _, path := range allowedPaths {
		t.Run("allows_"+filepath.Base(path), func(t *testing.T) {
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
			intPtr := func(v int) *int { return &v }

			fid, _ := adapter.WriteFile(ctx, &model.File{
				Path: path, Language: "go", Hash: "aaa", Symbols: 1, IndexedAt: now,
			})
			sym, _ := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: fid, Name: "Good", Qualified: "good.Good",
				Kind: "type", LineStart: 1, LineEnd: 10,
			})

			callerFID, _ := adapter.WriteFile(ctx, &model.File{
				Path: "internal/api/api.go", Language: "go", Hash: "bbb", Symbols: 1, IndexedAt: now,
			})
			callerSym, _ := adapter.WriteSymbol(ctx, &model.Symbol{
				FileID: callerFID, Name: "Use", Qualified: "api.Use",
				Kind: "function", LineStart: 1, LineEnd: 5,
			})
			if _, err := adapter.WriteEdge(ctx, &model.Edge{
				SourceID: model.Int64Ptr(callerSym), TargetID: sym,
				Kind: model.EdgeCalls, FileID: callerFID, Line: intPtr(1),
			}); err != nil {
				t.Fatal(err)
			}

			entries, err := buildKeyEntries(ctx, adapter, "", 10)
			if err != nil {
				t.Fatal(err)
			}

			found := false
			for _, e := range entries {
				if e.Name == "good.Good" {
					found = true
				}
			}
			if !found {
				t.Errorf("symbol from %q should NOT be excluded", path)
			}
		})
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
