package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

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
