package sqlite_test

import (
	"context"
	"encoding/binary"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
	_ "modernc.org/sqlite"
)

// openTestDB creates a fresh in-tempdir database for a test.
func openTestDB(t *testing.T) *sqlite.Adapter {
	t.Helper()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")
	a, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// seedFile inserts a file and returns its ID.
func seedFile(t *testing.T, a *sqlite.Adapter, path, lang, hash string) int64 {
	t.Helper()
	ctx := context.Background()
	fid, err := a.WriteFile(ctx, &model.File{
		Path: path, Language: lang, Hash: hash,
		Symbols: 0, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
	return fid
}

// seedSymbol inserts a symbol and returns its ID.
func seedSymbol(t *testing.T, a *sqlite.Adapter, fid int64, name, qualified, kind string) int64 {
	t.Helper()
	ctx := context.Background()
	sid, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: name, Qualified: qualified,
		Kind: model.SymbolKind(kind), LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatalf("WriteSymbol(%s): %v", qualified, err)
	}
	return sid
}

func TestInTx(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// Successful transaction
	err := a.InTx(ctx, func() error {
		fid := seedFile(t, a, "tx.go", "go", "h1")
		seedSymbol(t, a, fid, "F", "pkg.F", "function")
		return nil
	})
	if err != nil {
		t.Fatalf("InTx success: %v", err)
	}

	// Verify data persisted
	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "tx.go" {
		t.Errorf("FilePaths = %v, want [tx.go]", paths)
	}
}

func TestInTxRollback(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// Seed a file before the transaction
	seedFile(t, a, "before.go", "go", "h0")

	// Failing transaction should rollback
	err := a.InTx(ctx, func() error {
		seedFile(t, a, "rollback.go", "go", "h2")
		return context.Canceled // simulate error
	})
	if err == nil {
		t.Fatal("InTx should propagate error")
	}

	// The rolled-back file should not be visible (but since InTx uses
	// single-conn trick with BEGIN IMMEDIATE, the effects of WriteFile
	// inside fn() already went through the same connection — rollback
	// reverts them). Verify "before.go" persists.
	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	// The before.go was written outside the transaction, so it persists
	found := false
	for _, p := range paths {
		if p == "before.go" {
			found = true
		}
	}
	if !found {
		t.Error("before.go should persist after rollback")
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

func TestPreparedStatements(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "prep.go", "go", "h1")

	// Test PrepareSymbolStmt + ExecSymbolStmt
	symStmt, err := a.PrepareSymbolStmt(ctx)
	if err != nil {
		t.Fatalf("PrepareSymbolStmt: %v", err)
	}
	defer func() { _ = symStmt.Close() }()

	sid, err := sqlite.ExecSymbolStmt(ctx, symStmt, &model.Symbol{
		FileID: fid, Name: "F", Qualified: "pkg.F",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatalf("ExecSymbolStmt: %v", err)
	}
	if sid == 0 {
		t.Error("ExecSymbolStmt returned 0 id")
	}

	sid2, err := sqlite.ExecSymbolStmt(ctx, symStmt, &model.Symbol{
		FileID: fid, Name: "G", Qualified: "pkg.G",
		Kind: "function", LineStart: 10, LineEnd: 15,
	})
	if err != nil {
		t.Fatalf("ExecSymbolStmt second: %v", err)
	}
	if sid2 == 0 || sid2 == sid {
		t.Error("second symbol should have different non-zero id")
	}

	// Test PrepareEdgeStmt + ExecEdgeStmt
	edgeStmt, err := a.PrepareEdgeStmt(ctx)
	if err != nil {
		t.Fatalf("PrepareEdgeStmt: %v", err)
	}
	defer func() { _ = edgeStmt.Close() }()

	eid, err := sqlite.ExecEdgeStmt(ctx, edgeStmt, &model.Edge{
		SourceID: &sid, TargetID: sid2, Kind: model.EdgeCalls,
		FileID: fid, Confidence: 1.0,
	})
	if err != nil {
		t.Fatalf("ExecEdgeStmt: %v", err)
	}
	if eid == 0 {
		t.Error("ExecEdgeStmt returned 0 id")
	}

	// Test PrepareEmbeddingStmt
	embedStmt, err := a.PrepareEmbeddingStmt(ctx)
	if err != nil {
		t.Fatalf("PrepareEmbeddingStmt: %v", err)
	}
	defer func() { _ = embedStmt.Close() }()

	_, err = embedStmt.ExecContext(ctx, sid, []byte{4, 5, 6})
	if err != nil {
		t.Fatalf("PrepareEmbeddingStmt exec: %v", err)
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

func TestDeleteMeta(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	if err := a.WriteMeta(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("WriteMeta: %v", err)
	}

	val, err := a.ReadMeta(ctx, "test_key")
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if val != "test_value" {
		t.Errorf("ReadMeta = %q, want test_value", val)
	}

	if err := a.DeleteMeta(ctx, "test_key"); err != nil {
		t.Fatalf("DeleteMeta: %v", err)
	}

	val, err = a.ReadMeta(ctx, "test_key")
	if err != nil {
		t.Fatalf("ReadMeta after delete: %v", err)
	}
	if val != "" {
		t.Errorf("ReadMeta after delete = %q, want empty", val)
	}

	// Delete non-existent key should not error
	if err := a.DeleteMeta(ctx, "nonexistent"); err != nil {
		t.Fatalf("DeleteMeta nonexistent: %v", err)
	}
}

func TestEdgeWithNilLine(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "edge.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	// Edge without line information
	eid, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &s1, TargetID: s2, Kind: model.EdgeCalls,
		FileID: fid, Line: nil, Confidence: 0.8,
	})
	if err != nil {
		t.Fatalf("WriteEdge nil line: %v", err)
	}
	if eid == 0 {
		t.Error("WriteEdge returned 0 id")
	}

	edges, err := a.EdgesOfKind(ctx, model.EdgeCalls)
	if err != nil {
		t.Fatalf("EdgesOfKind: %v", err)
	}
	if len(edges) != 1 {
		t.Fatalf("EdgesOfKind len = %d, want 1", len(edges))
	}
	if edges[0].Line != nil {
		t.Error("edge line should be nil")
	}
}

func TestEdgeWithNilSourceID(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "edge.go", "go", "h1")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	// Edge with nil source (unresolved edge)
	eid, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: nil, TargetID: s2, Kind: model.EdgeCalls,
		FileID: fid, Confidence: 0.5,
	})
	if err != nil {
		t.Fatalf("WriteEdge nil source: %v", err)
	}
	if eid == 0 {
		t.Error("WriteEdge returned 0 id")
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

// seedSymbolWithParent inserts a symbol with a parent_id and returns its ID.
func seedSymbolWithParent(t *testing.T, a *sqlite.Adapter, fid int64, name, qualified, kind string, parentID int64) int64 {
	t.Helper()
	ctx := context.Background()
	sid, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: name, Qualified: qualified,
		Kind: model.SymbolKind(kind), LineStart: 1, LineEnd: 10,
		ParentID: &parentID,
	})
	if err != nil {
		t.Fatalf("WriteSymbol(%s): %v", qualified, err)
	}
	return sid
}

func TestSymbolIDsForPaths(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid1 := seedFile(t, a, "src/main.go", "go", "h1")
	fid2 := seedFile(t, a, "src/util.go", "go", "h2")
	seedSymbol(t, a, fid1, "Main", "pkg.Main", "function")
	seedSymbol(t, a, fid1, "Init", "pkg.Init", "function")
	seedSymbol(t, a, fid2, "Helper", "pkg.Helper", "function")

	// Query both paths
	ids, err := a.SymbolIDsForPaths(ctx, []string{"src/main.go", "src/util.go"})
	if err != nil {
		t.Fatalf("SymbolIDsForPaths: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("SymbolIDsForPaths len = %d, want 3", len(ids))
	}

	// Query single path
	ids, err = a.SymbolIDsForPaths(ctx, []string{"src/main.go"})
	if err != nil {
		t.Fatalf("SymbolIDsForPaths single: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("SymbolIDsForPaths single len = %d, want 2", len(ids))
	}

	// Empty input
	ids, err = a.SymbolIDsForPaths(ctx, nil)
	if err != nil {
		t.Fatalf("SymbolIDsForPaths empty: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("SymbolIDsForPaths empty len = %d, want 0", len(ids))
	}

	// Non-existent path
	ids, err = a.SymbolIDsForPaths(ctx, []string{"nonexistent.go"})
	if err != nil {
		t.Fatalf("SymbolIDsForPaths nonexistent: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("SymbolIDsForPaths nonexistent len = %d, want 0", len(ids))
	}

	_ = fid2 // used via seedSymbol
}

func TestSymbolsByIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	s2 := seedSymbol(t, a, fid, "Process", "pkg.Process", "function")

	// Query both
	result, err := a.SymbolsByIDs(ctx, []int64{s1, s2})
	if err != nil {
		t.Fatalf("SymbolsByIDs: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("SymbolsByIDs len = %d, want 2", len(result))
	}
	if result[s1].Name != "Order" {
		t.Errorf("result[s1].Name = %q, want Order", result[s1].Name)
	}
	if result[s2].Kind != "function" {
		t.Errorf("result[s2].Kind = %q, want function", result[s2].Kind)
	}

	// Empty input
	result, err = a.SymbolsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("SymbolsByIDs empty: %v", err)
	}
	if result != nil {
		t.Errorf("SymbolsByIDs empty = %v, want nil", result)
	}

	// Non-existent ID
	result, err = a.SymbolsByIDs(ctx, []int64{99999})
	if err != nil {
		t.Fatalf("SymbolsByIDs nonexistent: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("SymbolsByIDs nonexistent len = %d, want 0", len(result))
	}
}

func TestInboundEdgeCounts(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "edge.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "function")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "function")
	s3 := seedSymbol(t, a, fid, "C", "pkg.C", "function")

	line := 5
	// A calls B, A calls C, B calls C
	for _, e := range []struct{ src, tgt int64 }{
		{s1, s2}, {s1, s3}, {s2, s3},
	} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &e.src, TargetID: e.tgt, Kind: model.EdgeCalls,
			FileID: fid, Line: &line, Confidence: 1.0,
		}); err != nil {
			t.Fatalf("WriteEdge: %v", err)
		}
	}

	counts, err := a.InboundEdgeCounts(ctx, []int64{s1, s2, s3})
	if err != nil {
		t.Fatalf("InboundEdgeCounts: %v", err)
	}
	// s1 has 0 inbound, s2 has 1, s3 has 2
	if counts[s2] != 1 {
		t.Errorf("InboundEdgeCounts[s2] = %d, want 1", counts[s2])
	}
	if counts[s3] != 2 {
		t.Errorf("InboundEdgeCounts[s3] = %d, want 2", counts[s3])
	}
	if _, ok := counts[s1]; ok {
		t.Error("InboundEdgeCounts should not have entry for s1 (0 inbound)")
	}

	// Empty input
	counts, err = a.InboundEdgeCounts(ctx, nil)
	if err != nil {
		t.Fatalf("InboundEdgeCounts empty: %v", err)
	}
	if counts != nil {
		t.Errorf("InboundEdgeCounts empty = %v, want nil", counts)
	}
}

func TestCalleeIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "call.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "function")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "function")
	s3 := seedSymbol(t, a, fid, "C", "pkg.C", "function")

	line := 5
	// A calls B, A calls C
	for _, tgt := range []int64{s2, s3} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &s1, TargetID: tgt, Kind: model.EdgeCalls,
			FileID: fid, Line: &line, Confidence: 1.0,
		}); err != nil {
			t.Fatalf("WriteEdge: %v", err)
		}
	}
	// Also add a non-calls edge to make sure it is filtered out
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &s1, TargetID: s2, Kind: model.EdgeInherits,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge inherits: %v", err)
	}

	callees, err := a.CalleeIDs(ctx, []int64{s1})
	if err != nil {
		t.Fatalf("CalleeIDs: %v", err)
	}
	if len(callees[s1]) != 2 {
		t.Fatalf("CalleeIDs[s1] len = %d, want 2", len(callees[s1]))
	}

	// s2 has no outbound calls edges
	if len(callees[s2]) != 0 {
		t.Errorf("CalleeIDs[s2] len = %d, want 0", len(callees[s2]))
	}

	// Empty input
	callees, err = a.CalleeIDs(ctx, nil)
	if err != nil {
		t.Fatalf("CalleeIDs empty: %v", err)
	}
	if callees != nil {
		t.Errorf("CalleeIDs empty = %v, want nil", callees)
	}
}

func TestFilePathsByIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid1 := seedFile(t, a, "src/main.go", "go", "h1")
	fid2 := seedFile(t, a, "src/util.go", "go", "h2")

	paths, err := a.FilePathsByIDs(ctx, []int64{fid1, fid2})
	if err != nil {
		t.Fatalf("FilePathsByIDs: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("FilePathsByIDs len = %d, want 2", len(paths))
	}
	if paths[fid1] != "src/main.go" {
		t.Errorf("paths[fid1] = %q, want src/main.go", paths[fid1])
	}
	if paths[fid2] != "src/util.go" {
		t.Errorf("paths[fid2] = %q, want src/util.go", paths[fid2])
	}

	// Empty input returns empty map (not nil)
	paths, err = a.FilePathsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("FilePathsByIDs empty: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("FilePathsByIDs empty len = %d, want 0", len(paths))
	}

	// Non-existent ID
	paths, err = a.FilePathsByIDs(ctx, []int64{99999})
	if err != nil {
		t.Fatalf("FilePathsByIDs nonexistent: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("FilePathsByIDs nonexistent len = %d, want 0", len(paths))
	}
}

func TestParentSymbols(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "model.go", "go", "h1")
	parentID := seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	childID := seedSymbolWithParent(t, a, fid, "Process", "pkg.Order.Process", "method", parentID)
	orphanID := seedSymbol(t, a, fid, "Helper", "pkg.Helper", "function")

	parents, err := a.ParentSymbols(ctx, []int64{childID, orphanID})
	if err != nil {
		t.Fatalf("ParentSymbols: %v", err)
	}
	// childID has a parent, orphanID does not
	pi, ok := parents[childID]
	if !ok {
		t.Fatal("ParentSymbols missing entry for childID")
	}
	if pi.Name != "Order" {
		t.Errorf("parent name = %q, want Order", pi.Name)
	}
	if pi.Qualified != "pkg.Order" {
		t.Errorf("parent qualified = %q, want pkg.Order", pi.Qualified)
	}
	if pi.Kind != "class" {
		t.Errorf("parent kind = %q, want class", pi.Kind)
	}
	if _, ok := parents[orphanID]; ok {
		t.Error("orphan should not have a parent entry")
	}

	// Empty input
	parents, err = a.ParentSymbols(ctx, nil)
	if err != nil {
		t.Fatalf("ParentSymbols empty: %v", err)
	}
	if parents != nil {
		t.Errorf("ParentSymbols empty = %v, want nil", parents)
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

func TestLoadEmbeddings(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	// Write embeddings as float32 vectors encoded as bytes
	vec1 := floatVec(1.0, 2.0, 3.0)
	vec2 := floatVec(4.0, 5.0, 6.0)
	if err := a.WriteEmbedding(ctx, s1, vec1); err != nil {
		t.Fatalf("WriteEmbedding s1: %v", err)
	}
	if err := a.WriteEmbedding(ctx, s2, vec2); err != nil {
		t.Fatalf("WriteEmbedding s2: %v", err)
	}

	result, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatalf("LoadEmbeddings: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("LoadEmbeddings len = %d, want 2", len(result))
	}
	if len(result[s1]) != 3 {
		t.Errorf("result[s1] len = %d, want 3", len(result[s1]))
	}
}

func TestEmbeddingsForFiles(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid1 := seedFile(t, a, "a.go", "go", "h1")
	fid2 := seedFile(t, a, "b.go", "go", "h2")
	s1 := seedSymbol(t, a, fid1, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid2, "B", "pkg.B", "class")

	if err := a.WriteEmbedding(ctx, s1, floatVec(1.0, 2.0)); err != nil {
		t.Fatalf("WriteEmbedding s1: %v", err)
	}
	if err := a.WriteEmbedding(ctx, s2, floatVec(3.0, 4.0)); err != nil {
		t.Fatalf("WriteEmbedding s2: %v", err)
	}

	// Query for fid1 only
	result, err := a.EmbeddingsForFiles(ctx, []int64{fid1})
	if err != nil {
		t.Fatalf("EmbeddingsForFiles: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("EmbeddingsForFiles len = %d, want 1", len(result))
	}
	if _, ok := result[s1]; !ok {
		t.Error("EmbeddingsForFiles should contain s1")
	}

	// Empty input
	result, err = a.EmbeddingsForFiles(ctx, nil)
	if err != nil {
		t.Fatalf("EmbeddingsForFiles empty: %v", err)
	}
	if result != nil {
		t.Errorf("EmbeddingsForFiles empty = %v, want nil", result)
	}
}

func TestDeleteFile(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	seedFile(t, a, "remove.go", "go", "h1")
	seedFile(t, a, "keep.go", "go", "h2")

	if err := a.DeleteFile(ctx, "remove.go"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "keep.go" {
		t.Errorf("FilePaths after delete = %v, want [keep.go]", paths)
	}

	// Delete non-existent path should not error
	if err := a.DeleteFile(ctx, "nonexistent.go"); err != nil {
		t.Fatalf("DeleteFile nonexistent: %v", err)
	}
}

func TestClear(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	sid := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	line := 5
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &sid, TargetID: sid, Kind: model.EdgeCalls,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge: %v", err)
	}
	if err := a.WriteEmbedding(ctx, sid, floatVec(1.0)); err != nil {
		t.Fatalf("WriteEmbedding: %v", err)
	}

	if err := a.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	paths, err := a.FilePaths(ctx)
	if err != nil {
		t.Fatalf("FilePaths after clear: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("FilePaths after clear = %v, want empty", paths)
	}

	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount after clear: %v", err)
	}
	if count != 0 {
		t.Errorf("SymbolCount after clear = %d, want 0", count)
	}
}

func TestSymbolCountEmptyAndPopulated(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount empty: %v", err)
	}
	if count != 0 {
		t.Errorf("SymbolCount empty = %d, want 0", count)
	}

	fid := seedFile(t, a, "app.go", "go", "h1")
	seedSymbol(t, a, fid, "A", "pkg.A", "class")
	seedSymbol(t, a, fid, "B", "pkg.B", "function")

	count, err = a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount: %v", err)
	}
	if count != 2 {
		t.Errorf("SymbolCount = %d, want 2", count)
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

func TestStampSchemaVersion(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	// StampSchemaVersion sets PRAGMA user_version; should not error
	if err := a.StampSchemaVersion(ctx); err != nil {
		t.Fatalf("StampSchemaVersion: %v", err)
	}

	// Verify by reading PRAGMA user_version
	var version int
	if err := a.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if version == 0 {
		t.Error("user_version should not be 0 after stamp")
	}
}

// floatVec encodes float32 values into a little-endian byte slice.
func floatVec(values ...float32) []byte {
	buf := make([]byte, len(values)*4)
	for i, v := range values {
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(buf[i*4:], bits)
	}
	return buf
}
