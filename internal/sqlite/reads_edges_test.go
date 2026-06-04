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

func TestFileMeta(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// Missing file
	id, hash, err := a.FileMeta(ctx, "nonexistent.go")
	if err != nil {
		t.Fatalf("FileMeta missing: %v", err)
	}
	if id != 0 || hash != "" {
		t.Errorf("FileMeta missing = (%d, %q), want (0, \"\")", id, hash)
	}

	// Existing file
	seedFile(t, a, "main.go", "go", "abc123")
	id, hash, err = a.FileMeta(ctx, "main.go")
	if err != nil {
		t.Fatalf("FileMeta: %v", err)
	}
	if id == 0 {
		t.Error("FileMeta should return non-zero id")
	}
	if hash != "abc123" {
		t.Errorf("FileMeta hash = %q, want abc123", hash)
	}
}

func TestFileHashMap(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	seedFile(t, a, "a.go", "go", "h1")
	seedFile(t, a, "b.rb", "ruby", "h2")

	m, err := a.FileHashMap(ctx)
	if err != nil {
		t.Fatalf("FileHashMap: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("FileHashMap len = %d, want 2", len(m))
	}
	if m["a.go"].Hash != "h1" {
		t.Errorf("a.go hash = %q, want h1", m["a.go"].Hash)
	}
	if m["b.rb"].Hash != "h2" {
		t.Errorf("b.rb hash = %q, want h2", m["b.rb"].Hash)
	}
	if m["a.go"].ID == 0 {
		t.Error("a.go ID should be non-zero")
	}
}

func TestSymbolRefs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	seedSymbol(t, a, fid, "Process", "pkg.Process", "function")

	refs, err := a.SymbolRefs(ctx)
	if err != nil {
		t.Fatalf("SymbolRefs: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("SymbolRefs len = %d, want 2", len(refs))
	}
	// Should be ordered by id ascending
	if refs[0].ID >= refs[1].ID {
		t.Error("SymbolRefs not ordered by ascending id")
	}
	seen := map[string]bool{}
	for _, r := range refs {
		seen[r.Qualified] = true
	}
	if !seen["pkg.Order"] {
		t.Error("missing ref for pkg.Order")
	}
	if !seen["pkg.Process"] {
		t.Error("missing ref for pkg.Process")
	}
}

func TestSymbolRefsCarriesReceiver(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "price.rb", "ruby", "h1")
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "zero", Qualified: "PriceValue.zero",
		Kind: model.KindMethod, Receiver: "singleton", LineStart: 1, LineEnd: 3,
	}); err != nil {
		t.Fatalf("WriteSymbol singleton: %v", err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "zero?", Qualified: "PriceValue#zero?",
		Kind: model.KindMethod, Receiver: "instance", LineStart: 5, LineEnd: 7,
	}); err != nil {
		t.Fatalf("WriteSymbol instance: %v", err)
	}

	refs, err := a.SymbolRefs(ctx)
	if err != nil {
		t.Fatalf("SymbolRefs: %v", err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Qualified] = r.Receiver
	}
	if got["PriceValue.zero"] != "singleton" {
		t.Errorf("PriceValue.zero Receiver = %q, want singleton", got["PriceValue.zero"])
	}
	if got["PriceValue#zero?"] != "instance" {
		t.Errorf("PriceValue#zero? Receiver = %q, want instance", got["PriceValue#zero?"])
	}
}

func TestSymbolRefsCarriesLanguage(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	rb := seedFile(t, a, "app/models/user.rb", "ruby", "h1")
	js := seedFile(t, a, "app/javascript/controllers/application.js", "javascript", "h2")
	seedSymbol(t, a, rb, "User", "User", "class")
	seedSymbol(t, a, js, "application", "application", "constant")

	refs, err := a.SymbolRefs(ctx)
	if err != nil {
		t.Fatalf("SymbolRefs: %v", err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Qualified] = r.Language
	}
	if got["User"] != "ruby" {
		t.Errorf("User Language = %q, want ruby (left-joined from sense_files)", got["User"])
	}
	if got["application"] != "javascript" {
		t.Errorf("application Language = %q, want javascript", got["application"])
	}
}

func TestSymbolRefsCarriesPath(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	app := seedFile(t, a, "app/models/user.rb", "ruby", "h1")
	test := seedFile(t, a, "test/models/user_test.rb", "ruby", "h2")
	seedSymbol(t, a, app, "User", "User", "class")
	seedSymbol(t, a, test, "UserTest", "UserTest", "class")

	refs, err := a.SymbolRefs(ctx)
	if err != nil {
		t.Fatalf("SymbolRefs: %v", err)
	}
	got := map[string]string{}
	for _, r := range refs {
		got[r.Qualified] = r.Path
	}
	if got["User"] != "app/models/user.rb" {
		t.Errorf("User Path = %q, want app/models/user.rb (left-joined from sense_files)", got["User"])
	}
	if got["UserTest"] != "test/models/user_test.rb" {
		t.Errorf("UserTest Path = %q, want test/models/user_test.rb", got["UserTest"])
	}
}

func TestSymbolRefsScanError(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app/x.rb", "ruby", "h1")
	sid := seedSymbol(t, a, fid, "X", "X", "class")
	// Corrupt file_id to a non-integer so the row fails to scan into int64
	// (SQLite is dynamically typed), exercising the scan-error path. The write
	// is done on a pinned connection with FK enforcement off so the bad value
	// lands; reads don't check FKs, so SymbolRefs still hits the scan failure.
	// The connection is released before SymbolRefs — the adapter caps the pool
	// at one connection, so holding it would deadlock the subsequent read.
	conn, err := a.DB().Conn(ctx)
	if err != nil {
		t.Fatalf("Conn: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable FK: %v", err)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE sense_symbols SET file_id='not-an-int' WHERE id=?", sid); err != nil {
		t.Fatalf("corrupt file_id: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close conn: %v", err)
	}
	if _, err := a.SymbolRefs(ctx); err == nil {
		t.Error("expected a scan error for a non-integer file_id")
	}
}

func TestEdgesOfKind(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "edge.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	line := 5
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &s1, TargetID: s2, Kind: model.EdgeInherits,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge: %v", err)
	}
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &s1, TargetID: s2, Kind: model.EdgeCalls,
		FileID: fid, Confidence: 0.9,
	}); err != nil {
		t.Fatalf("WriteEdge calls: %v", err)
	}

	edges, err := a.EdgesOfKind(ctx, model.EdgeInherits)
	if err != nil {
		t.Fatalf("EdgesOfKind: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("EdgesOfKind inherits len = %d, want 1", len(edges))
	}
	if edges[0].Kind != model.EdgeInherits {
		t.Errorf("edge kind = %v, want inherits", edges[0].Kind)
	}
	if edges[0].SourceID == nil || *edges[0].SourceID != s1 {
		t.Error("edge source should be s1")
	}
	if edges[0].Line == nil || *edges[0].Line != 5 {
		t.Error("edge line should be 5")
	}

	// Query the other kind
	callEdges, err := a.EdgesOfKind(ctx, model.EdgeCalls)
	if err != nil {
		t.Fatalf("EdgesOfKind calls: %v", err)
	}
	if len(callEdges) != 1 {
		t.Fatalf("EdgesOfKind calls len = %d, want 1", len(callEdges))
	}
}

func TestFileIDsByLanguage(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	seedFile(t, a, "a.go", "go", "h1")
	seedFile(t, a, "b.go", "go", "h2")
	seedFile(t, a, "c.rb", "ruby", "h3")

	goFiles, err := a.FileIDsByLanguage(ctx, "go")
	if err != nil {
		t.Fatalf("FileIDsByLanguage go: %v", err)
	}
	if len(goFiles) != 2 {
		t.Errorf("go files = %d, want 2", len(goFiles))
	}

	rubyFiles, err := a.FileIDsByLanguage(ctx, "ruby")
	if err != nil {
		t.Fatalf("FileIDsByLanguage ruby: %v", err)
	}
	if len(rubyFiles) != 1 {
		t.Errorf("ruby files = %d, want 1", len(rubyFiles))
	}

	// Non-existent language
	pyFiles, err := a.FileIDsByLanguage(ctx, "python")
	if err != nil {
		t.Fatalf("FileIDsByLanguage python: %v", err)
	}
	if len(pyFiles) != 0 {
		t.Errorf("python files = %d, want 0", len(pyFiles))
	}
}

func TestReadSymbol(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	sid := seedSymbol(t, a, fid, "Order", "pkg.Order", "class")

	sc, err := a.ReadSymbol(ctx, sid)
	if err != nil {
		t.Fatalf("ReadSymbol: %v", err)
	}
	if sc.Symbol.Name != "Order" {
		t.Errorf("Symbol.Name = %q, want Order", sc.Symbol.Name)
	}
	if sc.Symbol.Qualified != "pkg.Order" {
		t.Errorf("Symbol.Qualified = %q, want pkg.Order", sc.Symbol.Qualified)
	}
	if sc.File.Path != "app.go" {
		t.Errorf("File.Path = %q, want app.go", sc.File.Path)
	}

	// Non-existent symbol
	_, err = a.ReadSymbol(ctx, 99999)
	if err == nil {
		t.Error("ReadSymbol nonexistent should return error")
	}
}
