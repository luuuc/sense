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
	"encoding/json"
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

// TestRetainedRowCarriesDeclaredChain: each retained row names the declared
// containment chain from its carrier down to the subject, so the row is a
// statable structural fact rather than an unverifiable hint (measured:
// agents re-verify hedged rows by hand and lose the wall to it).
func TestRetainedRowCarriesDeclaredChain(t *testing.T) {
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

	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID != holder {
			continue
		}
		got := make([]int64, 0, len(rh.Chain))
		for _, s := range rh.Chain {
			got = append(got, s.ID)
		}
		want := []int64{c2, c1, subject}
		if len(got) != len(want) {
			t.Fatalf("chain IDs = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chain[%d] = %d, want %d (carrier down to subject)", i, got[i], want[i])
			}
		}
		return
	}
	t.Fatalf("holder missing from retained rows")
}

// TestRetainedChainDepthOne: a carrier composing the subject directly chains
// [carrier, subject].
func TestRetainedChainDepthOne(t *testing.T) {
	fix, subject, carrier, _, holder := launderedFixture(t)

	res := computeRetention(t, fix, subject)

	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder {
			if len(rh.Chain) != 2 || rh.Chain[0].ID != carrier || rh.Chain[1].ID != subject {
				ids := []int64{}
				for _, s := range rh.Chain {
					ids = append(ids, s.ID)
				}
				t.Fatalf("chain = %v, want [%d %d]", ids, carrier, subject)
			}
			return
		}
	}
	t.Fatalf("holder missing")
}

// TestRetainedChainDiamondPicksLowestParent: when two level-1 parents both
// compose the same inner carrier (a containment diamond), the rendered chain
// goes through the lowest-ID parent. Kills the recordParent tie-break mutant
// (lowest flipped to highest): pair order from SELECT DISTINCT carries no
// guarantee, so the rule is the only thing standing between a diamond and
// run-to-run chain flapping.
func TestRetainedChainDiamondPicksLowestParent(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	p1 := fix.addSymbol(t, "ParentLow")
	p2 := fix.addSymbol(t, "ParentHigh")
	inner := fix.addSymbol(t, "InnerCarrier")
	iface := fix.addSymbolWith(t, "DiamondIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitDiamondThing", model.KindMethod, &iface)
	holder := fix.addSymbol(t, "DiamondHolder")
	fix.addEdge(t, p1, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, p2, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, inner, p1, model.EdgeComposes, 0.9)
	fix.addEdge(t, inner, p2, model.EdgeComposes, 0.9)
	fix.addEdge(t, inner, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	assertChain(t, res, holder, []int64{inner, p1, subject})
}

// TestRetainedChainNonSeedCycle: a composes cycle among non-seed carriers
// must not corrupt the parent tree. Kills the already-admitted-guard mutant
// in recordParent: without the guard, the cycle's back edge re-parents an
// admitted node onto its own descendant and the chain walk emits a garbage
// bound-length path instead of the true one. The cycle partner is created
// with a LOWER ID than the true parent so the lowest-ID rule cannot mask
// the guard's absence.
func TestRetainedChainNonSeedCycle(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	cyc := fix.addSymbol(t, "CyclePartner") // lower ID than trueParent
	trueParent := fix.addSymbol(t, "TrueParent")
	deep := fix.addSymbol(t, "DeepCarrier")
	iface := fix.addSymbolWith(t, "CycleChainIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitCycleChainThing", model.KindMethod, &iface)
	holder := fix.addSymbol(t, "CycleChainHolder")
	fix.addEdge(t, trueParent, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, deep, trueParent, model.EdgeComposes, 0.9)
	fix.addEdge(t, cyc, deep, model.EdgeComposes, 0.9)
	fix.addEdge(t, deep, cyc, model.EdgeComposes, 0.9)
	fix.addEdge(t, cyc, iface, model.EdgeInherits, confConvention)
	fix.addEdge(t, holder, iface, model.EdgeComposes, 0.9)

	res := computeRetention(t, fix, subject)

	assertChain(t, res, holder, []int64{cyc, deep, trueParent, subject})
}

// assertChain fails unless the holder's rendered chain is exactly want.
func assertChain(t *testing.T, res blast.Result, holder int64, want []int64) {
	t.Helper()
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID != holder {
			continue
		}
		got := make([]int64, 0, len(rh.Chain))
		for _, s := range rh.Chain {
			got = append(got, s.ID)
		}
		if len(got) != len(want) {
			t.Fatalf("chain = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("chain[%d] = %d, want %d (full: %v vs %v)", i, got[i], want[i], got, want)
			}
		}
		return
	}
	t.Fatalf("holder %d missing from retained rows", holder)
}

// --- Test-satisfier purity ---

// addTestFileSymbol writes a symbol living in a _test file, so the purity
// filter's IsTestPath check has something to refuse. The file is created
// lazily on first use and shared by every later call.
func (f *fixtureDB) addTestFileSymbol(t *testing.T, name string) int64 {
	t.Helper()
	ctx := context.Background()
	if f.testFileID == 0 {
		fid, err := f.adapter.WriteFile(ctx, &model.File{
			Path: "fixture_test.rb", Language: "ruby", Hash: "fixtest",
			Symbols: 1, IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("WriteFile test: %v", err)
		}
		f.testFileID = fid
	}
	line := f.nextLine
	f.nextLine += 10
	id, err := f.adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: f.testFileID, Name: name, Qualified: name,
		Kind: model.KindClass, LineStart: line, LineEnd: line + 5,
	})
	if err != nil {
		t.Fatalf("WriteSymbol %s: %v", name, err)
	}
	return id
}

// testHarnessFixture is the minimized temporal shape: a test-only struct that
// both carries the subject and satisfies a pile of interfaces, so every
// composer of those interfaces fabricates a may-retain row (52 of 100 on
// temporal). FakeHarness is written FIRST so it holds the lowest satisfier ID
// : the carrier nomination is lowest-ID-wins, so a fixture where the
// production carrier happens to win anyway would not kill the mutant.
//
//	subject <-composes- FakeHarness (test file) -inherits-> FakeOnlyIface <-composes- GhostHolder
//	subject <-composes- RealCarrier             -inherits-> SharedIface   <-composes- RealHolder
//	                    FakeHarness             -inherits-> SharedIface
func testHarnessFixture(t *testing.T) (fix *fixtureDB, subject, realCarrier, ghost, realHolder int64) {
	t.Helper()
	fix = newFixtureDB(t)
	subject = fix.addSymbol(t, "SubjectA")
	fake := fix.addTestFileSymbol(t, "FakeHarness")
	realCarrier = fix.addSymbol(t, "RealCarrier")

	fakeOnly := fix.addSymbolWith(t, "FakeOnlyIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitFakeOnlyThing", model.KindMethod, &fakeOnly)
	shared := fix.addSymbolWith(t, "SharedIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitSharedThing", model.KindMethod, &shared)

	ghost = fix.addSymbol(t, "GhostHolder")
	realHolder = fix.addSymbol(t, "RealHolder")

	fix.addEdge(t, fake, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, realCarrier, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, fake, fakeOnly, model.EdgeInherits, confConvention)
	fix.addEdge(t, fake, shared, model.EdgeInherits, confConvention)
	fix.addEdge(t, realCarrier, shared, model.EdgeInherits, confConvention)
	fix.addEdge(t, ghost, fakeOnly, model.EdgeComposes, 0.9)
	fix.addEdge(t, realHolder, shared, model.EdgeComposes, 0.9)
	return fix, subject, realCarrier, ghost, realHolder
}

// TestRetentionDropsTestOnlySatisfierRows: an interface satisfied only by a
// test double contributes no rows, and the row that survives is proved by the
// production carrier: never by the test double, even though the double owns
// the lower ID the nomination would otherwise pick.
func TestRetentionDropsTestOnlySatisfierRows(t *testing.T) {
	fix, subject, realCarrier, ghost, realHolder := testHarnessFixture(t)

	res := computeRetention(t, fix, subject)

	if retainedIDs(res)[ghost] {
		t.Errorf("holder %d rides a test-only satisfier and must not be in the ring: %+v", ghost, res.RetainedViaInterfaces)
	}
	if !retainedIDs(res)[realHolder] {
		t.Fatalf("holder %d rides a production satisfier and must stay in the ring: %+v", realHolder, res.RetainedViaInterfaces)
	}
	if res.RetainedCount != 1 {
		t.Errorf("RetainedCount = %d, want 1", res.RetainedCount)
	}
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == realHolder && rh.Carrier.ID != realCarrier {
			t.Errorf("carrier = %d (%s), want the production carrier %d", rh.Carrier.ID, rh.Carrier.Name, realCarrier)
		}
	}
}

// TestRetentionDisclosesExcludedSatisfiers: the refusal is auditable: the
// count and the refused names ride out with the result, so a purified ring is
// distinguishable from a subject nothing launders.
func TestRetentionDisclosesExcludedSatisfiers(t *testing.T) {
	fix, subject, _, _, _ := testHarnessFixture(t)

	res := computeRetention(t, fix, subject)

	if res.RetainedPurity.Excluded != 1 {
		t.Errorf("RetainedPurity.Excluded = %d, want 1", res.RetainedPurity.Excluded)
	}
	if len(res.RetainedPurity.Names) != 1 || res.RetainedPurity.Names[0] != "FakeHarness" {
		t.Errorf("RetainedPurity.Names = %v, want [FakeHarness]", res.RetainedPurity.Names)
	}
}

// TestRetentionExcludedNamesCapped: the disclosed sample is bounded at five
// names while the count keeps the true magnitude, so a harness package with
// dozens of fakes cannot spend the ring's token budget on its own exclusion.
func TestRetentionExcludedNamesCapped(t *testing.T) {
	fix := newFixtureDB(t)
	subject := fix.addSymbol(t, "SubjectA")
	iface := fix.addSymbolWith(t, "RareCappedIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitRareCappedThing", model.KindMethod, &iface)
	for i := range 7 {
		fake := fix.addTestFileSymbol(t, fmt.Sprintf("Fake%d", i))
		fix.addEdge(t, fake, subject, model.EdgeComposes, 0.9)
		fix.addEdge(t, fake, iface, model.EdgeInherits, confConvention)
	}

	res := computeRetention(t, fix, subject)

	if res.RetainedPurity.Excluded != 7 {
		t.Errorf("Excluded = %d, want 7", res.RetainedPurity.Excluded)
	}
	if len(res.RetainedPurity.Names) != 5 {
		t.Errorf("len(Names) = %d, want 5 (capped), got %v", len(res.RetainedPurity.Names), res.RetainedPurity.Names)
	}
}

// --- Promiscuity stamp ---

// TestRetentionStampsViaSatisfiers: a row riding an interface many unrelated
// concretes satisfy is KEPT and stamped with the count. This is the pebble
// trap in reverse: the paid-win ring there runs through a generic iterator
// interface, so promiscuity annotates and must never drop.
func TestRetentionStampsViaSatisfiers(t *testing.T) {
	fix, subject, _, iface, holder := launderedFixture(t)
	// Five more concretes satisfy the same interface without carrying the
	// subject: they change the stamp, not the ring.
	for i := range 5 {
		other := fix.addSymbol(t, fmt.Sprintf("Unrelated%d", i))
		fix.addEdge(t, other, iface, model.EdgeInherits, confConvention)
	}
	// An interface embedding the via extends the contract, it cannot be the
	// runtime value of the field, so it must not inflate the stamp.
	extender := fix.addSymbolWith(t, "ExtendingIface", model.KindInterface, nil)
	fix.addEdge(t, extender, iface, model.EdgeInherits, confConvention)

	res := computeRetention(t, fix, subject)

	if !retainedIDs(res)[holder] {
		t.Fatalf("promiscuous via must keep its row: %+v", res.RetainedViaInterfaces)
	}
	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID != holder {
			continue
		}
		if rh.ViaSatisfiers != 6 {
			t.Errorf("ViaSatisfiers = %d, want 6 (carrier + 5 unrelated concretes, no interfaces)", rh.ViaSatisfiers)
		}
	}
}

// TestRetentionStampsSoleSatisfier pins the low end: a two-party contract
// stamps 1, so the consumer can tell a narrow via from a promiscuous one.
func TestRetentionStampsSoleSatisfier(t *testing.T) {
	fix, subject, _, _, holder := launderedFixture(t)

	res := computeRetention(t, fix, subject)

	for _, rh := range res.RetainedViaInterfaces {
		if rh.Symbol.ID == holder && rh.ViaSatisfiers != 1 {
			t.Errorf("ViaSatisfiers = %d, want 1", rh.ViaSatisfiers)
		}
	}
}

// --- Deterministic ring order ---

// orderFixture builds a ring whose creation order, test/production split, and
// ID order all disagree, so an exact-sequence assertion over it kills any
// perturbation of the comparator: TestHolder is created FIRST (lowest ID) yet
// must sort LAST, and the production holders must come out ID-ascending.
func orderFixture(t *testing.T) (fix *fixtureDB, subject int64, want []int64) {
	t.Helper()
	fix = newFixtureDB(t)
	subject = fix.addSymbol(t, "SubjectA")
	carrier := fix.addSymbol(t, "CarrierC")
	iface := fix.addSymbolWith(t, "RareOrderIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitRareOrderThing", model.KindMethod, &iface)
	fix.addEdge(t, carrier, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrier, iface, model.EdgeInherits, confConvention)

	testHolder := fix.addTestFileSymbol(t, "TestHolder")
	first := fix.addSymbol(t, "HolderA")
	second := fix.addSymbol(t, "HolderB")
	for _, h := range []int64{testHolder, first, second} {
		fix.addEdge(t, h, iface, model.EdgeComposes, 0.9)
	}
	return fix, subject, []int64{first, second, testHolder}
}

// ringOrder renders one computation's ring as its holder ID sequence.
func ringOrder(res blast.Result) []int64 {
	out := make([]int64, 0, len(res.RetainedViaInterfaces))
	for _, rh := range res.RetainedViaInterfaces {
		out = append(out, rh.Symbol.ID)
	}
	return out
}

// TestRetentionRingOrderIsPinned: the ring comes out in one exact sequence ,
// production holders ID-ascending, then test-file holders. Paging is a cursor
// over this order, so a perturbation here silently drops or duplicates rows
// across pages; the exact sequence is the tripwire.
func TestRetentionRingOrderIsPinned(t *testing.T) {
	fix, subject, want := orderFixture(t)

	got := ringOrder(computeRetention(t, fix, subject))

	if len(got) != len(want) {
		t.Fatalf("ring = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ring[%d] = %d, want %d (full: %v vs %v)", i, got[i], want[i], got, want)
		}
	}
}

// TestRetentionRingOrderIsRepeatable: two calls on the same index return the
// ring byte-for-byte identically. The comparator is a total order (symbol IDs
// are unique), so no pair may swap between runs: the precondition every page
// boundary rests on.
func TestRetentionRingOrderIsRepeatable(t *testing.T) {
	fix, subject, _ := orderFixture(t)

	first, err := json.Marshal(computeRetention(t, fix, subject).RetainedViaInterfaces)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for run := range 5 {
		again, err := json.Marshal(computeRetention(t, fix, subject).RetainedViaInterfaces)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(again) != string(first) {
			t.Fatalf("run %d differs:\n%s\n%s", run, first, again)
		}
	}
}

// --- Ring paging ---

// pagedFixture builds a ring of five holders on one via-interface, the
// smallest shape where two pages have a real boundary between them.
func pagedFixture(t *testing.T) (fix *fixtureDB, subject int64) {
	t.Helper()
	fix = newFixtureDB(t)
	subject = fix.addSymbol(t, "SubjectA")
	carrier := fix.addSymbol(t, "CarrierC")
	iface := fix.addSymbolWith(t, "RarePageIface", model.KindInterface, nil)
	fix.addSymbolWith(t, "VisitRarePageThing", model.KindMethod, &iface)
	fix.addEdge(t, carrier, subject, model.EdgeComposes, 0.9)
	fix.addEdge(t, carrier, iface, model.EdgeInherits, confConvention)
	for i := range 5 {
		h := fix.addSymbol(t, fmt.Sprintf("PagedHolder%d", i))
		fix.addEdge(t, h, iface, model.EdgeComposes, 0.9)
	}
	return fix, subject
}

// computePage runs one blast with an explicit ring window.
func computePage(t *testing.T, fix *fixtureDB, subject int64, offset, limit int) blast.Result {
	t.Helper()
	res, err := blast.Compute(context.Background(), fix.db, []int64{subject},
		blast.Options{MaxHops: 3, MinConfidence: 0.1, MaxResults: limit, RetainedOffset: offset})
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	return res
}

// TestRetentionPagesCoverRingExactly: two consecutive pages cover the whole
// ring with no duplicate and no gap, and every page reports the FULL count ,
// the property that makes a truncated ring safe to resume instead of
// abandon.
func TestRetentionPagesCoverRingExactly(t *testing.T) {
	fix, subject := pagedFixture(t)

	whole := ringOrder(computePage(t, fix, subject, 0, 100))
	if len(whole) != 5 {
		t.Fatalf("ring = %v, want 5 holders", whole)
	}

	first := computePage(t, fix, subject, 0, 3)
	second := computePage(t, fix, subject, 3, 3)
	union := append(ringOrder(first), ringOrder(second)...)

	if len(union) != len(whole) {
		t.Fatalf("paged union = %v, want the whole ring %v", union, whole)
	}
	for i := range whole {
		if union[i] != whole[i] {
			t.Fatalf("union[%d] = %d, want %d (union %v vs whole %v)", i, union[i], whole[i], union, whole)
		}
	}
	for _, page := range []blast.Result{first, second} {
		if page.RetainedCount != 5 {
			t.Errorf("page reports count %d, want the full ring size 5", page.RetainedCount)
		}
	}
	if first.RetainedOffset != 0 || second.RetainedOffset != 3 {
		t.Errorf("offsets = %d/%d, want 0/3", first.RetainedOffset, second.RetainedOffset)
	}
}

// TestRetentionPageBeyondRingIsEmpty: an offset past the end returns no rows
// but still reports where the ring actually ends, so an over-shot cursor is
// self-correcting rather than an error round trip.
func TestRetentionPageBeyondRingIsEmpty(t *testing.T) {
	fix, subject := pagedFixture(t)

	res := computePage(t, fix, subject, 50, 3)

	if len(res.RetainedViaInterfaces) != 0 {
		t.Errorf("rows = %d, want 0 past the end", len(res.RetainedViaInterfaces))
	}
	if res.RetainedCount != 5 {
		t.Errorf("RetainedCount = %d, want 5", res.RetainedCount)
	}
}

// TestRetentionFingerprintIdentifiesGeneration: pages cut from one index
// carry the same fingerprint, and re-indexing a file moves it: the signal
// that two pages must not be unioned.
func TestRetentionFingerprintIdentifiesGeneration(t *testing.T) {
	fix, subject := pagedFixture(t)

	first := computePage(t, fix, subject, 0, 3)
	second := computePage(t, fix, subject, 3, 3)
	if first.RetainedFingerprint == "" {
		t.Fatal("a paged ring must carry an index fingerprint")
	}
	if first.RetainedFingerprint != second.RetainedFingerprint {
		t.Errorf("same index gave different fingerprints: %q vs %q", first.RetainedFingerprint, second.RetainedFingerprint)
	}

	if _, err := fix.adapter.WriteFile(context.Background(), &model.File{
		Path: "later.rb", Language: "ruby", Hash: "later",
		Symbols: 1, IndexedAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if after := computePage(t, fix, subject, 0, 3); after.RetainedFingerprint == first.RetainedFingerprint {
		t.Errorf("fingerprint %q survived a re-index; a moved index must be visible", after.RetainedFingerprint)
	}
}
