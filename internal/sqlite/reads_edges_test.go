package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// openWithSeed opens a fresh adapter and returns it plus the underlying
// *sql.DB so a test can insert rows the typed Write* helpers reject (e.g. a
// malformed indexed_at timestamp).
func openWithSeed(t *testing.T) (*sqlite.Adapter, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "reads.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, a.DB()
}

// A symbol row whose file carries an unparseable indexed_at timestamp makes
// ReadSymbol fail at the time.Parse step, after the row scan succeeds.
func TestReadSymbolMalformedIndexedAt(t *testing.T) {
	ctx := context.Background()
	a, db := openWithSeed(t)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at)
		 VALUES (1, 'bad.go', 'go', 'h', 1, 'not-a-timestamp')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sense_symbols (id, file_id, name, qualified, kind, line_start, line_end)
		 VALUES (1, 1, 'X', 'pkg.X', 'function', 1, 5)`); err != nil {
		t.Fatal(err)
	}

	if _, err := a.ReadSymbol(ctx, 1); err == nil {
		t.Fatal("expected parse error for malformed indexed_at, got nil")
	}
}

// ReadSymbol on an absent id returns index.ErrNotFound, not a generic error.
func TestReadSymbolNotFound(t *testing.T) {
	ctx := context.Background()
	a, _ := openWithSeed(t)

	_, err := a.ReadSymbol(ctx, 4242)
	if !errors.Is(err, index.ErrNotFound) {
		t.Fatalf("expected index.ErrNotFound, got %v", err)
	}
}

// EdgesOfKind hydrates the nullable source_id and line columns: an unresolved
// edge (nil source) and a resolved one (non-nil source, with a line) exercise
// both sides of the NullInt64 branches.
func TestEdgesOfKindNullableColumns(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "edges.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "e.go", Language: "go", Hash: "h", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	srcID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Src", Qualified: "pkg.Src", Kind: "function", LineStart: 1, LineEnd: 5,
	})
	tgtID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Tgt", Qualified: "pkg.Tgt", Kind: "function", LineStart: 10, LineEnd: 15,
	})

	line := 3
	// Resolved edge: source + line present.
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(srcID), TargetID: tgtID,
		Kind: model.EdgeCalls, FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	// Unresolved edge: nil source, nil line.
	if _, err := a.WriteEdge(ctx, &model.Edge{
		TargetID: tgtID, Kind: model.EdgeCalls, FileID: fid, Confidence: 0.5,
	}); err != nil {
		t.Fatal(err)
	}

	edges, err := a.EdgesOfKind(ctx, model.EdgeCalls)
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(edges))
	}

	var sawResolved, sawUnresolved bool
	for _, e := range edges {
		if e.SourceID != nil && *e.SourceID == srcID {
			sawResolved = true
			if e.Line == nil || *e.Line != line {
				t.Errorf("resolved edge line = %v, want %d", e.Line, line)
			}
		}
		if e.SourceID == nil {
			sawUnresolved = true
			if e.Line != nil {
				t.Errorf("unresolved edge line = %v, want nil", e.Line)
			}
		}
	}
	if !sawResolved || !sawUnresolved {
		t.Errorf("missing edge variants: resolved=%v unresolved=%v", sawResolved, sawUnresolved)
	}
}

// FilePaths lists every tracked path.
func TestFilePaths(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "paths.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	for _, p := range []string{"x.go", "y.go", "z.rb"} {
		if _, err := a.WriteFile(ctx, &model.File{
			Path: p, Language: "go", Hash: p, Symbols: 0, IndexedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d: %v", len(paths), paths)
	}
}

// Query with a limit caps the result set and the kind/name/file filters narrow
// it; the zero filter matches everything.
func TestQueryFiltersAndLimit(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "query.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, _ := a.WriteFile(ctx, &model.File{
		Path: "q.go", Language: "go", Hash: "h", Symbols: 3, IndexedAt: time.Now(),
	})
	for _, s := range []model.Symbol{
		{FileID: fid, Name: "Foo", Qualified: "pkg.Foo", Kind: "function", LineStart: 1, LineEnd: 5},
		{FileID: fid, Name: "Bar", Qualified: "pkg.Bar", Kind: "type", LineStart: 6, LineEnd: 10},
		{FileID: fid, Name: "Baz", Qualified: "pkg.Baz", Kind: "function", LineStart: 11, LineEnd: 15},
	} {
		sym := s
		if _, err := a.WriteSymbol(ctx, &sym); err != nil {
			t.Fatal(err)
		}
	}

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("zero filter should match all 3, got %d", len(all))
	}

	limited, err := a.Query(ctx, index.Filter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("limit=2 should return 2, got %d", len(limited))
	}

	byKind, err := a.Query(ctx, index.Filter{Kind: "type"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byKind) != 1 || byKind[0].Name != "Bar" {
		t.Fatalf("kind=type should return only Bar, got %v", byKind)
	}

	byName, err := a.Query(ctx, index.Filter{Name: "Foo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) != 1 || byName[0].Name != "Foo" {
		t.Fatalf("name=Foo should return only Foo, got %v", byName)
	}
}
