package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// writeParentChildFixture builds two files with a container symbol in
// parentPath and a method in childPath whose ParentID points at the
// container — the cross-file link shape introduced by the parent-linkage
// finalize pass.
func writeParentChildFixture(t *testing.T, a *sqlite.Adapter, parentPath, childPath string) (parentID, childID int64) {
	t.Helper()
	ctx := context.Background()

	parentFile, err := a.WriteFile(ctx, &model.File{Path: parentPath, Language: "go", IndexedAt: time.Now()})
	if err != nil {
		t.Fatalf("WriteFile parent: %v", err)
	}
	childFile := parentFile
	if childPath != parentPath {
		childFile, err = a.WriteFile(ctx, &model.File{Path: childPath, Language: "go", IndexedAt: time.Now()})
		if err != nil {
			t.Fatalf("WriteFile child: %v", err)
		}
	}

	parentID, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: parentFile, Name: "Store", Qualified: "mvcc.Store",
		Kind: model.KindClass, LineStart: 1, LineEnd: 3,
	})
	if err != nil {
		t.Fatalf("WriteSymbol parent: %v", err)
	}
	childID, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: childFile, Name: "Read", Qualified: "mvcc.Store.Read",
		Kind: model.KindMethod, ParentID: &parentID, LineStart: 1, LineEnd: 2,
	})
	if err != nil {
		t.Fatalf("WriteSymbol child: %v", err)
	}
	return parentID, childID
}

// TestDeleteFileDetachesCrossFileChildren pins the FK hazard closed by the
// detach: parent_id carries no ON DELETE action, so deleting the parent's
// file while a child in ANOTHER file still references the parent must not
// trip the foreign key — and the child must end up detached (NULL), not
// dangling.
func TestDeleteFileDetachesCrossFileChildren(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	_, childID := writeParentChildFixture(t, a, "kvstore.go", "kvstore_txn.go")

	if err := a.DeleteFile(ctx, "kvstore.go"); err != nil {
		t.Fatalf("DeleteFile with cross-file child: %v", err)
	}

	var parent any
	err = a.DB().QueryRowContext(ctx,
		"SELECT parent_id FROM sense_symbols WHERE id = ?", childID).Scan(&parent)
	if err != nil {
		t.Fatalf("read child after delete: %v", err)
	}
	if parent != nil {
		t.Errorf("child parent_id = %v after parent file delete, want NULL", parent)
	}
}

// TestDeleteFileSameFileParentStillCascades pins the pre-existing shape:
// parent and child in the same file both leave with the file, and the
// detach does not disturb links in files that survive.
func TestDeleteFileSameFileParentStillCascades(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	_, _ = writeParentChildFixture(t, a, "same.go", "same.go")
	survivorParent, survivorChild := writeParentChildFixtureNamed(t, a, "other.go", "other.go", "sql.Engine", "sql.Engine.Query")

	if err := a.DeleteFile(ctx, "same.go"); err != nil {
		t.Fatalf("DeleteFile same-file parent+child: %v", err)
	}

	var n int
	if err := a.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sense_symbols WHERE qualified LIKE 'mvcc.%'").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("deleted file left %d symbols behind", n)
	}

	var got int64
	if err := a.DB().QueryRowContext(ctx,
		"SELECT parent_id FROM sense_symbols WHERE id = ?", survivorChild).Scan(&got); err != nil {
		t.Fatalf("surviving child lost its parent link: %v", err)
	}
	if got != survivorParent {
		t.Errorf("surviving child parent_id = %d, want %d", got, survivorParent)
	}
}

func writeParentChildFixtureNamed(t *testing.T, a *sqlite.Adapter, parentPath, childPath, parentQual, childQual string) (parentID, childID int64) {
	t.Helper()
	ctx := context.Background()

	parentFile, err := a.WriteFile(ctx, &model.File{Path: parentPath, Language: "go", IndexedAt: time.Now()})
	if err != nil {
		t.Fatalf("WriteFile parent: %v", err)
	}
	childFile := parentFile
	if childPath != parentPath {
		childFile, err = a.WriteFile(ctx, &model.File{Path: childPath, Language: "go", IndexedAt: time.Now()})
		if err != nil {
			t.Fatalf("WriteFile child: %v", err)
		}
	}
	parentID, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: parentFile, Name: "X", Qualified: parentQual,
		Kind: model.KindClass, LineStart: 1, LineEnd: 3,
	})
	if err != nil {
		t.Fatalf("WriteSymbol parent: %v", err)
	}
	childID, err = a.WriteSymbol(ctx, &model.Symbol{
		FileID: childFile, Name: "Y", Qualified: childQual,
		Kind: model.KindMethod, ParentID: &parentID, LineStart: 1, LineEnd: 2,
	})
	if err != nil {
		t.Fatalf("WriteSymbol child: %v", err)
	}
	return parentID, childID
}
