package resolve_test

import (
	"testing"
	"time"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// workerRefs models the mastodon run-method hierarchy: a base worker defining
// #perform/#distribute!, plus subclasses — one inheriting #perform wholesale
// (AccountRaw), one two hops deep (StatusUpdate < Distribution < RawDist).
func workerRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "AP::RawDistributionWorker", FileID: 1, Language: "ruby"},
		{ID: 2, Qualified: "AP::RawDistributionWorker#perform", FileID: 1, Language: "ruby", Receiver: extract.ReceiverInstance},
		{ID: 3, Qualified: "AP::RawDistributionWorker#distribute!", FileID: 1, Language: "ruby", Receiver: extract.ReceiverInstance},
		{ID: 4, Qualified: "AP::AccountRawDistributionWorker", FileID: 2, Language: "ruby"},
		{ID: 5, Qualified: "AP::DistributionWorker", FileID: 3, Language: "ruby"},
		{ID: 6, Qualified: "AP::DistributionWorker#perform", FileID: 3, Language: "ruby", Receiver: extract.ReceiverInstance},
		{ID: 7, Qualified: "AP::StatusUpdateDistributionWorker", FileID: 4, Language: "ruby"},
	}
}

func workerAncestry() map[string][]string {
	return map[string][]string{
		"AP::AccountRawDistributionWorker":   {"AP::RawDistributionWorker"},
		"AP::DistributionWorker":             {"AP::RawDistributionWorker"},
		"AP::StatusUpdateDistributionWorker": {"AP::DistributionWorker"},
	}
}

func TestInheritedResolvesNoOwnPerform(t *testing.T) {
	// AccountRaw has no own #perform — an enqueue edge to its #perform resolves
	// to the inherited RawDistributionWorker#perform.
	ix := resolve.NewIndex(workerRefs()).WithInheritance(workerAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "AP::AccountRawDistributionWorker#perform",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	if !ok {
		t.Fatal("expected inherited resolution to RawDistributionWorker#perform")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2 (AP::RawDistributionWorker#perform)", r.SymbolID)
	}
	if r.Confidence != extract.ConfidenceConvention {
		t.Errorf("Confidence = %v, want %v (unique ancestor keeps base)", r.Confidence, extract.ConfidenceConvention)
	}
}

func TestInheritedWalksMultipleHops(t *testing.T) {
	// StatusUpdate < Distribution < RawDist. StatusUpdate#distribute! resolves
	// two hops up to RawDistributionWorker#distribute!.
	ix := resolve.NewIndex(workerRefs()).WithInheritance(workerAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "AP::StatusUpdateDistributionWorker#distribute!",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution two hops up the chain")
	}
	if r.SymbolID != 3 {
		t.Errorf("SymbolID = %d, want 3 (RawDistributionWorker#distribute!)", r.SymbolID)
	}
}

func TestInheritedPrefersNearestAncestor(t *testing.T) {
	// When both an intermediate and a base ancestor define the method, the
	// nearest one wins. DistributionWorker defines its own #perform, so
	// StatusUpdate#perform resolves to DistributionWorker#perform, not RawDist's.
	ix := resolve.NewIndex(workerRefs()).WithInheritance(workerAncestry())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "AP::StatusUpdateDistributionWorker#perform",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	if !ok {
		t.Fatal("expected resolution to the nearest ancestor's #perform")
	}
	if r.SymbolID != 6 {
		t.Errorf("SymbolID = %d, want 6 (DistributionWorker#perform, nearest)", r.SymbolID)
	}
}

func TestInheritedNoEdgeWhenMethodUndefinedInChain(t *testing.T) {
	// A method no ancestor defines must NOT resolve via inheritance.
	ix := resolve.NewIndex(workerRefs()).WithInheritance(workerAncestry())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "AP::AccountRawDistributionWorker#nonexistent_method",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	if ok {
		t.Error("a method defined in no ancestor must not resolve via inheritance")
	}
}

func TestInheritedNoEdgeForNonSubclassReceiver(t *testing.T) {
	// A receiver with no ancestry entry (e.g. a gem/stdlib class) is never
	// guessed at. RawDistributionWorker has no parent in the map.
	ix := resolve.NewIndex(workerRefs()).WithInheritance(workerAncestry())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "AP::RawDistributionWorker#totally_made_up",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("a receiver with no recorded superclass must not resolve via inheritance")
	}
}

func TestInheritedOnlyMethodDispatch(t *testing.T) {
	// A `::` namespace target must not trigger inherited resolution — it carries
	// no receiver type to inherit through.
	rs := append(workerRefs(), model.SymbolRef{ID: 20, Qualified: "AP::RawDistributionWorker::CONST", FileID: 1, Language: "ruby"})
	anc := workerAncestry()
	anc["AP::AccountRawDistributionWorker::CONST"] = []string{"AP::RawDistributionWorker::CONST"} // nonsense, must be ignored
	ix := resolve.NewIndex(rs).WithInheritance(anc)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "AP::AccountRawDistributionWorker::CONST",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	// The leaf fallback may still bind `::CONST` to the coincidental constant,
	// but only at the demoted name-collision confidence. The inherited step
	// (which would keep BaseConfidence 1.0) must NOT have fired.
	if ok && r.Confidence >= 1.0 {
		t.Errorf("`::` namespace target must not resolve via the inherited-method step; got confidence %v", r.Confidence)
	}
}

func TestInheritedNoOpWithoutAncestryMap(t *testing.T) {
	// Without WithInheritance (the default NewIndex), the step is inert — a
	// no-own-method call falls through to the existing leaf fallback / drop.
	ix := resolve.NewIndex(workerRefs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "AP::AccountRawDistributionWorker#perform",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	// byName["perform"] has two instance candidates (RawDist, Distribution) →
	// ambiguous leaf fallback, which is name-collision (below floor) but still
	// resolves. The point: it did NOT take the precise inherited path.
	if ok {
		// If it resolved, it must be the demoted name-collision, not the precise 0.9.
		r, _ := ix.Resolve(resolve.Request{Target: "AP::AccountRawDistributionWorker#perform", Kind: model.EdgeCalls, SourceFileID: 99, BaseConfidence: extract.ConfidenceConvention})
		if r.Confidence >= extract.ConfidenceConvention {
			t.Errorf("without ancestry, resolution must not reach inherited-path confidence; got %v", r.Confidence)
		}
	}
}

func TestInheritedTerminatesOnCycle(t *testing.T) {
	// A cyclic inherits graph (A < B, B < A) must not hang. The method is
	// defined nowhere, so resolution fails — the point is that it returns.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "A", FileID: 1, Language: "ruby"},
		{ID: 2, Qualified: "B", FileID: 2, Language: "ruby"},
	}
	ix := resolve.NewIndex(rs).WithInheritance(map[string][]string{
		"A": {"B"},
		"B": {"A"},
	})
	done := make(chan struct{})
	go func() {
		ix.Resolve(resolve.Request{
			Target:         "A#whatever",
			Kind:           model.EdgeCalls,
			SourceFileID:   99,
			BaseConfidence: 1.0,
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("resolveInherited did not terminate on a cyclic inherits graph")
	}
}

func TestInheritedMultiParentSameDepthIsAmbiguous(t *testing.T) {
	// A class recorded with two superclasses both defining the method (reopened
	// class with divergent superclass clauses) is an ambiguous pick: the result
	// must be flagged Ambiguous and confidence clamped, not silently confident.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Sub", FileID: 1, Language: "ruby"},
		{ID: 2, Qualified: "ParentA", FileID: 2, Language: "ruby"},
		{ID: 3, Qualified: "ParentA#perform", FileID: 2, Language: "ruby", Receiver: extract.ReceiverInstance},
		{ID: 4, Qualified: "ParentB", FileID: 3, Language: "ruby"},
		{ID: 5, Qualified: "ParentB#perform", FileID: 3, Language: "ruby", Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs).WithInheritance(map[string][]string{
		"Sub": {"ParentA", "ParentB"},
	})
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Sub#perform",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	if !ok {
		t.Fatal("expected resolution to one of the two parents")
	}
	if !r.Ambiguous {
		t.Errorf("two ancestors at the same depth defining the method must flag Ambiguous; got %+v", r)
	}
	if r.Confidence > 0.8 {
		t.Errorf("ambiguous inherited pick must clamp confidence to <=0.8, got %v", r.Confidence)
	}
}

func TestInheritedSingletonDispatch(t *testing.T) {
	// Class-method (`.`) inheritance: Sub.build resolves to Base.build.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Base", FileID: 1, Language: "ruby"},
		{ID: 2, Qualified: "Base.build", FileID: 1, Language: "ruby", Receiver: extract.ReceiverSingleton},
		{ID: 3, Qualified: "Sub", FileID: 2, Language: "ruby"},
	}
	ix := resolve.NewIndex(rs).WithInheritance(map[string][]string{"Sub": {"Base"}})
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Sub.build",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: extract.ConfidenceConvention,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("Sub.build should resolve to Base.build (id 2); got ok=%v id=%d", ok, r.SymbolID)
	}
}
