package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

// foldMemberCallers surfaces a DB error from the member-edge collection:
// the child-symbol lookup succeeds (sense_symbols intact) but loading a
// member's inbound edges fails (sense_edges dropped), so the error rides
// collectMemberCallerEdges back through foldMemberCallers.
func TestFoldMemberCallersEdgeLoadError(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, filepath.Join(t.TempDir(), "fold.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{Path: "c.php", Language: "php", Hash: "h", Symbols: 2, IndexedAt: time.Now()})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	classID, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "C", Qualified: "C", Kind: model.KindClass, LineStart: 1, LineEnd: 9})
	if err != nil {
		t.Fatalf("WriteSymbol class: %v", err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "m", Qualified: "C\\m", Kind: model.KindMethod, ParentID: &classID, LineStart: 2, LineEnd: 4}); err != nil {
		t.Fatalf("WriteSymbol method: %v", err)
	}
	if _, err := a.db.ExecContext(ctx, `DROP TABLE sense_edges`); err != nil {
		t.Fatalf("drop sense_edges: %v", err)
	}

	sc := &model.SymbolContext{Symbol: model.Symbol{ID: classID, Kind: model.KindClass}}
	if err := a.foldMemberCallers(ctx, sc); err == nil {
		t.Fatal("foldMemberCallers with dropped edges table: want error, got nil")
	}
}
