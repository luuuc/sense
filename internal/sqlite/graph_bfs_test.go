package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// ReadSymbolGraph on an absent root id fails at the root ReadSymbol before any
// BFS work.
func TestReadSymbolGraphMissingRoot(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	if _, err := a.ReadSymbolGraph(ctx, 9999, 3, model.DirectionBoth, 0); err == nil {
		t.Fatal("expected error for missing root symbol, got nil")
	}
}

// A context cancelled before the BFS hop loop runs surfaces as a cancellation
// error rather than a partial result. Depth ≥ 2 with a non-empty frontier is
// required to reach the per-hop ctx.Err() check.
func TestReadSymbolGraphCancelledContext(t *testing.T) {
	parent := context.Background()
	a, err := sqlite.Open(parent, filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Chain S2 -> S1 -> S0 so depth=3 has frontier to expand.
	ids := seedChain(parent, t, a, 3)

	ctx, cancel := context.WithCancel(parent)
	cancel()

	if _, err := a.ReadSymbolGraph(ctx, ids[0], 3, model.DirectionCallers, 0); err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
}

// A temporal (co-change) edge between symbols on the BFS path is not followed:
// expandableEdge filters it, so a caller reachable only via a temporal edge
// never enters a later hop.
func TestReadSymbolGraphSkipsTemporalDuringHop(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	mk := func(name string, line int) int64 {
		fid, _ := a.WriteFile(ctx, &model.File{
			Path: name + ".rb", Language: "ruby", Hash: name, IndexedAt: time.Now(),
		})
		id, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: name,
			Kind: model.KindMethod, LineStart: line, LineEnd: line + 4,
		})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	root := mk("Root", 1)
	caller := mk("Caller", 10)
	grand := mk("Grand", 20)

	// Caller -> Root (real call, enters hop-2 frontier).
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(caller), TargetID: root,
		Kind: model.EdgeCalls, FileID: 1, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}
	// Grand ~ Caller (temporal): must NOT be admitted into hop 3.
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(grand), TargetID: caller,
		Kind: model.EdgeTemporal, FileID: 1, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	gr, err := a.ReadSymbolGraph(ctx, root, 3, model.DirectionCallers, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, layer := range gr.Layers {
		for _, e := range layer.Inbound {
			if e.Target.ID == grand {
				t.Error("temporal edge should not be followed into a later hop")
			}
		}
	}
}
