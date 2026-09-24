package blast_test

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// addGuessChild writes a method symbol owned by parent, so seedFrontier expands
// it as a member of the class and the class blast inherits its callers.
func addGuessChild(t *testing.T, a *sqlite.Adapter, fileID int64, qualified string, parent int64) int64 {
	t.Helper()
	id, err := a.WriteSymbol(context.Background(), &model.Symbol{
		FileID:    fileID,
		Name:      qualified,
		Qualified: qualified,
		Kind:      model.KindMethod,
		ParentID:  &parent,
		LineStart: 40,
		LineEnd:   41,
	})
	if err != nil {
		t.Fatalf("WriteSymbol %q: %v", qualified, err)
	}
	return id
}

// The discourse shape, at the MCP server's own settings (min_confidence 0.3,
// max_hops 5). Admin::BadgesController's entire claim on the word `new` is a
// two-line empty Rails action, and every `SomeOtherClass.new` the resolver
// could not type binds that method by trailing name at
// extract.ConfidenceNameCollision. Rolled up to the class, those guesses served
// 59 of 60 direct callers, risk high and total_affected 357 — against one real
// caller, the route.
//
// The stamp is the resolver saying "this is a guess, ignore it for impact".
// MinConfidence is the DEPTH control and must not be able to re-admit it.
func TestComputeIgnoresBareNameGuessCallers(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	ctrl := addBlastSymbol(t, adapter, fid, "Admin::BadgesController", model.KindClass)
	action := addGuessChild(t, adapter, fid, "Admin::BadgesController#new", ctrl)
	route := addBlastSymbol(t, adapter, fid, "route:admin_badges_path", model.KindFunction)
	groupAdd := addBlastSymbol(t, adapter, fid, "Group#add", model.KindMethod)
	settingParse := addBlastSymbol(t, adapter, fid, "GlobalSetting::FileProvider.parse", model.KindMethod)
	guardian := addBlastSymbol(t, adapter, fid, "ApplicationController#guardian", model.KindMethod)

	// The one production reference to the controller in the tree.
	addBlastEdge(t, adapter, fid, route, ctrl, model.EdgeCalls, 0.9)
	// `GroupManager.new`, `new(file)`, `Guardian.new(...)` — none of them writes
	// BadgesController, all of them bound `new` by trailing name.
	addBlastEdge(t, adapter, fid, groupAdd, action, model.EdgeCalls, extract.ConfidenceNameCollision)
	addBlastEdge(t, adapter, fid, settingParse, action, model.EdgeCalls, extract.ConfidenceNameCollision)
	addBlastEdge(t, adapter, fid, guardian, action, model.EdgeReferences, extract.ConfidenceNameCollision)

	res, err := blast.Compute(ctx, db, []int64{ctrl}, blast.Options{MaxHops: 5, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	got := affectedIDs(res)
	if !got[route] {
		t.Errorf("the route edge is the real caller and must be served; got %v", got)
	}
	for name, id := range map[string]int64{
		"Group#add":                         groupAdd,
		"GlobalSetting::FileProvider.parse": settingParse,
		"ApplicationController#guardian":    guardian,
	} {
		if got[id] {
			t.Errorf("%s reached the class only through a bare-name guess on `new` and must NOT be in the radius; got %v", name, got)
		}
	}
	if len(res.DirectCallers) != 1 {
		t.Errorf("DirectCallers = %d, want 1 (the route)", len(res.DirectCallers))
	}
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1", res.TotalAffected)
	}
}

// Scope guard, half one: the stratum ABOVE the stamp is the one the tool
// description promises ("raising min_confidence above 0.5 silently drops
// unverified-receiver call edges — most production dependents"). An
// unresolved-receiver call that bound a UNIQUE name keeps
// extract.ConfidenceUnresolved and must still be served. Kills the
// "raise the floor to 0.5" mutant.
func TestComputeKeepsUnresolvedReceiverCallers(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	subject := addBlastSymbol(t, adapter, fid, "Uniq#checkout_total", model.KindMethod)
	caller := addBlastSymbol(t, adapter, fid, "Cart#summary", model.KindMethod)
	addBlastEdge(t, adapter, fid, caller, subject, model.EdgeCalls, extract.ConfidenceUnresolved)

	res, err := blast.Compute(ctx, db, []int64{subject}, blast.Options{MaxHops: 5, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !affectedIDs(res)[caller] {
		t.Errorf("an unresolved-receiver caller at %v must stay in the radius", extract.ConfidenceUnresolved)
	}
}

// Scope guard, half two: the floor is on USAGE edges only. A temporal edge's
// confidence is a co-change ratio, not a resolution — 0.3 there means "these
// two files change together weakly", which is a measurement, not a guess.
// Kills the "gate every edge kind at 0.3" mutant.
func TestComputeKeepsWeakTemporalEdge(t *testing.T) {
	db, adapter := setupGraph(t)
	ctx := context.Background()
	fid := fileIDOf(t, adapter, "a.rb")

	subject := addBlastSymbol(t, adapter, fid, "Cochange#change", model.KindMethod)
	partner := addBlastSymbol(t, adapter, fid, "CochangePartner#other", model.KindMethod)
	addBlastEdge(t, adapter, fid, partner, subject, model.EdgeTemporal, 0.3)

	res, err := blast.Compute(ctx, db, []int64{subject}, blast.Options{MaxHops: 5, MinConfidence: 0.3})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !affectedIDs(res)[partner] {
		t.Errorf("a 0.3 temporal co-change partner must stay in the radius at min_confidence 0.3")
	}
}
