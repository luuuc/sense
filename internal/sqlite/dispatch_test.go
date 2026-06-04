package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func openDispatchDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")

	adapter, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	_ = adapter.Close()

	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedDispatchFixture(t *testing.T, db *sql.DB) (ifaceMethodID, structMethodID int64) {
	t.Helper()
	ctx := context.Background()

	for _, q := range []string{
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'iface.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z')`,

		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (10, 1, 'I', 'pkg.I', 'interface', 1, 10)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id)
		 VALUES (11, 1, 'M', 'pkg.I.M', 'method', 2, 5, 10)`,

		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (20, 1, 'S', 'pkg.S', 'class', 20, 40)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id)
		 VALUES (21, 1, 'M', 'pkg.S.M', 'method', 22, 30, 20)`,

		// S inherits I
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence)
		 VALUES (20, 10, 'inherits', 1, 1.0)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}
	return 11, 21
}

func TestInterfaceImplementorsQuery(t *testing.T) {
	db := openDispatchDB(t)
	ifaceMethodID, structMethodID := seedDispatchFixture(t, db)
	ctx := context.Background()

	ids, err := sqlite.DispatchMethodIDs(ctx, db, ifaceMethodID)
	if err != nil {
		t.Fatalf("DispatchMethodIDs(interface method): %v", err)
	}
	if len(ids) != 1 || ids[0] != structMethodID {
		t.Errorf("got %v, want [%d]", ids, structMethodID)
	}
}

func TestReverseDispatch(t *testing.T) {
	db := openDispatchDB(t)
	ifaceMethodID, structMethodID := seedDispatchFixture(t, db)
	ctx := context.Background()

	ids, err := sqlite.DispatchMethodIDs(ctx, db, structMethodID)
	if err != nil {
		t.Fatalf("DispatchMethodIDs(struct method): %v", err)
	}
	if len(ids) != 1 || ids[0] != ifaceMethodID {
		t.Errorf("got %v, want [%d]", ids, ifaceMethodID)
	}
}

func TestDispatchNoParent(t *testing.T) {
	db := openDispatchDB(t)
	ctx := context.Background()

	_, err := db.ExecContext(ctx,
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'f.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (1, 1, 'Func', 'pkg.Func', 'function', 1, 5)`)
	if err != nil {
		t.Fatal(err)
	}

	ids, err := sqlite.DispatchMethodIDs(ctx, db, 1)
	if err != nil {
		t.Fatalf("DispatchMethodIDs: %v", err)
	}
	if ids != nil {
		t.Errorf("want nil for parentless symbol, got %v", ids)
	}
}

func TestDispatchMethodIDsMissingSymbol(t *testing.T) {
	db := openDispatchDB(t)
	ctx := context.Background()

	// No symbol with this id: the lookup row scan returns ErrNoRows, which
	// DispatchMethodIDs wraps as an error rather than returning nil.
	if _, err := sqlite.DispatchMethodIDs(ctx, db, 999); err == nil {
		t.Fatal("expected error for nonexistent symbol id, got nil")
	}
}

func TestDispatchMultipleImplementors(t *testing.T) {
	db := openDispatchDB(t)
	ctx := context.Background()

	// Interface Reader with method Read, implemented by FileReader and NetReader.
	for _, q := range []string{
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'io.go', 'go', 'abc', 0, '2026-01-01T00:00:00Z')`,

		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (10, 1, 'Reader', 'io.Reader', 'interface', 1, 5)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id)
		 VALUES (11, 1, 'Read', 'io.Reader.Read', 'method', 2, 3, 10)`,

		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (20, 1, 'FileReader', 'io.FileReader', 'class', 10, 20)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id)
		 VALUES (21, 1, 'Read', 'io.FileReader.Read', 'method', 11, 15, 20)`,

		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (30, 1, 'NetReader', 'io.NetReader', 'class', 25, 40)`,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end, parent_id)
		 VALUES (31, 1, 'Read', 'io.NetReader.Read', 'method', 26, 35, 30)`,

		// Both FileReader and NetReader inherit Reader.
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence)
		 VALUES (20, 10, 'inherits', 1, 1.0)`,
		`INSERT INTO sense_edges (source_id, target_id, kind, file_id, confidence)
		 VALUES (30, 10, 'inherits', 1, 1.0)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\nquery: %s", err, q)
		}
	}

	// From the interface method, should find both concrete implementations.
	ids, err := sqlite.DispatchMethodIDs(ctx, db, 11)
	if err != nil {
		t.Fatalf("DispatchMethodIDs(interface method): %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 implementors, got %v", ids)
	}

	got := map[int64]bool{ids[0]: true, ids[1]: true}
	if !got[21] || !got[31] {
		t.Errorf("expected IDs [21, 31], got %v", ids)
	}
}

func TestInterfaceAliveMethods(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "dispatch.go", "go", "h1")

	// Create an interface with a method
	ifaceID := seedSymbol(t, a, fid, "Processor", "pkg.Processor", "interface")
	ifaceMethodID := seedSymbolWithParent(t, a, fid, "Process", "pkg.Processor.Process", "method", ifaceID)

	// Create a concrete type that inherits from the interface
	implID := seedSymbol(t, a, fid, "Worker", "pkg.Worker", "class")
	seedSymbolWithParent(t, a, fid, "Process", "pkg.Worker.Process", "method", implID)

	// Write an inherits edge: Worker -> Processor
	line := 10
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &implID, TargetID: ifaceID, Kind: model.EdgeInherits,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge inherits: %v", err)
	}

	// Write a calls edge TO the interface method (someone calls it)
	callerID := seedSymbol(t, a, fid, "main", "pkg.main", "function")
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &callerID, TargetID: ifaceMethodID, Kind: model.EdgeCalls,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge calls: %v", err)
	}

	alive, err := sqlite.InterfaceAliveMethods(ctx, a.DB())
	if err != nil {
		t.Fatalf("InterfaceAliveMethods: %v", err)
	}

	// Should have the implementor's parent type + method name
	key := sqlite.InterfaceMethodKey{ParentID: implID, MethodName: "Process"}
	if _, ok := alive[key]; !ok {
		t.Errorf("InterfaceAliveMethods missing key {ParentID: %d, MethodName: Process}", implID)
	}
}

func TestDispatchMethodIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "dispatch.go", "go", "h1")

	// Interface and its method
	ifaceID := seedSymbol(t, a, fid, "Processor", "pkg.Processor", "interface")
	ifaceMethodID := seedSymbolWithParent(t, a, fid, "Process", "pkg.Processor.Process", "method", ifaceID)

	// Concrete type that inherits the interface
	implID := seedSymbol(t, a, fid, "Worker", "pkg.Worker", "class")
	implMethodID := seedSymbolWithParent(t, a, fid, "Process", "pkg.Worker.Process", "method", implID)

	line := 10
	// inherits: Worker -> Processor
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &implID, TargetID: ifaceID, Kind: model.EdgeInherits,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge inherits: %v", err)
	}

	// From interface method: should find dispatch targets on implementors
	ids, err := sqlite.DispatchMethodIDs(ctx, a.DB(), ifaceMethodID)
	if err != nil {
		t.Fatalf("DispatchMethodIDs from interface: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == implMethodID {
			found = true
		}
	}
	if !found {
		t.Errorf("DispatchMethodIDs from interface should include impl method %d, got %v", implMethodID, ids)
	}

	// From impl method: should find dispatch targets on the interface
	ids, err = sqlite.DispatchMethodIDs(ctx, a.DB(), implMethodID)
	if err != nil {
		t.Fatalf("DispatchMethodIDs from concrete: %v", err)
	}
	found = false
	for _, id := range ids {
		if id == ifaceMethodID {
			found = true
		}
	}
	if !found {
		t.Errorf("DispatchMethodIDs from concrete should include interface method %d, got %v", ifaceMethodID, ids)
	}

	// Symbol without parent
	noParent := seedSymbol(t, a, fid, "standalone", "pkg.standalone", "function")
	ids, err = sqlite.DispatchMethodIDs(ctx, a.DB(), noParent)
	if err != nil {
		t.Fatalf("DispatchMethodIDs no parent: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("DispatchMethodIDs no parent = %v, want empty", ids)
	}
}
