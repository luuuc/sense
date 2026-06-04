package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// formatSymbolContext renders a class/type with a resolved parent as an
// inheritance header ("kind qualified < parent"), the branch the embedding
// context relies on to express nesting.
func TestFormatSymbolContextClassWithParent(t *testing.T) {
	sym := symbolCtx{kind: "class", qualified: "Admin::User", parentName: "User"}
	out := formatSymbolContext("app/models/admin/user.rb", sym, symbolEdges{})
	if !strings.Contains(out, "class Admin::User < User") {
		t.Errorf("want inheritance header, got: %q", out)
	}
}

// foldRootMembers propagates the DB error from either fold direction rather
// than swallowing it. Exercised against a closed adapter so the child-symbol
// lookup inside each fold fails.
func TestFoldRootMembersError(t *testing.T) {
	ctx := context.Background()
	a, err := Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	root := &model.SymbolContext{Symbol: model.Symbol{ID: 1, Kind: model.KindClass}}
	t.Run("callers direction surfaces the member-caller fold error", func(t *testing.T) {
		if err := a.foldRootMembers(ctx, root, model.DirectionCallers); err == nil {
			t.Fatal("foldRootMembers on closed adapter: want error, got nil")
		}
	})
	t.Run("callees direction surfaces the member-callee fold error", func(t *testing.T) {
		if err := a.foldRootMembers(ctx, root, model.DirectionCallees); err == nil {
			t.Fatal("foldRootMembers on closed adapter: want error, got nil")
		}
	})
}
