package sqlite_test

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// A class with no callees of its own surfaces the callees of its methods, so
// "what does this class call" no longer reads as an empty "depends on nothing"
// — while its own members (self-calls, sibling calls) are excluded.
func TestReadSymbolGraphFoldsMemberCallees(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Price", Qualified: "Price", Kind: model.KindClass, LineStart: 1, LineEnd: 50})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "formatted", Qualified: "Price#formatted", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 10})
	siblingID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "amount", Qualified: "Price#amount", Kind: model.KindMethod, ParentID: &classID, LineStart: 12, LineEnd: 16})
	depID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "format", Qualified: "I18n.format", Kind: model.KindFunction, LineStart: 20, LineEnd: 30})

	// formatted -> I18n.format (external dependency, should fold up)
	mustEdge(t, a, &model.Edge{SourceID: &methodID, TargetID: depID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	// formatted -> amount (internal sibling call, must be excluded)
	mustEdge(t, a, &model.Edge{SourceID: &methodID, TargetID: siblingID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var gotDep, gotSelf bool
	for _, e := range gr.Root.Outbound {
		switch e.Target.ID {
		case depID:
			gotDep = true
		case classID, methodID, siblingID:
			gotSelf = true
		}
	}
	if !gotDep {
		t.Error("class graph should fold in the callee of its method")
	}
	if gotSelf {
		t.Error("class graph must exclude the class's own members as callees")
	}
}

// When a class already names a callee directly, the precise answer is kept —
// method callees are not folded in to dilute it.
func TestReadSymbolGraphDirectCalleesNotDiluted(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Acct", Qualified: "Acct", Kind: model.KindClass, LineStart: 1, LineEnd: 30})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "debit", Qualified: "Acct#debit", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})
	directID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Direct", Qualified: "Direct", Kind: model.KindFunction, LineStart: 10, LineEnd: 14})
	indirectID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Indirect", Qualified: "Indirect", Kind: model.KindFunction, LineStart: 16, LineEnd: 20})

	// Class itself references Direct (a real callee of its own).
	mustEdge(t, a, &model.Edge{SourceID: &classID, TargetID: directID, Kind: model.EdgeReferences, FileID: fileID, Confidence: 1.0})
	// Method debit calls Indirect — should NOT fold up since the class has a callee.
	mustEdge(t, a, &model.Edge{SourceID: &methodID, TargetID: indirectID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var direct, indirect bool
	for _, e := range gr.Root.Outbound {
		if e.Target.ID == directID {
			direct = true
		}
		if e.Target.ID == indirectID {
			indirect = true
		}
	}
	if !direct {
		t.Error("direct callee missing")
	}
	if indirect {
		t.Error("method-callee should not be folded when the class already names a callee")
	}
}

// A non-container root (a method) never folds — its graph is exactly its own edges.
func TestReadSymbolGraphMethodRootNotFoldedCallees(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Svc", Qualified: "Svc", Kind: model.KindClass, LineStart: 1, LineEnd: 20})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "run", Qualified: "Svc#run", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})

	gr, err := a.ReadSymbolGraph(ctx, methodID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	if len(gr.Root.Outbound) != 0 {
		t.Errorf("method root outbound = %d, want 0 (no folding for non-containers)", len(gr.Root.Outbound))
	}
}

// A low-confidence member callee (below the 0.5 floor — e.g. a 0.3 ERB/i18n
// guess) is not folded into the class's callees.
func TestReadSymbolGraphMemberCalleesRespectConfidenceFloor(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Price", Qualified: "Price", Kind: model.KindClass, LineStart: 1, LineEnd: 50})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "formatted", Qualified: "Price#formatted", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 10})
	noiseID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "i18n_key", Qualified: "i18n:some.key", Kind: model.KindFunction, LineStart: 20, LineEnd: 21})

	// 0.3 == ConfidenceNameCollision, below the 0.5 fold floor.
	mustEdge(t, a, &model.Edge{SourceID: &methodID, TargetID: noiseID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.3})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	for _, e := range gr.Root.Outbound {
		if e.Target.ID == noiseID {
			t.Error("low-confidence member callee should not be folded into the class")
		}
	}
}

// A container with no members folds nothing — outbound stays empty.
func TestReadSymbolGraphFoldsMemberCalleesNoChildren(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Empty", Qualified: "Empty", Kind: model.KindClass, LineStart: 1, LineEnd: 2})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	if len(gr.Root.Outbound) != 0 {
		t.Errorf("childless class outbound = %d, want 0", len(gr.Root.Outbound))
	}
}

// The fold dedupes a callee reached from several methods, skips temporal
// (co-change) member edges, and preserves the container's own structural
// outbound edge (which doesn't suppress the fold since it isn't a usage edge).
func TestReadSymbolGraphMemberCalleesDedupSkipsTemporalKeepsStructural(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Wallet", Qualified: "Wallet", Kind: model.KindClass, LineStart: 1, LineEnd: 60})
	baseID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Base", Qualified: "Base", Kind: model.KindClass, LineStart: 100, LineEnd: 110})
	m1 := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "credit", Qualified: "Wallet#credit", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 9})
	m2 := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "debit", Qualified: "Wallet#debit", Kind: model.KindMethod, ParentID: &classID, LineStart: 11, LineEnd: 15})
	depID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "new", Qualified: "Money.new", Kind: model.KindFunction, LineStart: 200, LineEnd: 210})
	tmpID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "CoChange", Qualified: "CoChange", Kind: model.KindFunction, LineStart: 300, LineEnd: 310})

	// Class itself inherits Base — a structural (non-usage) edge that should
	// survive and must not suppress the fold.
	mustEdge(t, a, &model.Edge{SourceID: &classID, TargetID: baseID, Kind: model.EdgeInherits, FileID: fileID, Confidence: 1.0})
	// Both methods reach Money.new — should fold up exactly once.
	mustEdge(t, a, &model.Edge{SourceID: &m1, TargetID: depID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	mustEdge(t, a, &model.Edge{SourceID: &m2, TargetID: depID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	// A co-change edge on a member must be skipped, not folded as a callee.
	mustEdge(t, a, &model.Edge{SourceID: &m1, TargetID: tmpID, Kind: model.EdgeTemporal, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallees, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var depCount, baseCount int
	var sawTemporal bool
	for _, e := range gr.Root.Outbound {
		switch e.Target.ID {
		case depID:
			depCount++
		case baseID:
			baseCount++
		case tmpID:
			sawTemporal = true
		}
	}
	if depCount != 1 {
		t.Errorf("Money.new folded %d times, want 1 (deduped across methods)", depCount)
	}
	if baseCount != 1 {
		t.Errorf("structural Base edge count = %d, want 1 (preserved)", baseCount)
	}
	if sawTemporal {
		t.Error("temporal member edge should not be folded as a callee")
	}
}
