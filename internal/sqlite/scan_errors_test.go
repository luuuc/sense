package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// corruptColumn writes a non-integer string into an integer column on a pinned
// connection with foreign-key enforcement disabled, so the bad value lands.
// Reads don't re-check FKs or types, so the next query that scans that column
// into an int64 hits its rows.Scan error return. The connection is released
// before the read because the adapter pins the pool to one connection.
func corruptColumn(t *testing.T, a *sqlite.Adapter, table, column string, id int64) {
	t.Helper()
	ctx := context.Background()
	conn, err := a.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable FK: %v", err)
	}
	if _, err := conn.ExecContext(ctx,
		"UPDATE "+table+" SET "+column+"='not-an-int' WHERE id=?", id); err != nil {
		t.Fatalf("corrupt %s.%s: %v", table, column, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
}

// closedReadAdapter opens an adapter, seeds it, then closes the handle so every
// subsequent query fails at QueryContext.
func closedSeededAdapter(t *testing.T, seed func(a *sqlite.Adapter)) *sqlite.Adapter {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if seed != nil {
		seed(a)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return a
}

// --- scan errors: a corrupt int column surfaces the rows.Scan error path. ---

// A non-integer parent_id makes the ContextForFile "defines" scan fail; the
// row survives the `parent_id IS NOT NULL` filter, so the int64 scan is
// attempted and its error propagates out of ContextForFile.
func TestContextForFileDefinesScanError(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "ctx.rb", "ruby", "h1")
	classID := seedSymbol(t, a, fid, "C", "C", "class")
	childID := seedSymbolWithParent(t, a, fid, "m", "C#m", "method", classID)
	corruptColumn(t, a, "sense_symbols", "parent_id", childID)

	if _, err := a.ContextForFile(ctx, fid); err == nil {
		t.Error("expected scan error from ContextForFile defines, got nil")
	}
}

// A non-integer file_id makes the Query row scan fail. The zero filter keeps the
// corrupt row in the result set so its scan is attempted.
func TestQueryScanError(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "q.go", "go", "h1")
	sid := seedSymbol(t, a, fid, "A", "pkg.A", "function")
	corruptColumn(t, a, "sense_symbols", "file_id", sid)

	if _, err := a.Query(ctx, index.Filter{}); err == nil {
		t.Error("expected scan error from Query, got nil")
	}
}

// A non-integer source_id makes the loadEdges inbound scan fail (the value is
// scanned via COALESCE into the edge's NullInt64 source, then the target's id).
func TestLoadEdgesScanError(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "le.go", "go", "h1")
	src := seedSymbol(t, a, fid, "Src", "pkg.Src", "function")
	tgt := seedSymbol(t, a, fid, "Tgt", "pkg.Tgt", "function")
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &src, TargetID: tgt, Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	// Corrupt the edge file_id, which loadEdges scans into e.FileID (int64).
	conn, err := a.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE sense_edges SET file_id='not-an-int'"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}

	// ReadSymbol of the target loads inbound edges → hits the loadEdges scan.
	if _, err := a.ReadSymbol(ctx, tgt); err == nil {
		t.Error("expected scan error from loadEdges, got nil")
	}
}

// --- query errors: a closed handle surfaces the QueryContext error path. ---

func TestKeySymbolsQueryErrorsOnClosedDB(t *testing.T) {
	ctx := context.Background()
	a := closedSeededAdapter(t, nil)

	t.Run("TopSymbolsByReach", func(t *testing.T) {
		if _, err := a.TopSymbolsByReach(ctx, "internal/", 10); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("TopCallers", func(t *testing.T) {
		if _, err := a.TopCallers(ctx, 1, 3); err == nil {
			t.Error("expected error")
		}
	})
	t.Run("CallersOfTargets", func(t *testing.T) {
		if _, err := a.CallersOfTargets(ctx, []int64{1}, 0, 10); err == nil {
			t.Error("expected error")
		}
	})
}

func TestDispatchQueryErrorsOnClosedDB(t *testing.T) {
	ctx := context.Background()
	a := closedSeededAdapter(t, nil)

	// InterfaceAliveMethods runs a single QueryContext that fails on a closed db.
	if _, err := sqlite.InterfaceAliveMethods(ctx, a.DB()); err == nil {
		t.Error("expected error from InterfaceAliveMethods on closed db")
	}
}
