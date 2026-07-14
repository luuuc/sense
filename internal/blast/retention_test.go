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

// retainedIDs collects holder IDs from the retained group.
func retainedIDs(res blast.Result) map[int64]bool {
	out := map[int64]bool{}
	for _, rh := range res.RetainedViaInterfaces {
		out[rh.Symbol.ID] = true
	}
	return out
}

func computeRetention(t *testing.T, fix *fixtureDB, subject int64) blast.Result {
	t.Helper()
	res, err := blast.Compute(context.Background(), fix.db, []int64{subject},
		blast.Options{MaxHops: 3, MinConfidence: 0.1})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return res
}

// TestRetentionSurfacesLaunderedHolder is the primary G-11 repro: the
// laundered holder surfaces in RetainedViaInterfaces with its via-interface,
// while TotalAffected stays what it was without the group (may-retain never
// inflates the headline).
func TestRetentionSurfacesLaunderedHolder(t *testing.T) {
	fix, subject, _, iface, holder := launderedFixture(t)

	res := computeRetention(t, fix, subject)

	if !retainedIDs(res)[holder] {
		t.Fatalf("holder must appear in RetainedViaInterfaces, got %+v", res.RetainedViaInterfaces)
	}
	if res.RetainedCount != 1 {
		t.Errorf("RetainedCount = %d, want 1", res.RetainedCount)
	}
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder && rh.Via.ID != iface {
			t.Errorf("holder Via = %d, want interface %d", rh.Via.ID, iface)
		}
	}
	// Carrier C is the only affected symbol; the holder must not count.
	if res.TotalAffected != 1 {
		t.Errorf("TotalAffected = %d, want 1 (retained holders never counted)", res.TotalAffected)
	}
}

// TestRetentionOneRoundBound kills the fixpoint mutant: a holder reachable
// only through a SECOND interface indirection (holder satisfies another
// interface some other struct composes) must NOT surface.
func TestRetentionOneRoundBound(t *testing.T) {
	fix, subject, _, _, holder := launderedFixture(t)
	iface2 := fix.addSymbolWith(t, "SecondIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "ResolveSecondThing", model.KindMethod, &iface2)
	holder2 := fix.addSymbol(t, "TwoHopHolder")
	fix.addEdge(t, holder, iface2, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder2, iface2, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	got := retainedIDs(res)
	if !got[holder] {
		t.Errorf("one-hop holder must stay present")
	}
	if got[holder2] {
		t.Errorf("two-interface-hop holder must NOT surface (one laundering round)")
	}
}

// TestRetentionKindRestriction: a class-kind inherits target never launders —
// only interface-kind targets open the reverse-composition hop.
func TestRetentionKindRestriction(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	carrier := fix.addSymbol(t, "CarrierC")
	base := fix.addSymbol(t, "BaseClass") // kind class, not interface
	k := fix.addSymbol(t, "BaseComposer")
	fix.addEdge(t, carrier, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrier, base, model.EdgeInherits, 1.0)
	fix.addEdge(t, k, base, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	if len(res.RetainedViaInterfaces) != 0 || res.RetainedCount != 0 {
		t.Errorf("class-kind inherits target must not launder, got %+v", res.RetainedViaInterfaces)
	}
}

// TestRetentionTerminatesOnCompositionCycle: mutually-composing structs must
// not hang the carrier fixpoint, and the laundered holder still surfaces.
func TestRetentionTerminatesOnCompositionCycle(t *testing.T) {
	fix, subject, carrier, _, holder := launderedFixture(t)
	fix.addEdge(t, subject, carrier, model.EdgeComposes, 0.9) // cycle: A composes C, C composes A

	res := computeRetention(t, fix, subject)

	got := retainedIDs(res)
	if !got[holder] {
		t.Errorf("holder must surface despite composition cycle")
	}
	if got[subject] {
		t.Errorf("subject must never be its own holder")
	}
}

// TestRetentionTransitiveCarrierLaunders: the laundering base is the FULL
// typed-field carrier closure, not just direct composers — a level-2 carrier's
// satisfied interface still reaches its holders (the DoltSession shape; the
// measured direct-only variant lost 7/18 gold files).
func TestRetentionTransitiveCarrierLaunders(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	c1 := fix.addSymbol(t, "DirectCarrier")
	c2 := fix.addSymbol(t, "DeepCarrier")
	iface := fix.addSymbolWith(t, "DeepIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitDeepThing", model.KindMethod, &iface)
	holder := fix.addSymbol(t, "DeepHolder")
	fix.addEdge(t, c1, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, c2, c1, model.EdgeComposes, 0.9)
	fix.addEdge(t, c2, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	if !retainedIDs(res)[holder] {
		t.Errorf("holder via a level-2 carrier must surface, got %+v", res.RetainedViaInterfaces)
	}
}

// TestRetentionEmbeddedShapes: an embedded interface field (includes edge)
// retains just as a named field does — for the holder AND for the carrier
// chain — while an interface embedding the via-interface (interface source)
// is never a holder.
func TestRetentionEmbeddedShapes(t *testing.T) {
	fix, subject, _, iface, _ := launderedFixture(t)
	embedHolder := fix.addSymbol(t, "EmbedHolder")
	fix.addEdge(t, embedHolder, iface, model.EdgeIncludes, 0.9)
	superIface := fix.addSymbolWith(t, "SuperIface", model.KindInterface, nil)
	fix.addEdge(t, superIface, iface, model.EdgeIncludes, 0.9) // interface embeds interface

	// embed-carrier: EmbedCarrier embeds the subject (struct-embeds-struct),
	// satisfies its own rare interface, whose holder must surface.
	embedCarrier := fix.addSymbol(t, "EmbedCarrier")
	iface2 := fix.addSymbolWith(t, "EmbedCarrierIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitEmbedThing", model.KindMethod, &iface2)
	holder2 := fix.addSymbol(t, "EmbedCarrierHolder")
	fix.addEdge(t, embedCarrier, subject, model.EdgeIncludes, 0.9)
	fix.addEdge(t, embedCarrier, iface2, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder2, iface2, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	got := retainedIDs(res)
	if !got[embedHolder] {
		t.Errorf("embedded-interface-field holder must surface")
	}
	if !got[holder2] {
		t.Errorf("holder via an embed-carrier must surface")
	}
	if got[superIface] {
		t.Errorf("an interface embedding the via-interface is not a holder")
	}
}

// TestRetentionExclusionOverlaps pins the one-node-one-group boundaries:
// the subject never holds itself, a holder that is also a carrier stays in
// affected_via_composition, a holder that is also a subclass stays in
// affected_subclasses, and a member of the subject never appears.
func TestRetentionExclusionOverlaps(t *testing.T) {
	fix, subject, _, iface, holder := launderedFixture(t)
	// (a) subject composes its own satisfied interface.
	fix.addEdge(t, subject, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, subject, iface, model.EdgeComposes, 0.9)
	// (b) holder that is also a direct carrier.
	carrierHolder := fix.addSymbol(t, "CarrierHolder")
	fix.addEdge(t, carrierHolder, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrierHolder, iface, model.EdgeComposes, 0.9)
	// (c) holder that is also a subclass of the subject.
	subclassHolder := fix.addSymbol(t, "SubclassHolder")
	fix.addEdge(t, subclassHolder, subject, model.EdgeInherits, 1.0)
	fix.addEdge(t, subclassHolder, iface, model.EdgeComposes, 0.9)
	// (d) a member of the subject with a stray composes edge to the interface.
	member := fix.addSymbolWith(t, "subjectMethod", model.KindMethod, &subject)
	fix.addEdge(t, member, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	got := retainedIDs(res)
	if !got[holder] {
		t.Fatalf("plain holder must stay present")
	}
	for id, label := range map[int64]string{
		subject:        "subject (self)",
		carrierHolder:  "carrier-holder (belongs to composition group)",
		subclassHolder: "subclass-holder (belongs to subclasses group)",
		member:         "subject member (childSet)",
	} {
		if got[id] {
			t.Errorf("%s must not appear in RetainedViaInterfaces", label)
		}
	}
	// The excluded overlaps stay in their own groups.
	if !symbolIDs(res.AffectedViaComposition)[carrierHolder] {
		t.Errorf("carrier-holder must stay in AffectedViaComposition")
	}
	if !symbolIDs(res.AffectedSubclasses)[subclassHolder] {
		t.Errorf("subclass-holder must stay in AffectedSubclasses")
	}
}

// TestRetentionSkipsNonTypeSubjects: only class/type/interface subjects can be
// retained through fields; a function subject pays nothing and gets nothing.
func TestRetentionSkipsNonTypeSubjects(t *testing.T) {
	fix := newFixtureDB(t)
	fn := fix.addSymbolWith(t, "DoWork", model.KindFunction, nil)
	iface := fix.addSymbolWith(t, "WorkIface", model.KindInterface, nil)
	holder := fix.addSymbol(t, "WorkHolder")
	fix.addEdge(t, fn, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, fn)

	if len(res.RetainedViaInterfaces) != 0 || res.RetainedCount != 0 {
		t.Errorf("function subject must produce no retained group, got %+v", res.RetainedViaInterfaces)
	}
}
