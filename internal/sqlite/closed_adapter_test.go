package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// closedAdapter opens an Adapter, closes its DB handle, and returns it
// so the caller can exercise the "every SQL call returns an error" branch
// across the read- and write-path methods without crafting a corrupt
// schema. Reused by the closed-DB error-path tests below and by hook/
// pre_compact tests in another package.
func closedAdapter(t *testing.T) *sqlite.Adapter {
	t.Helper()
	path := filepath.Join(t.TempDir(), "index.db")
	a, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return a
}

func TestClosedAdapterReturnsErrors(t *testing.T) {
	ctx := context.Background()
	a := closedAdapter(t)

	t.Run("FileMeta", func(t *testing.T) {
		if _, _, err := a.FileMeta(ctx, "foo"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("FileHashMap", func(t *testing.T) {
		if _, err := a.FileHashMap(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("FilePaths", func(t *testing.T) {
		if _, err := a.FilePaths(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("DeleteFile", func(t *testing.T) {
		if err := a.DeleteFile(ctx, "foo"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("WriteFile", func(t *testing.T) {
		f := &model.File{Path: "foo.go", Language: "go", Hash: "h", IndexedAt: time.Now()}
		if _, err := a.WriteFile(ctx, f); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("WriteSymbol", func(t *testing.T) {
		s := &model.Symbol{FileID: 1, Name: "x", Qualified: "x", Kind: "function"}
		if _, err := a.WriteSymbol(ctx, s); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("WriteEdge", func(t *testing.T) {
		src := int64(1)
		e := &model.Edge{SourceID: &src, TargetID: 2, Kind: "calls", FileID: 1, Confidence: 1}
		if _, err := a.WriteEdge(ctx, e); err == nil {
			t.Error("expected error (source non-nil)")
		}
		eNoSource := &model.Edge{TargetID: 2, Kind: "calls", FileID: 1, Confidence: 1}
		if _, err := a.WriteEdge(ctx, eNoSource); err == nil {
			t.Error("expected error (source nil)")
		}
	})
	t.Run("Query", func(t *testing.T) {
		if _, err := a.Query(ctx, index.Filter{}); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("SymbolRefs", func(t *testing.T) {
		if _, err := a.SymbolRefs(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("EdgesOfKind", func(t *testing.T) {
		if _, err := a.EdgesOfKind(ctx, "calls"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("FileIDsByLanguage", func(t *testing.T) {
		if _, err := a.FileIDsByLanguage(ctx, "go"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("SymbolsForFiles", func(t *testing.T) {
		if _, err := a.SymbolsForFiles(ctx, []int64{1, 2}); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("SymbolsWithoutEmbeddings", func(t *testing.T) {
		if _, err := a.SymbolsWithoutEmbeddings(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("WriteEmbedding", func(t *testing.T) {
		if err := a.WriteEmbedding(ctx, 1, []byte{0, 0, 0, 0}); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("WriteMeta", func(t *testing.T) {
		if err := a.WriteMeta(ctx, "k", "v"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("ReadMeta", func(t *testing.T) {
		if _, err := a.ReadMeta(ctx, "k"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("EmbeddingDebtCount", func(t *testing.T) {
		if _, err := a.EmbeddingDebtCount(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("ClearEmbeddings", func(t *testing.T) {
		if err := a.ClearEmbeddings(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("DeleteMeta", func(t *testing.T) {
		if err := a.DeleteMeta(ctx, "k"); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("ReadSymbol", func(t *testing.T) {
		if _, err := a.ReadSymbol(ctx, 1); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("StampSchemaVersion", func(t *testing.T) {
		if err := a.StampSchemaVersion(ctx); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("Rebuild", func(t *testing.T) {
		if err := a.Rebuild(ctx); err == nil {
			t.Error("expected error")
		}
	})
}

func TestInTxBeginFailsOnClosedAdapter(t *testing.T) {
	ctx := context.Background()
	a := closedAdapter(t)
	err := a.InTx(ctx, func() error { return nil })
	if err == nil {
		t.Fatal("expected error from InTx on closed adapter")
	}
}

func TestInTxPanicsWhenPoolSizeChanges(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	a.DB().SetMaxOpenConns(5)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic when MaxOpenConnections != 1")
		}
	}()
	_ = a.InTx(ctx, func() error { return nil })
}
