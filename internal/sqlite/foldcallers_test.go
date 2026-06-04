package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func newFoldAdapter(t *testing.T) (*sqlite.Adapter, int64) {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "fold.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	fid, err := a.WriteFile(ctx, &model.File{
		Path: "fold.rb", Language: "ruby", Hash: "f", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return a, fid
}

func mustSym(t *testing.T, a *sqlite.Adapter, s *model.Symbol) int64 {
	t.Helper()
	id, err := a.WriteSymbol(context.Background(), s)
	if err != nil {
		t.Fatalf("WriteSymbol %s: %v", s.Name, err)
	}
	return id
}

func mustEdge(t *testing.T, a *sqlite.Adapter, e *model.Edge) {
	t.Helper()
	if _, err := a.WriteEdge(context.Background(), e); err != nil {
		t.Fatalf("WriteEdge: %v", err)
	}
}

// A class with no direct callers surfaces the callers of its methods, so it
// no longer looks unused — while its own members are excluded as callers.
func TestReadSymbolGraphFoldsMemberCallers(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Order", Qualified: "Order", Kind: model.KindClass, LineStart: 1, LineEnd: 50})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "process", Qualified: "Order#process", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 10})
	siblingID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "validate", Qualified: "Order#validate", Kind: model.KindMethod, ParentID: &classID, LineStart: 12, LineEnd: 16})
	callerID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Run", Qualified: "Client.Run", Kind: model.KindFunction, LineStart: 20, LineEnd: 30})

	mustEdge(t, a, &model.Edge{SourceID: &callerID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	mustEdge(t, a, &model.Edge{SourceID: &siblingID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var gotCaller, gotSelf bool
	for _, e := range gr.Root.Inbound {
		switch e.Target.ID {
		case callerID:
			gotCaller = true
		case classID, methodID, siblingID:
			gotSelf = true
		}
	}
	if !gotCaller {
		t.Error("class graph should fold in the caller of its method")
	}
	if gotSelf {
		t.Error("class graph must exclude the class's own members as callers")
	}
}

// When a class already has direct callers, the precise answer is kept — method
// callers are not folded in to dilute it.
func TestReadSymbolGraphDirectCallersNotDiluted(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Acct", Qualified: "Acct", Kind: model.KindClass, LineStart: 1, LineEnd: 30})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "debit", Qualified: "Acct#debit", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})
	directID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Direct", Qualified: "Direct", Kind: model.KindFunction, LineStart: 10, LineEnd: 14})
	indirectID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Indirect", Qualified: "Indirect", Kind: model.KindFunction, LineStart: 16, LineEnd: 20})

	mustEdge(t, a, &model.Edge{SourceID: &directID, TargetID: classID, Kind: model.EdgeReferences, FileID: fileID, Confidence: 1.0})
	mustEdge(t, a, &model.Edge{SourceID: &indirectID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var direct, indirect bool
	for _, e := range gr.Root.Inbound {
		if e.Target.ID == directID {
			direct = true
		}
		if e.Target.ID == indirectID {
			indirect = true
		}
	}
	if !direct {
		t.Error("direct caller missing")
	}
	if indirect {
		t.Error("method-caller should not be folded when direct callers already exist")
	}
}

// foldMemberCallers skips edges that blast would filter: temporal (co-change)
// noise and below-floor low-confidence guesses are not folded into the class's
// callers, while a genuine high-confidence caller of a method is. A second
// high-confidence caller of the same external symbol is deduped to one edge.
func TestReadSymbolGraphFoldCallersFilters(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Ledger", Qualified: "Ledger", Kind: model.KindClass, LineStart: 1, LineEnd: 80})
	methodA := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "post", Qualified: "Ledger#post", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 10})
	methodB := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "void", Qualified: "Ledger#void", Kind: model.KindMethod, ParentID: &classID, LineStart: 12, LineEnd: 16})

	realCaller := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Boot", Qualified: "Boot", Kind: model.KindFunction, LineStart: 20, LineEnd: 30})
	dupCaller := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Boot2", Qualified: "Boot2", Kind: model.KindFunction, LineStart: 32, LineEnd: 40})
	lowConfCaller := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Guess", Qualified: "Guess", Kind: model.KindFunction, LineStart: 42, LineEnd: 50})
	temporalCaller := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Chg", Qualified: "Chg", Kind: model.KindFunction, LineStart: 52, LineEnd: 60})

	// Two high-confidence callers of methodA: realCaller and dupCaller.
	mustEdge(t, a, &model.Edge{SourceID: &realCaller, TargetID: methodA, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	mustEdge(t, a, &model.Edge{SourceID: &dupCaller, TargetID: methodA, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	// dupCaller also calls methodB — folds to the same target id once.
	mustEdge(t, a, &model.Edge{SourceID: &dupCaller, TargetID: methodB, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	// A below-floor guess: must not be folded.
	mustEdge(t, a, &model.Edge{SourceID: &lowConfCaller, TargetID: methodA, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.3})
	// A temporal (co-change) edge: must not be folded.
	mustEdge(t, a, &model.Edge{SourceID: &temporalCaller, TargetID: methodA, Kind: model.EdgeTemporal, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}

	seen := map[int64]int{}
	for _, e := range gr.Root.Inbound {
		seen[e.Target.ID]++
	}
	if seen[realCaller] == 0 {
		t.Error("high-confidence method caller should be folded in")
	}
	if seen[dupCaller] != 1 {
		t.Errorf("duplicate caller across two methods should appear once, got %d", seen[dupCaller])
	}
	if seen[lowConfCaller] != 0 {
		t.Error("below-floor caller must not be folded")
	}
	if seen[temporalCaller] != 0 {
		t.Error("temporal caller must not be folded")
	}
}

// A container class with no child symbols at all takes the empty-children
// early return in foldMemberCallers without erroring.
func TestReadSymbolGraphFoldCallersNoChildren(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Lonely", Qualified: "Lonely", Kind: model.KindClass, LineStart: 1, LineEnd: 5})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	if len(gr.Root.Inbound) != 0 {
		t.Errorf("childless class inbound = %d, want 0", len(gr.Root.Inbound))
	}
}

// A non-container root (a method) never folds — its graph is exactly its own edges.
func TestReadSymbolGraphMethodRootNotFolded(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Svc", Qualified: "Svc", Kind: model.KindClass, LineStart: 1, LineEnd: 20})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "run", Qualified: "Svc#run", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})

	gr, err := a.ReadSymbolGraph(ctx, methodID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	if len(gr.Root.Inbound) != 0 {
		t.Errorf("method root inbound = %d, want 0 (no folding for non-containers)", len(gr.Root.Inbound))
	}
}
