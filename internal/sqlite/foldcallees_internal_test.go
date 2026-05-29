package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// foldMemberCallees surfaces a DB error from the child-symbol lookup rather
// than swallowing it. Exercised against a closed adapter so the query fails.
func TestFoldMemberCalleesChildQueryError(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	sc := &model.SymbolContext{Symbol: model.Symbol{ID: 1, Kind: model.KindClass}}
	if err := a.foldMemberCallees(ctx, sc); err == nil {
		t.Fatal("foldMemberCallees on closed adapter: want error, got nil")
	}
}
