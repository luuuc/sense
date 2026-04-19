package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// refs is a small test index covering every resolution path the
// resolver exposes. IDs are ascending so NewIndex's "first bucket
// element = lowest id" contract holds.
func refs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "app.User", FileID: 10},
		{ID: 2, Qualified: "app.User.email", FileID: 10},   // method on class via `.`
		{ID: 3, Qualified: "Greeter#hello", FileID: 11},    // Ruby instance method
		{ID: 4, Qualified: "Greeter#greet", FileID: 11},
		{ID: 5, Qualified: "Money::new", FileID: 12},       // Rust associated fn
		{ID: 6, Qualified: "Money::display", FileID: 12},
		{ID: 7, Qualified: "test.User", FileID: 20},        // same bare name, different file
		{ID: 8, Qualified: "helper", FileID: 10},           // top-level fn, no parent
		{ID: 9, Qualified: "fmt.Sprintf", FileID: 30},      // unqualified target for fallback
	}
}

func TestResolveExactQualifiedUnique(t *testing.T) {
	ix := resolve.NewIndex(refs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "app.User.email",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2", r.SymbolID)
	}
	if r.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0", r.Confidence)
	}
}

func TestResolveAmbiguousPrefersSameFile(t *testing.T) {
	// Two rows under qualified "Dup", in different files. Source
	// lives in file 11 → prefer the file-11 match (id 101). Per the
	// pitch, ambiguous resolution clamps confidence to 0.8 and
	// flags the result — same-file preference breaks the tie but
	// doesn't remove the ambiguity.
	rs := append(refs(), model.SymbolRef{ID: 100, Qualified: "Dup", FileID: 10})
	rs = append(rs, model.SymbolRef{ID: 101, Qualified: "Dup", FileID: 11})
	ix := resolve.NewIndex(rs)

	r, ok := ix.Resolve(resolve.Request{
		Target:         "Dup",
		Kind:           model.EdgeInherits,
		SourceFileID:   11,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 101 {
		t.Errorf("SymbolID = %d, want 101 (same-file preference)", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (ambiguous clamp applies even on same-file win)", r.Confidence)
	}
	if !r.Ambiguous {
		t.Error("Ambiguous = false, want true (multiple candidates)")
	}
}

func TestResolveAmbiguousCrossFileClampsConfidence(t *testing.T) {
	rs := append(refs(), model.SymbolRef{ID: 100, Qualified: "Dup", FileID: 10})
	rs = append(rs, model.SymbolRef{ID: 101, Qualified: "Dup", FileID: 11})
	ix := resolve.NewIndex(rs)

	// Source lives in file 99 — no same-file candidate. Falls back to
	// lowest-id (100), confidence clamped to 0.8.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Dup",
		Kind:           model.EdgeInherits,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 100 {
		t.Errorf("SymbolID = %d, want 100 (lowest id, no same-file)", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", r.Confidence)
	}
	if !r.Ambiguous {
		t.Error("Ambiguous = false, want true (multiple candidates)")
	}
}

func TestResolveReceiverRewriteRust(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Source is `Money::new`, target as written is `Self::display`.
	// Rewrite to `Money::display` and look up (id 6).
	r, ok := ix.Resolve(resolve.Request{
		Target:                "Self::display",
		Kind:                  model.EdgeCalls,
		SourceFileID:          12,
		SourceQualified:       "Money::new",
		SourceParentQualified: "Money",
		BaseConfidence:        1.0,
	})
	if !ok {
		t.Fatal("expected resolution after Self:: rewrite")
	}
	if r.SymbolID != 6 {
		t.Errorf("SymbolID = %d, want 6 (Money::display)", r.SymbolID)
	}
}

func TestResolveReceiverRewriteSelfDotPython(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Python-style: source qualified `app.User.profile`, target
	// `self.email`. Rewrite to `app.User.email`.
	r, ok := ix.Resolve(resolve.Request{
		Target:                "self.email",
		Kind:                  model.EdgeCalls,
		SourceFileID:          10,
		SourceQualified:       "app.User.profile",
		SourceParentQualified: "app.User",
		BaseConfidence:        1.0,
	})
	if !ok {
		t.Fatal("expected resolution after self. rewrite")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2 (app.User.email)", r.SymbolID)
	}
}

func TestResolveReceiverRewriteHashRuby(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Ruby: source is `Greeter#greet`, target `self.hello`. The
	// separator derives from source/parent relation: `Greeter#greet`
	// relative to `Greeter` starts with `#`, so rewrite to
	// `Greeter#hello`.
	r, ok := ix.Resolve(resolve.Request{
		Target:                "self.hello",
		Kind:                  model.EdgeCalls,
		SourceFileID:          11,
		SourceQualified:       "Greeter#greet",
		SourceParentQualified: "Greeter",
		BaseConfidence:        1.0,
	})
	if !ok {
		t.Fatal("expected resolution after self.-on-Ruby rewrite")
	}
	if r.SymbolID != 3 {
		t.Errorf("SymbolID = %d, want 3 (Greeter#hello)", r.SymbolID)
	}
}

func TestResolveReceiverRewriteSkippedWithoutParent(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Top-level function has no parent — self rewrites must no-op.
	// Target's trailing segment also has no name match, so the
	// unqualified fallback doesn't interfere with the rewrite check.
	_, ok := ix.Resolve(resolve.Request{
		Target:                "self.nonexistent_name",
		Kind:                  model.EdgeCalls,
		SourceFileID:          10,
		SourceQualified:       "helper",
		SourceParentQualified: "",
		BaseConfidence:        1.0,
	})
	if ok {
		t.Error("expected no resolution for self. with no source parent")
	}
}

func TestResolveUnqualifiedFallbackForCalls(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Bare `Sprintf` doesn't exist as a qualified name but `fmt.Sprintf`
	// does, and its unqualified segment is `Sprintf`. The fallback
	// fires for calls, with confidence clamped to 0.8.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "whatever.Sprintf",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected unqualified fallback resolution")
	}
	if r.SymbolID != 9 {
		t.Errorf("SymbolID = %d, want 9 (fmt.Sprintf)", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8", r.Confidence)
	}
}

func TestResolveUnqualifiedFallbackGatedToCallsOnly(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Same `whatever.Sprintf` target, but this time it's an inherits
	// edge. Inherits does not fall back to unqualified; the edge
	// drops cleanly.
	_, ok := ix.Resolve(resolve.Request{
		Target:         "whatever.Sprintf",
		Kind:           model.EdgeInherits,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("unqualified fallback should not fire for non-calls kinds")
	}
}

func TestResolveNoMatch(t *testing.T) {
	ix := resolve.NewIndex(refs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "totally.missing",
		Kind:           model.EdgeInherits,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("expected no resolution for missing target")
	}
}

// TestResolveSeparatorMismatchNoOps guards the resilience of
// rewriteReceiver when the parent qualified name is a prefix of
// sourceQualified but isn't followed by one of the recognised
// separators (`.`, `#`, `::`). The rewrite must no-op and lookup
// must fail cleanly rather than producing a garbage target.
func TestResolveSeparatorMismatchNoOps(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// Target trailing segment is also absent from byName, so the
	// calls fallback doesn't rescue us — the test observes rewrite
	// behavior without side channels.
	_, ok := ix.Resolve(resolve.Request{
		Target:                "self.nonexistent_name",
		Kind:                  model.EdgeCalls,
		SourceFileID:          10,
		SourceQualified:       "FooBar",
		SourceParentQualified: "Foo", // prefix of "FooBar" but no separator follows
		BaseConfidence:        1.0,
	})
	if ok {
		t.Error("expected no resolution when parent-qualified isn't followed by a separator")
	}
}

// TestResolveBareTargetNoFallback pins that an unqualified target
// with no separator doesn't double-dip through the fallback path:
// unqualifiedName(target) == target, so the fallback guard skips.
// Exact match either hits or misses; there's no second chance.
func TestResolveBareTargetNoFallback(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// `helper` is a real qualified name (id 8) at bare form — hits
	// byQualified directly, no fallback involved.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "helper",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 8 {
		t.Fatalf("expected exact match to helper (id=8), got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence != 1.0 {
		t.Errorf("Confidence = %v, want 1.0 (exact match, not fallback)", r.Confidence)
	}

	// `nonexistent_bare` matches neither byQualified nor byName →
	// clean miss, no fallback engagement.
	_, ok = ix.Resolve(resolve.Request{
		Target:         "nonexistent_bare",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("expected miss for bare target with no matches")
	}
}
