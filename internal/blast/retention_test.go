package blast_test

// Retention-closure fixtures (pitch 31-12 / ledger G-11): a struct that holds
// the subject only behind an interface-typed field (an "interface-laundered
// holder") is reachable from the subject by one forward satisfaction hop plus
// one reverse composition hop. The BFS walks reverse edges only, so such
// holders are structurally invisible to every existing blast group — the
// characterization test below pins that fact, and stays true after the fix:
// laundered holders surface ONLY in RetainedViaInterfaces, never in the
// caller lists or the edge-kind groups.

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
)

const confConvention = 0.9 // extract.ConfidenceConvention (satisfaction edges)

// launderedFixture builds the minimal laundering shape:
//
//	subject A ←composes— carrier C —inherits→ interface I ←composes— holder H
//
// C carries A through a named field; C satisfies I; H holds an I-typed field.
// I's single member is rare, so the junk screen keeps it.
func launderedFixture(t *testing.T) (fix *fixtureDB, subject, carrier, iface, holder int64) {
	t.Helper()
	fix = newFixtureDB(t)
	subject = fix.addSymbol(t, "SubjectA")
	carrier = fix.addSymbol(t, "CarrierC")
	iface = fix.addSymbolWith(t, "RareIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitRareThing", model.KindMethod, &iface)
	holder = fix.addSymbol(t, "HolderH")

	fix.addEdge(t, carrier, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrier, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)
	return fix, subject, carrier, iface, holder
}

// symbolIDs collects the IDs of a symbol slice.
func symbolIDs(syms []model.Symbol) map[int64]bool {
	out := map[int64]bool{}
	for _, s := range syms {
		out[s.ID] = true
	}
	return out
}

// TestLaunderedHolderInvisibleToCallerGroups pins the G-11 defect shape: the
// interface-laundered holder appears in NO caller list and NO edge-kind group,
// at any hop count. This characterization holds before AND after the fix —
// laundered holders belong to RetainedViaInterfaces exclusively.
func TestLaunderedHolderInvisibleToCallerGroups(t *testing.T) {
	fix, subject, _, _, holder := launderedFixture(t)

	res, err := blast.Compute(context.Background(), fix.db, []int64{subject},
		blast.Options{MaxHops: 5, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if symbolIDs(res.DirectCallers)[holder] {
		t.Errorf("holder %d must not appear in DirectCallers", holder)
	}
	for _, hop := range res.IndirectCallers {
		if hop.Symbol.ID == holder {
			t.Errorf("holder %d must not appear in IndirectCallers", holder)
		}
	}
	for _, group := range [][]model.Symbol{res.AffectedSubclasses, res.AffectedViaComposition, res.AffectedViaIncludes} {
		if symbolIDs(group)[holder] {
			t.Errorf("holder %d must not appear in edge-kind groups", holder)
		}
	}
}
