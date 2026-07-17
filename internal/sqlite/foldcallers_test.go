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

// When a class already has enough direct callers of its own (the sufficient-
// evidence floor), the precise answer is kept; method callers are not folded
// in to dilute it.
func TestReadSymbolGraphDirectCallersNotDiluted(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Acct", Qualified: "Acct", Kind: model.KindClass, LineStart: 1, LineEnd: 30})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "debit", Qualified: "Acct#debit", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})
	indirectID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Indirect", Qualified: "Indirect", Kind: model.KindFunction, LineStart: 16, LineEnd: 20})

	var directIDs []int64
	for i, name := range []string{"DirectA", "DirectB", "DirectC"} {
		id := mustSym(t, a, &model.Symbol{FileID: fileID, Name: name, Qualified: name, Kind: model.KindFunction, LineStart: 10 + 10*i, LineEnd: 14 + 10*i})
		mustEdge(t, a, &model.Edge{SourceID: &id, TargetID: classID, Kind: model.EdgeReferences, FileID: fileID, Confidence: 1.0})
		directIDs = append(directIDs, id)
	}
	mustEdge(t, a, &model.Edge{SourceID: &indirectID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var direct, indirect bool
	for _, e := range gr.Root.Inbound {
		if e.Target.ID == directIDs[0] {
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
		t.Error("method-caller should not be folded when the class has enough direct callers")
	}
}

// A container with only ONE direct caller still folds its member callers in:
// a single class-level edge is not sufficient evidence that the caller set is
// complete. Regression: laravelio's Thread had 1 class-level call edge
// (CreateThread) and 66 method-level caller edges; graph answered
// called_by=1 with completeness "complete" while blast's verified band held
// 12 dependents: the PHP shape, where good method resolution starves the
// class node.
func TestReadSymbolGraphFewDirectCallersStillFold(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Thread", Qualified: "App\\Models\\Thread", Kind: model.KindClass, LineStart: 1, LineEnd: 90})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "feed", Qualified: "App\\Models\\Thread\\feed", Kind: model.KindMethod, ParentID: &classID, LineStart: 5, LineEnd: 12})
	directID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "handle", Qualified: "App\\Jobs\\CreateThread\\handle", Kind: model.KindFunction, LineStart: 20, LineEnd: 30})
	methodCallerID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "index", Qualified: "ForumController\\index", Kind: model.KindFunction, LineStart: 32, LineEnd: 40})

	mustEdge(t, a, &model.Edge{SourceID: &directID, TargetID: classID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})
	mustEdge(t, a, &model.Edge{SourceID: &methodCallerID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var direct, folded bool
	for _, e := range gr.Root.Inbound {
		if e.Target.ID == directID {
			direct = true
		}
		if e.Target.ID == methodCallerID {
			folded = true
		}
	}
	if !direct {
		t.Error("the class-level caller must be listed")
	}
	if !folded {
		t.Error("one class-level caller must not suppress the member-caller fold")
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

// A synthetic accessor caller (django-related:*, route:*, …) is declaration-site
// plumbing, not a real direct caller — it must not suppress the member-caller
// fold. Regression: saleor's ProductVariant gained ONE django-related:variants
// edge (0.8) and graph lost all 125 method-derived callers.
func TestReadSymbolGraphSyntheticCallerDoesNotSuppressFold(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Variant", Qualified: "Variant", Kind: model.KindClass, LineStart: 1, LineEnd: 40})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "sku", Qualified: "Variant#sku", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})
	accessorID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "variants", Qualified: "django-related:variants", Kind: model.KindConstant, LineStart: 8, LineEnd: 8})
	realCallerID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Payload", Qualified: "Payload", Kind: model.KindFunction, LineStart: 10, LineEnd: 20})

	// The synthetic accessor "calls" the class at convention confidence…
	mustEdge(t, a, &model.Edge{SourceID: &accessorID, TargetID: classID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.8})
	// …while the real consumer reaches it through a method.
	mustEdge(t, a, &model.Edge{SourceID: &realCallerID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.7})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var synthetic, folded bool
	for _, e := range gr.Root.Inbound {
		if e.Target.ID == accessorID {
			synthetic = true
		}
		if e.Target.ID == realCallerID {
			folded = true
		}
	}
	if !synthetic {
		t.Error("synthetic accessor edge should still be listed among callers")
	}
	if !folded {
		t.Error("method-caller fold must fire despite the synthetic caller: plumbing is not a real direct caller")
	}
}

// Real (non-synthetic) direct callers at the sufficient-evidence floor still
// suppress the fold when they arrive alongside a synthetic one; the synthetic
// edge neither suppresses nor un-suppresses; only genuine usage counts toward
// the floor.
func TestReadSymbolGraphRealCallerStillSuppressesFoldBesideSynthetic(t *testing.T) {
	a, fileID := newFoldAdapter(t)
	ctx := context.Background()

	classID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Device", Qualified: "Device", Kind: model.KindClass, LineStart: 1, LineEnd: 40})
	methodID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "save", Qualified: "Device#save", Kind: model.KindMethod, ParentID: &classID, LineStart: 3, LineEnd: 6})
	accessorID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "devices", Qualified: "django-related:devices", Kind: model.KindConstant, LineStart: 8, LineEnd: 8})
	directID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Filter", Qualified: "Filter", Kind: model.KindFunction, LineStart: 10, LineEnd: 20})
	methodCallerID := mustSym(t, a, &model.Symbol{FileID: fileID, Name: "Task", Qualified: "Task", Kind: model.KindFunction, LineStart: 22, LineEnd: 30})

	mustEdge(t, a, &model.Edge{SourceID: &accessorID, TargetID: classID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.8})
	mustEdge(t, a, &model.Edge{SourceID: &directID, TargetID: classID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.9})
	for i, name := range []string{"FilterB", "FilterC"} {
		id := mustSym(t, a, &model.Symbol{FileID: fileID, Name: name, Qualified: name, Kind: model.KindFunction, LineStart: 42 + 10*i, LineEnd: 48 + 10*i})
		mustEdge(t, a, &model.Edge{SourceID: &id, TargetID: classID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 0.9})
	}
	mustEdge(t, a, &model.Edge{SourceID: &methodCallerID, TargetID: methodID, Kind: model.EdgeCalls, FileID: fileID, Confidence: 1.0})

	gr, err := a.ReadSymbolGraph(ctx, classID, 1, model.DirectionCallers, 0)
	if err != nil {
		t.Fatalf("ReadSymbolGraph: %v", err)
	}
	var direct, folded bool
	for _, e := range gr.Root.Inbound {
		if e.Target.ID == directID {
			direct = true
		}
		if e.Target.ID == methodCallerID {
			folded = true
		}
	}
	if !direct {
		t.Error("the genuine direct caller must be listed")
	}
	if folded {
		t.Error("a genuine direct caller must still suppress the method-caller fold")
	}
}
