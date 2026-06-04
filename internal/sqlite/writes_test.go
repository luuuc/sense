package sqlite_test

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

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
