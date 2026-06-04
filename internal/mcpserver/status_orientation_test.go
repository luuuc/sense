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

// seedIndexWith opens a fresh index and runs fn to populate it, returning the
// open adapter and a cleanup. Helper for the orientation queries, which read
// directly off the DB.
func seedIndexWith(t *testing.T, fn func(ctx context.Context, a *sqlite.Adapter)) (*sqlite.Adapter, string) {
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
	fn(ctx, adapter)
	return adapter, dir
}

// TestQueryTopNamespacesTruncatesToEight seeds symbols across more than eight
// distinct top-level directories and asserts queryTopNamespaces returns only
// the eight heaviest, exercising the truncation branch.
func TestQueryTopNamespacesTruncatesToEight(t *testing.T) {
	adapter, _ := seedIndexWith(t, func(ctx context.Context, a *sqlite.Adapter) {
		now := time.Now()
		// 10 distinct top-level namespaces, decreasing symbol weight so the
		// sort + truncation keeps the eight largest deterministically.
		for i := 0; i < 10; i++ {
			dir := string(rune('a'+i)) + "pkg"
			fid, err := a.WriteFile(ctx, &model.File{
				Path: dir + "/file.go", Language: "go", Hash: "h" + dir, Symbols: 10 - i, IndexedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			for j := 0; j <= 10-i; j++ {
				_, err := a.WriteSymbol(ctx, &model.Symbol{
					FileID: fid, Name: "S", Qualified: dir + ".S" + string(rune('0'+j)),
					Kind: "function", LineStart: j + 1, LineEnd: j + 1,
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		}
	})

	ns, err := queryTopNamespaces(context.Background(), adapter.DB())
	if err != nil {
		t.Fatalf("queryTopNamespaces: %v", err)
	}
	if len(ns) != 8 {
		t.Errorf("expected the eight heaviest namespaces, got %d", len(ns))
	}
}

// TestQueryEntryPointsIncludesFilePatterns seeds a root index.ts (a known
// file-based entry point with no main/Main symbol) and asserts
// queryEntryPoints reports it via the file-pattern branch.
func TestQueryEntryPointsIncludesFilePatterns(t *testing.T) {
	adapter, _ := seedIndexWith(t, func(ctx context.Context, a *sqlite.Adapter) {
		now := time.Now()
		// A recognised file-based entry point with no symbol-based main.
		if _, err := a.WriteFile(ctx, &model.File{
			Path: "index.ts", Language: "typescript", Hash: "hidx", Symbols: 0, IndexedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	})

	eps, err := queryEntryPoints(context.Background(), adapter.DB())
	if err != nil {
		t.Fatalf("queryEntryPoints: %v", err)
	}
	var found bool
	for _, ep := range eps {
		if ep.File == "index.ts" && ep.Kind == "file" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected index.ts as a file-based entry point, got %+v", eps)
	}
}

// TestQueryEntryPointsDedupesSymbolAndFileMain covers the seen-set skip: when a
// root main.go exists both as a symbol-based entry point and as a file
// pattern, the file branch must not duplicate it.
func TestQueryEntryPointsDedupesSymbolAndFileMain(t *testing.T) {
	adapter, _ := seedIndexWith(t, func(ctx context.Context, a *sqlite.Adapter) {
		now := time.Now()
		fid, err := a.WriteFile(ctx, &model.File{
			Path: "main.go", Language: "go", Hash: "hmain", Symbols: 1, IndexedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: "main", Qualified: "main.main", Kind: "function", LineStart: 1, LineEnd: 2,
		}); err != nil {
			t.Fatal(err)
		}
	})

	eps, err := queryEntryPoints(context.Background(), adapter.DB())
	if err != nil {
		t.Fatalf("queryEntryPoints: %v", err)
	}
	var mainGoCount int
	for _, ep := range eps {
		if ep.File == "main.go" {
			mainGoCount++
		}
	}
	if mainGoCount != 1 {
		t.Errorf("main.go should appear once (symbol entry, not re-added as a file), got %d", mainGoCount)
	}
}

// TestQueryVersionReturnsNilOnClosedDB covers queryVersion's guard: when the
// user_version pragma cannot be read it returns nil rather than a partial
// version block.
func TestQueryVersionReturnsNilOnClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "ver_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	if v := queryVersion(ctx, db); v != nil {
		t.Errorf("expected nil version when the pragma cannot be read, got %+v", v)
	}
}

// TestQueryVersionReportsStoredModel covers the stored-model branch: when the
// index records an embedding_model meta value, queryVersion reports it (rather
// than defaulting to the binary's) and flags whether it is current.
func TestQueryVersionReportsStoredModel(t *testing.T) {
	adapter, _ := seedIndexWith(t, func(ctx context.Context, a *sqlite.Adapter) {
		if err := a.WriteMeta(ctx, "embedding_model", "some-stored-model"); err != nil {
			t.Fatal(err)
		}
	})

	v := queryVersion(context.Background(), adapter.DB())
	if v == nil {
		t.Fatal("expected a version block")
	}
	if v.EmbeddingModel != "some-stored-model" {
		t.Errorf("EmbeddingModel = %q, want some-stored-model", v.EmbeddingModel)
	}
	if v.EmbeddingModelCurrent {
		t.Error("a stored model differing from the binary must not be reported current")
	}
}

// TestBuildStatusResponsePropagatesLanguageError covers the early return in
// buildStatusResponse when the language breakdown query fails (closed DB after
// the index counts succeeded is not possible; instead we close before any
// query so the first failing call surfaces an error).
func TestBuildStatusResponsePropagatesLanguageError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "bsr_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	if _, err := buildStatusResponse(ctx, db, dir, nil); err == nil {
		t.Error("expected buildStatusResponse to propagate the underlying query error")
	}
}
