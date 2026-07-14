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
	"fmt"
	"testing"
	"time"

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

// addCommonMethods seeds n method symbols sharing the bare name but with
// distinct qualified names (T1.Next, T2.Next, …) — the real-repo shape the
// junk screen's frequency count reads. Distinct qualified names matter: the
// adapter upserts same-file same-qualified symbols into one row.
func addCommonMethods(t *testing.T, fix *fixtureDB, name string, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		line := fix.nextLine
		fix.nextLine += 10
		if _, err := fix.adapter.WriteSymbol(context.Background(), &model.Symbol{
			FileID: fix.fileID, Name: name,
			Qualified: fmt.Sprintf("FreqType%d.%s", i, name),
			Kind:      model.KindMethod, LineStart: line, LineEnd: line + 5,
		}); err != nil {
			t.Fatalf("WriteSymbol %s#%d: %v", name, i, err)
		}
	}
}

// launderedVia wires carrier→iface (satisfaction) and holder→iface
// (composition) so iface becomes a laundering route for the fixture.
func launderedVia(t *testing.T, fix *fixtureDB, carrier int64, memberNames ...string) (iface, holder int64) {
	t.Helper()
	iface = fix.addSymbolWith(t, "Iface"+memberNames[0], model.KindInterface, nil)
	for _, m := range memberNames {
		fix.addSymbolWith(t, m, model.KindMethod, &iface)
	}
	holder = fix.addSymbol(t, "HolderVia"+memberNames[0])
	fix.addEdge(t, carrier, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)
	return iface, holder
}

// TestRetentionJunkScreen pins the F-31-09b interim screen: a single-member
// via-interface whose member name is common (index-wide method-name frequency
// strictly above the threshold) is junk — its holders never surface. The
// boundary is exact: frequency 25 keeps, 26 screens. Zero-member (embedded-
// only) interfaces and multi-member interfaces with a common member always
// pass — the method-SET match is their signal.
func TestRetentionJunkScreen(t *testing.T) {
	fix, subject, carrier, _, holder := launderedFixture(t)

	// Junk twin: sole member "Next", 60 same-named methods index-wide.
	_, junkHolder := launderedVia(t, fix, carrier, "Next")
	addCommonMethods(t, fix, "Next", 59) // + the member itself = 60 > threshold

	// Boundary pair: exactly 25 total stays, 26 total is screened.
	_, keptBoundary := launderedVia(t, fix, carrier, "AtBoundary")
	addCommonMethods(t, fix, "AtBoundary", 24) // 24 + member = 25, kept
	_, screenedBoundary := launderedVia(t, fix, carrier, "PastBoundary")
	addCommonMethods(t, fix, "PastBoundary", 25) // 25 + member = 26, screened

	// Zero-direct-member interface (embedded-only shape) is never screened.
	emptyIface := fix.addSymbolWith(t, "EmbeddedOnlyIface", model.KindInterface, nil)
	emptyHolder := fix.addSymbol(t, "HolderViaEmpty")
	fix.addEdge(t, carrier, emptyIface, model.EdgeInherits, confConvention)
	fix.addEdge(t, emptyHolder, emptyIface, model.EdgeComposes, 0.9)

	// Multi-member interface with one common name (the CommitItr shape) passes.
	_, multiHolder := launderedVia(t, fix, carrier, "Next", "ResetToRareState")

	res := computeRetention(t, fix, subject)

	got := retainedIDs(res)
	for id, want := range map[int64]bool{
		holder:           true,
		junkHolder:       false,
		keptBoundary:     true,
		screenedBoundary: false,
		emptyHolder:      true,
		multiHolder:      true,
	} {
		if got[id] != want {
			t.Errorf("holder %d: present=%v, want %v", id, got[id], want)
		}
	}
}

// TestRetentionResultCapSetsTruncated: more holders than MaxResults trims the
// list, flags Truncated, and leaves RetainedCount at the full size.
func TestRetentionResultCapSetsTruncated(t *testing.T) {
	fix, subject, carrier, _, _ := launderedFixture(t)
	iface2, _ := launderedVia(t, fix, carrier, "VisitCapThing")
	extra := fix.addSymbol(t, "CapHolderB")
	fix.addEdge(t, extra, iface2, model.EdgeComposes, 0.9)

	res, err := blast.Compute(context.Background(), fix.db, []int64{subject},
		blast.Options{MaxHops: 3, MinConfidence: 0.1, MaxResults: 2})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}

	if len(res.RetainedViaInterfaces) != 2 {
		t.Errorf("holders enumerated = %d, want 2 (capped)", len(res.RetainedViaInterfaces))
	}
	if res.RetainedCount != 3 {
		t.Errorf("RetainedCount = %d, want 3 (full size survives the cap)", res.RetainedCount)
	}
	if !res.Truncated {
		t.Errorf("Truncated must be set when the retained cap bites")
	}
}

// TestRetentionLevelCapSetsTruncated: a composition chain deeper than the
// fixpoint level cap flags Truncated instead of walking forever.
func TestRetentionLevelCapSetsTruncated(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "ChainSubject")
	prev := subject
	for i := 0; i < 25; i++ { // depth > retentionMaxLevels (20)
		next := fix.addSymbol(t, fmt.Sprintf("ChainCarrier%02d", i))
		fix.addEdge(t, next, prev, model.EdgeComposes, 0.9)
		prev = next
	}

	res := computeRetention(t, fix, subject)

	if !res.Truncated {
		t.Errorf("Truncated must be set when the level cap cuts a live frontier")
	}
}

// TestRetentionDeterministicOrder: production holders come first in ID order;
// a test-path holder sorts last even with the lowest ID.
func TestRetentionDeterministicOrder(t *testing.T) {
	fix, subject, _, iface, holder := launderedFixture(t)

	testFileID, err := fix.adapter.WriteFile(context.Background(), &model.File{
		Path: "spec/fixture_spec.rb", Language: "ruby", Hash: "spec",
		Symbols: 1, IndexedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	testHolder, err := fix.adapter.WriteSymbol(context.Background(), &model.Symbol{
		FileID: testFileID, Name: "SpecHolder", Qualified: "SpecHolder",
		Kind: model.KindClass, LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}
	fix.addEdge(t, testHolder, iface, model.EdgeComposes, 0.9)
	prodHolder := fix.addSymbol(t, "SecondProdHolder")
	fix.addEdge(t, prodHolder, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	want := []int64{holder, prodHolder, testHolder}
	if len(res.RetainedViaInterfaces) != len(want) {
		t.Fatalf("holders = %d, want %d", len(res.RetainedViaInterfaces), len(want))
	}
	for i, id := range want {
		if res.RetainedViaInterfaces[i].Symbol.ID != id {
			t.Errorf("holder[%d] = %d, want %d (production-first, then ID)",
				i, res.RetainedViaInterfaces[i].Symbol.ID, id)
		}
	}
}

// TestRetentionDualInterfaceAttribution: a holder reaching the subject through
// two via-interfaces appears once, attributed to the lowest interface ID.
func TestRetentionDualInterfaceAttribution(t *testing.T) {
	fix, subject, carrier, iface, holder := launderedFixture(t)
	iface2 := fix.addSymbolWith(t, "SecondRareIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitOtherRareThing", model.KindMethod, &iface2)
	fix.addEdge(t, carrier, iface2, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface2, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	seen := 0
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder {
			seen++
			if rh.Via.ID != iface {
				t.Errorf("Via = %d, want lowest interface ID %d", rh.Via.ID, iface)
			}
		}
	}
	if seen != 1 {
		t.Errorf("holder appears %d times, want exactly once", seen)
	}
}

// TestRetentionCarrierSetCapTrips: a direct-composer fan wider than the
// carrier-set cap truncates the closure (order-defined: the ID-ascending
// prefix is admitted) and flags Truncated.
func TestRetentionCarrierSetCapTrips(t *testing.T) {
	if testing.Short() {
		t.Skip("wide-fan fixture")
	}
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "WideSubject")
	for i := 0; i < 2100; i++ { // > retentionMaxCarriers (2000)
		c := fix.addSymbol(t, fmt.Sprintf("WideCarrier%04d", i))
		fix.addEdge(t, c, subject, model.EdgeComposes, 0.9)
	}

	res := computeRetention(t, fix, subject)

	if !res.Truncated {
		t.Errorf("Truncated must be set when the carrier-set cap trips")
	}
}

// TestRetentionAllInterfacesScreened: when every candidate via-interface is
// junk, the group is empty — the screen's empty result short-circuits before
// any composer expansion.
func TestRetentionAllInterfacesScreened(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	carrier := fix.addSymbol(t, "CarrierC")
	fix.addEdge(t, carrier, subject, model.EdgeComposes, 0.9)
	_, _ = launderedVia(t, fix, carrier, "Close")
	addCommonMethods(t, fix, "Close", 40)

	res := computeRetention(t, fix, subject)

	if len(res.RetainedViaInterfaces) != 0 || res.RetainedCount != 0 {
		t.Errorf("all-junk candidates must leave the group empty, got %+v", res.RetainedViaInterfaces)
	}
}

// TestRetainedHolderNamesConcreteCarrier: each retained row must name a
// concrete carrier that satisfies its via-interface: the fact the laundering
// round proves and the consumer otherwise re-derives one graph join per
// interface (measured: 30 follow-up lookups on the dolt hub, cell 5).
func TestRetainedHolderNamesConcreteCarrier(t *testing.T) {
	fix, subject, carrier, _, holder := launderedFixture(t)

	res := computeRetention(t, fix, subject)

	found := false
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder {
			found = true
			if rh.Carrier.ID != carrier {
				t.Errorf("Carrier = %d, want concrete carrier %d", rh.Carrier.ID, carrier)
			}
		}
	}
	if !found {
		t.Fatalf("holder missing from RetainedViaInterfaces: %+v", res.RetainedViaInterfaces)
	}
}

// TestRetainedCarrierDeterministicOnMultiSatisfier: when several carriers
// satisfy the via-interface, the lowest-ID carrier is recorded (mirrors the
// lowest-via rule) so output is stable run to run.
func TestRetainedCarrierDeterministicOnMultiSatisfier(t *testing.T) {
	fix, subject, carrier, iface, holder := launderedFixture(t)
	carrier2 := fix.addSymbol(t, "CarrierD")
	fix.addEdge(t, carrier2, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrier2, iface, model.EdgeInherits, confConvention)

	res := computeRetention(t, fix, subject)

	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder && rh.Carrier.ID != carrier {
			t.Errorf("Carrier = %d, want lowest-ID satisfier %d (not %d)", rh.Carrier.ID, carrier, carrier2)
		}
	}
}
