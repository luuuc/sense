package sqlite_test

import (
	"context"
	"testing"
)

func TestSymbolsForFiles(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "model.go", "go", "h1")
	seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	seedSymbol(t, a, fid, "Process", "pkg.Process", "function")

	fid2 := seedFile(t, a, "other.go", "go", "h2")
	seedSymbol(t, a, fid2, "Config", "pkg.Config", "class")

	syms, err := a.SymbolsForFiles(ctx, []int64{fid})
	if err != nil {
		t.Fatalf("SymbolsForFiles: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("SymbolsForFiles len = %d, want 2", len(syms))
	}
	seen := map[string]bool{}
	for _, s := range syms {
		seen[s.Qualified] = true
	}
	if !seen["pkg.Order"] {
		t.Error("missing pkg.Order")
	}
	if !seen["pkg.Process"] {
		t.Error("missing pkg.Process")
	}

	// Empty input
	empty, err := a.SymbolsForFiles(ctx, nil)
	if err != nil {
		t.Fatalf("SymbolsForFiles empty: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("SymbolsForFiles empty len = %d, want 0", len(empty))
	}
}

func TestSymbolsForFilesMultipleFiles(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid1 := seedFile(t, a, "a.go", "go", "h1")
	fid2 := seedFile(t, a, "b.go", "go", "h2")
	seedSymbol(t, a, fid1, "A", "pkg.A", "class")
	seedSymbol(t, a, fid2, "B", "pkg.B", "class")

	syms, err := a.SymbolsForFiles(ctx, []int64{fid1, fid2})
	if err != nil {
		t.Fatalf("SymbolsForFiles: %v", err)
	}
	if len(syms) != 2 {
		t.Errorf("SymbolsForFiles len = %d, want 2", len(syms))
	}
}

func TestSymbolsWithoutEmbeddings(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	// Before any embeddings, both should be returned
	syms, err := a.SymbolsWithoutEmbeddings(ctx)
	if err != nil {
		t.Fatalf("SymbolsWithoutEmbeddings: %v", err)
	}
	if len(syms) != 2 {
		t.Fatalf("before embedding: len = %d, want 2", len(syms))
	}

	// Write embedding for s1
	if err := a.WriteEmbedding(ctx, s1, []byte{1, 2, 3}); err != nil {
		t.Fatalf("WriteEmbedding: %v", err)
	}

	syms, err = a.SymbolsWithoutEmbeddings(ctx)
	if err != nil {
		t.Fatalf("SymbolsWithoutEmbeddings after: %v", err)
	}
	if len(syms) != 1 {
		t.Fatalf("after one embedding: len = %d, want 1", len(syms))
	}
	if syms[0].ID != s2 {
		t.Errorf("remaining symbol ID = %d, want %d", syms[0].ID, s2)
	}
}

func TestClearEmbeddings(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	sid := seedSymbol(t, a, fid, "A", "pkg.A", "class")

	if err := a.WriteEmbedding(ctx, sid, []byte{1, 2, 3}); err != nil {
		t.Fatalf("WriteEmbedding: %v", err)
	}

	// Verify embedding exists
	debt, err := a.EmbeddingDebtCount(ctx)
	if err != nil {
		t.Fatalf("EmbeddingDebtCount: %v", err)
	}
	if debt != 0 {
		t.Errorf("debt before clear = %d, want 0", debt)
	}

	if err := a.ClearEmbeddings(ctx); err != nil {
		t.Fatalf("ClearEmbeddings: %v", err)
	}

	debt, err = a.EmbeddingDebtCount(ctx)
	if err != nil {
		t.Fatalf("EmbeddingDebtCount after: %v", err)
	}
	if debt != 1 {
		t.Errorf("debt after clear = %d, want 1", debt)
	}
}
