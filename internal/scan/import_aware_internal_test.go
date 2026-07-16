package scan

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/model"
)

// faultEdgesOfKindStore fails the includes-adjacency load, proving the Go
// embedding map's DB half fails the resolve pass loudly instead of silently
// starving the walk (a quiet miss would nondeterministically demote healed
// bands, the exact class the incremental leg exists to prevent).
type faultEdgesOfKindStore struct {
	indexStore
}

func (f *faultEdgesOfKindStore) EdgesOfKind(context.Context, model.EdgeKind) ([]model.Edge, error) {
	return nil, errors.New("boom")
}

func TestResolvePassFailsLoudWhenAdjacencyLoadFails(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "go.mod", "module corp/app\n")
	writeSource(t, dir, "a.go", "package p\n\ntype B struct{}\n\ntype S struct {\n\tB\n}\n")

	adapter := openIndex(t, dir)
	h := &harness{
		ctx:            context.Background(),
		idx:            &faultEdgesOfKindStore{indexStore: adapter},
		out:            io.Discard,
		warn:           io.Discard,
		root:           dir,
		matcher:        ignore.New(ignore.DefaultPatterns()...),
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
		collector:      newWarningCollector(),
		progress:       &progress{},
		seenPaths:      map[string]bool{},
	}
	// A pending includes edge with an import annotation activates the Go
	// lane, which needs the DB adjacency; the load failure must surface.
	h.pendingEdges = []pendingEdge{
		{SourceID: 1, TargetName: "ctx.Do", Kind: model.EdgeCalls, FileID: 1,
			Confidence: 1.0, TargetImportPath: "corp/app/p", TargetInPackage: "T.Do"},
		{SourceID: 1, TargetName: "p.B", Kind: model.EdgeIncludes, FileID: 1, Confidence: 1.0},
	}
	err := h.resolveAndWriteEdges()
	if err == nil {
		t.Fatal("adjacency load failure must fail the resolve pass")
	}
	if got := err.Error(); !strings.Contains(got, "load includes adjacency") {
		t.Fatalf("error must name the failing leg, got %q", got)
	}
}

// cancellingStore cancels the pass's context from inside the adjacency load,
// so the next prepared-statement edge write fails: the only seam that can
// reach ExecEdgeStmt's error leg.
type cancellingStore struct {
	indexStore
	cancel context.CancelFunc
}

func (c *cancellingStore) EdgesOfKind(_ context.Context, _ model.EdgeKind) ([]model.Edge, error) {
	c.cancel()
	return nil, nil
}

func TestResolvePassSurfacesStatementWriteFailure(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "go.mod", "module corp/app\n")
	writeSource(t, dir, "a.go", "package p\n\ntype B struct{}\n\ntype S struct {\n\tB\n}\n")
	adapter := openIndex(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fid, err := adapter.WriteFile(ctx, &model.File{Path: "a.go", Language: "go"})
	if err != nil {
		t.Fatal(err)
	}
	sid, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "B", Qualified: "p.B", Kind: model.KindClass, LineStart: 3, LineEnd: 3})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		ctx:            ctx,
		idx:            &cancellingStore{indexStore: adapter, cancel: cancel},
		out:            io.Discard,
		warn:           io.Discard,
		root:           dir,
		matcher:        ignore.New(ignore.DefaultPatterns()...),
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
		collector:      newWarningCollector(),
		progress:       &progress{},
		seenPaths:      map[string]bool{},
	}
	h.pendingEdges = []pendingEdge{
		{SourceID: sid, TargetName: "p.B", Kind: model.EdgeIncludes, FileID: fid, Confidence: 1.0,
			TargetImportPath: "corp/app", TargetInPackage: "B"},
	}
	if err := h.resolveAndWriteEdges(); err == nil {
		t.Fatal("a failing edge statement must fail the resolve pass")
	}
}

func TestDBGoEmbeddingsSkipsFileLevelEdges(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "a.go", "package p\n")
	adapter := openIndex(t, dir)
	ctx := context.Background()
	fid, err := adapter.WriteFile(ctx, &model.File{Path: "a.go", Language: "go"})
	if err != nil {
		t.Fatal(err)
	}
	sid, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "B", Qualified: "p.B", Kind: model.KindClass, LineStart: 1, LineEnd: 1})
	if err != nil {
		t.Fatal(err)
	}
	// A file-level (NULL-source) includes edge carries no embedder identity
	// and must contribute nothing to the walk map.
	if _, err := adapter.WriteEdge(ctx, &model.Edge{TargetID: sid, Kind: model.EdgeIncludes, FileID: fid, Confidence: 1.0}); err != nil {
		t.Fatal(err)
	}
	// A satisfaction edge is inherits-kind: it must NEVER enter the walk map
	// (a struct does not acquire methods from interfaces it satisfies; a
	// union with inherits would launder satisfaction into the verified band;
	// mutant M2's kill case, at the construction seam).
	sid2, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "S", Qualified: "p.S", Kind: model.KindClass, LineStart: 2, LineEnd: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteEdge(ctx, &model.Edge{SourceID: &sid2, TargetID: sid, Kind: model.EdgeInherits, FileID: fid, Confidence: 0.9}); err != nil {
		t.Fatal(err)
	}
	h := &harness{ctx: ctx, idx: adapter}
	m, err := h.dbGoEmbeddings(map[int64]string{sid: "p.B", sid2: "p.S"})
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 0 {
		t.Fatalf("non-includes or file-level edges entered the walk map: %v", m)
	}
}
