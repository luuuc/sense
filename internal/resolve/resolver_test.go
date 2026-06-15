package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// refs is a small test index covering every resolution path the
// resolver exposes. IDs are ascending so NewIndex's "first bucket
// element = lowest id" contract holds.
func refs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "app.User", FileID: 10},
		{ID: 2, Qualified: "app.User.email", FileID: 10}, // method on class via `.`
		{ID: 3, Qualified: "Greeter#hello", FileID: 11},  // Ruby instance method
		{ID: 4, Qualified: "Greeter#greet", FileID: 11},
		{ID: 5, Qualified: "Money::new", FileID: 12}, // Rust associated fn
		{ID: 6, Qualified: "Money::display", FileID: 12},
		{ID: 7, Qualified: "test.User", FileID: 20},   // same bare name, different file
		{ID: 8, Qualified: "helper", FileID: 10},      // top-level fn, no parent
		{ID: 9, Qualified: "fmt.Sprintf", FileID: 30}, // unqualified target for fallback
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

func TestResolveReceiverRewriteNoOpForNonSelfTarget(t *testing.T) {
	ix := resolve.NewIndex(refs())
	// The source has a parent, but the target is an ordinary qualified name,
	// not a `self.`/`Self::` reference — the receiver rewrite must leave it
	// untouched and exact resolution proceeds normally.
	r, ok := ix.Resolve(resolve.Request{
		Target:                "app.User.email",
		Kind:                  model.EdgeCalls,
		SourceFileID:          10,
		SourceQualified:       "app.User.profile",
		SourceParentQualified: "app.User",
		BaseConfidence:        1.0,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected unchanged target to resolve to id 2, got id=%d ok=%v", r.SymbolID, ok)
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
	// `whatever.Sprintf` misses exact lookup; only its leaf `Sprintf` matches
	// `fmt.Sprintf`. The fallback fires for calls edges (the point of this
	// test), but `whatever` is not an indexed receiver type, so binding the
	// leaf is an unverified cross-scope guess: confidence lands below blast's
	// floor while the edge still resolves for dead-code liveness.
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
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (unverified receiver prefix demoted)", r.Confidence)
	}
}

func TestResolveUnqualifiedFallbackForTests(t *testing.T) {
	ix := resolve.NewIndex(refs())
	r, ok := ix.Resolve(resolve.Request{
		Target:         "whatever.Sprintf",
		Kind:           model.EdgeTests,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected unqualified fallback resolution for tests edges")
	}
	if r.SymbolID != 9 {
		t.Errorf("SymbolID = %d, want 9 (fmt.Sprintf)", r.SymbolID)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (unverified receiver prefix demoted)", r.Confidence)
	}
}

func TestResolveUnqualifiedFallbackGatedByKind(t *testing.T) {
	ix := resolve.NewIndex(refs())
	_, ok := ix.Resolve(resolve.Request{
		Target:         "whatever.Sprintf",
		Kind:           model.EdgeInherits,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("unqualified fallback should not fire for inherits edges")
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

func TestResolveIncludesCrossFile(t *testing.T) {
	// Include edges resolve cross-file via byQualified just like any
	// other edge kind — there is no intra-file restriction.
	tests := []struct {
		name     string
		refs     []model.SymbolRef
		target   string
		wantOK   bool
		wantID   int64
		wantConf float64
	}{
		{
			name:   "bare target cross-file",
			refs:   []model.SymbolRef{{ID: 1, Qualified: "Topic", FileID: 10}, {ID: 2, Qualified: "HasErrors", FileID: 20}},
			target: "HasErrors",
			wantOK: true, wantID: 2, wantConf: 1.0,
		},
		{
			name:   "scope resolution cross-file",
			refs:   []model.SymbolRef{{ID: 1, Qualified: "Topic", FileID: 10}, {ID: 2, Qualified: "RateLimiter::OnCreate", FileID: 30}},
			target: "RateLimiter::OnCreate",
			wantOK: true, wantID: 2, wantConf: 1.0,
		},
		{
			name:   "unresolved target",
			refs:   []model.SymbolRef{{ID: 1, Qualified: "Topic", FileID: 10}},
			target: "NonExistent",
			wantOK: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := resolve.NewIndex(tt.refs)
			r, ok := ix.Resolve(resolve.Request{
				Target:         tt.target,
				Kind:           model.EdgeIncludes,
				SourceFileID:   10,
				BaseConfidence: 1.0,
			})
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if r.SymbolID != tt.wantID {
				t.Errorf("SymbolID = %d, want %d", r.SymbolID, tt.wantID)
			}
			if r.Confidence != tt.wantConf {
				t.Errorf("Confidence = %v, want %v", r.Confidence, tt.wantConf)
			}
		})
	}
}

func TestResolveAmbiguousNameOnlyDroppedBelowBlastFloor(t *testing.T) {
	// Two symbols share a trailing name but neither matches the target's
	// qualified form, so resolution falls to the bare-name index with
	// multiple candidates. That guess must land below blast's 0.5 floor.
	rs := append(refs(),
		model.SymbolRef{ID: 200, Qualified: "A.process", FileID: 10},
		model.SymbolRef{ID: 201, Qualified: "B.process", FileID: 11},
	)
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "process",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution via name fallback")
	}
	if !r.Ambiguous {
		t.Error("Ambiguous = false, want true (multiple same-named candidates)")
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 so blast ignores the guess", r.Confidence)
	}
}

func TestResolveSingleNameOnlyKeepsConfidence(t *testing.T) {
	// A unique bare-name match is trustworthy enough to keep the ambiguous
	// clamp (0.8) — only multi-candidate fallback is dropped below the floor.
	rs := append(refs(), model.SymbolRef{ID: 300, Qualified: "Only.unique_method", FileID: 10})
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "unique_method",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.Ambiguous {
		t.Error("single match should not be ambiguous")
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (single name-only clamp)", r.Confidence)
	}
}

// receiverRefs models the headline collision: a class/singleton method and a
// same-named instance method. Their qualified names differ by separator, so
// they share a bare-name bucket and the unqualified fallback must use the
// call's dispatch kind to pick the right one.
func receiverRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "PriceValue.zero", FileID: 10, Receiver: extract.ReceiverSingleton},
		{ID: 2, Qualified: "Counter#zero", FileID: 11, Receiver: extract.ReceiverInstance},
	}
}

func TestResolveInstanceCallDoesNotBindSingleton(t *testing.T) {
	ix := resolve.NewIndex(receiverRefs())
	// Instance dispatch: `something#zero` whose class guess missed. The
	// fallback must land on the instance method, never the PriceValue
	// singleton — the bug that attributed `.zero?`-style calls to PriceValue.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Counter#zero",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 2 {
		t.Errorf("SymbolID = %d, want 2 (Counter#zero instance method)", r.SymbolID)
	}
	if r.Ambiguous {
		t.Error("dispatch kind uniquely disambiguates — should not be ambiguous")
	}
}

func TestResolveSingletonCallDoesNotBindInstance(t *testing.T) {
	ix := resolve.NewIndex(receiverRefs())
	// Class dispatch on a constant whose qualified form missed: must land on
	// the singleton, not the same-named instance method.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Other.zero",
		Kind:           model.EdgeCalls,
		SourceFileID:   99,
		BaseConfidence: 1.0,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 1 {
		t.Errorf("SymbolID = %d, want 1 (PriceValue.zero singleton)", r.SymbolID)
	}
}

func TestResolveImplicitSelfReachesInstanceConcernMethod(t *testing.T) {
	// A template/file-level `self.current_currency` (no source parent to
	// rewrite against) must still resolve to an instance concern method. The
	// `self.` sentinel's trailing `.` must not be mistaken for singleton
	// dispatch and filter the instance candidate out.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "CurrencyContext#current_currency", FileID: 11, Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "self.current_currency",
		Kind:           model.EdgeCalls,
		SourceFileID:   50, // a view file, no parent class
		BaseConfidence: extract.ConfidenceConvention,
	})
	if !ok {
		t.Fatal("expected resolution to the concern method")
	}
	if r.SymbolID != 1 {
		t.Errorf("SymbolID = %d, want 1 (CurrencyContext#current_currency)", r.SymbolID)
	}
}

func TestResolveDropsCodeToCodeCrossLanguageFallback(t *testing.T) {
	// A Ruby file's bare `application` call must not bind to a JS symbol named
	// `application` (the Stimulus entrypoint). Cross-language code-to-code
	// bare-name matches are coincidences, so the edge drops to unresolved.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "StripeClient#charge", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "application", FileID: 60, Language: "javascript"},
	}
	ix := resolve.NewIndex(rs)
	// `Rails.application` misses exact lookup, so resolution falls back to the
	// leaf `application`, whose only candidate is the JS symbol. The language
	// gate must drop it (Ruby source, JS candidate) so the edge stays unresolved
	// rather than becoming a cross-language phantom.
	_, ok := ix.Resolve(resolve.Request{
		Target:         "Rails.application",
		Kind:           model.EdgeCalls,
		SourceFileID:   10, // ruby file
		BaseConfidence: 1.0,
	})
	if ok {
		t.Error("expected no resolution: a Ruby source must not bind a same-named JS symbol")
	}
}

func TestResolveKeepsFallbackWhenReceiverTypeIsIndexed(t *testing.T) {
	// The inheritance case the demotion must NOT break: `Child#describe` misses
	// exact lookup because `describe` is defined on its parent, but `Child` is
	// an indexed type, so an inherited (or reopened) method is plausible. The
	// leaf bind keeps the 0.8 ambiguous clamp rather than being demoted.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Child", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "Parent#describe", FileID: 11, Language: "ruby", Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Child#describe",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected fallback to id 2 (Parent#describe), got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (indexed receiver type — inherited method kept)", r.Confidence)
	}
}

func TestResolveDemotesBareUnresolvedGuess(t *testing.T) {
	// A Ruby call on an unknown receiver (`x.body`) is emitted bare at
	// ConfidenceUnresolved. Its only same-named match is a coincidental test
	// method. With no receiver type to verify, the bare guess must land below
	// blast's floor — it still resolves for dead-code liveness.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Hub2Client#post", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "TranslationServiceTest.body", FileID: 20, Language: "ruby", Receiver: extract.ReceiverSingleton},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "body",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceUnresolved,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected bare fallback to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (bare unresolved guess demoted)", r.Confidence)
	}
}

func TestResolveDemotesFallbackWhenReceiverTypeIsExternal(t *testing.T) {
	// The headline residual bug: a rescue variable typed to an external gem
	// class — `Stripe::StripeError#message` — misses exact lookup (the gem is
	// not indexed) and its leaf `message` binds to a coincidental same-named
	// test method. The receiver type is not indexed, so this is an unverified
	// cross-type guess and must land below blast's floor while still resolving.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "StripeClient#charge", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "TranslationServiceTest.message", FileID: 20, Language: "ruby", Receiver: extract.ReceiverSingleton},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Stripe::StripeError#message",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceDynamic,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected leaf fallback to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (external receiver type — cross-type guess demoted)", r.Confidence)
	}
}

func TestResolveDemotesReceiverKindContradiction(t *testing.T) {
	// Even when the receiver type IS indexed, a dispatch-kind contradiction is
	// evidence of a wrong bind: an instance call `Widget#size` whose only
	// same-named candidate is a singleton method cannot be that method. It
	// resolves (the set is kept as a tie-break) but is demoted below the floor.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Widget", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "Catalog.size", FileID: 11, Language: "ruby", Receiver: extract.ReceiverSingleton},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Widget#size",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected fallback to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (instance call vs singleton-only candidate demoted)", r.Confidence)
	}
}

func TestResolveViewSourceKeepsReceiverDispatchFallback(t *testing.T) {
	// View templates dispatch into helpers loosely, so the receiver-dispatch
	// demotion is off for view-language sources (mirroring the language gate's
	// view carve-out). An ERB source's `helper.format` keeps base confidence
	// even though `helper` is not an indexed type. Symbol id 2 anchors the erb
	// file's language so fileLang[50] == "erb".
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "MoneyHelper.format", FileID: 11, Language: "ruby", Receiver: extract.ReceiverSingleton},
		{ID: 2, Qualified: "turbo-frame:cart", FileID: 50, Language: "erb"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "helper.format",
		Kind:           model.EdgeCalls,
		SourceFileID:   50,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 1 {
		t.Fatalf("expected erb→ruby fallback to id 1, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (view source exempt from receiver-dispatch demotion)", r.Confidence)
	}
}

func TestResolveViewSourceKeepsCrossLanguageHelperCall(t *testing.T) {
	// An ERB template calling a Ruby helper (`current_user`) by bare name is a
	// legitimate cross-language view edge. The gate is OFF for view-language
	// sources, so it must still resolve. Symbol id 2 anchors the erb file's
	// language so fileLang[50] == "erb".
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "ApplicationController#current_user", FileID: 11, Language: "ruby", Receiver: extract.ReceiverInstance},
		{ID: 2, Qualified: "turbo-frame:cart", FileID: 50, Language: "erb"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "current_user",
		Kind:           model.EdgeCalls,
		SourceFileID:   50, // erb template
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 1 {
		t.Fatalf("expected erb→ruby helper edge to id 1, got id=%d ok=%v", r.SymbolID, ok)
	}
}

func TestResolveViewSourceKeepsCrossLanguageStimulusCall(t *testing.T) {
	// The headline case from the report: an ERB template referencing a Stimulus
	// JS controller by bare name (data-controller="photo-upload"). The source is
	// a view language, so the gate is OFF and the cross-language ERB→JS edge must
	// survive. Symbol id 2 anchors the erb file's language so fileLang[50]=="erb".
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "PhotoUploadController", FileID: 60, Language: "javascript"},
		{ID: 2, Qualified: "turbo-frame:cart", FileID: 50, Language: "erb"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "x.PhotoUploadController",
		Kind:           model.EdgeCalls,
		SourceFileID:   50, // erb template
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 1 {
		t.Fatalf("expected erb→js Stimulus edge to id 1, got id=%d ok=%v", r.SymbolID, ok)
	}
}

func TestResolveExactMatchDroppedWhenCrossLanguage(t *testing.T) {
	// A Ruby production call to `I18n.t` exact-matches a JS test-helper symbol
	// of the same qualified name. The exact byQualified path now applies the
	// language gate too, so the cross-language coincidence is dropped rather
	// than returned at full confidence — and it does not leaf-fall-back into a
	// further coincidence.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "LafricaClient#handle", FileID: 10, Language: "ruby", Path: "app/clients/lafrica_client.rb"},
		{ID: 2, Qualified: "I18n.t", FileID: 60, Language: "javascript", Path: "test/system/support/i18n.js"},
	}
	ix := resolve.NewIndex(rs)
	if _, ok := ix.Resolve(resolve.Request{
		Target:         "I18n.t",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	}); ok {
		t.Error("expected no resolution: a Ruby source must not exact-bind a same-named JS symbol")
	}
}

func TestResolveExactMatchKeepsSyntheticCrossLanguageTarget(t *testing.T) {
	// A Ruby controller rendering a partial emits the synthetic `partial:` target
	// that resolves to an ERB symbol. This cross-language edge is intentional, so
	// the exact-path language gate must exempt synthetic-prefix targets.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "OrdersController#show", FileID: 10, Language: "ruby", Path: "app/controllers/orders_controller.rb"},
		{ID: 2, Qualified: "partial:orders/line_item", FileID: 50, Language: "erb", Path: "app/views/orders/_line_item.html.erb"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "partial:orders/line_item",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected synthetic cross-language edge to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
}

func TestResolveProductionDoesNotBindTestSymbol(t *testing.T) {
	// A production helper's `url_for` (really ActionView's, unindexed) misses
	// exact lookup and its only same-named candidate is a test helper method.
	// Production code never has a real calls edge into a test file, so the edge
	// drops to unresolved rather than binding the coincidence.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "FiltersHelper#filter_url", FileID: 10, Language: "ruby", Path: "app/helpers/filters_helper.rb"},
		{ID: 2, Qualified: "FiltersHelperTest#url_for", FileID: 20, Language: "ruby", Path: "test/helpers/filters_helper_test.rb", Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs)
	if _, ok := ix.Resolve(resolve.Request{
		Target:         "FiltersHelper#url_for",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceDynamic,
	}); ok {
		t.Error("expected no resolution: production code must not bind a test-file symbol")
	}
}

func TestResolveProductionPrefersNonTestCandidate(t *testing.T) {
	// When a production call has both a production and a test candidate of the
	// same name, the test candidate is excluded and the production one wins.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Caller#act", FileID: 10, Language: "ruby", Path: "app/caller.rb"},
		{ID: 2, Qualified: "Worker#process", FileID: 11, Language: "ruby", Path: "app/worker.rb", Receiver: extract.ReceiverInstance},
		{ID: 3, Qualified: "FakeTest#process", FileID: 20, Language: "ruby", Path: "test/fake_test.rb", Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Unknown#process",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceDynamic,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected production candidate id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
}

func TestResolveTestSourceKeepsTestCandidate(t *testing.T) {
	// The gate is one-directional: a test-file source legitimately calls a test
	// helper, so test candidates are kept when the source itself is a test file.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "WidgetTest#setup", FileID: 20, Language: "ruby", Path: "test/models/widget_test.rb"},
		{ID: 2, Qualified: "TestDataHelpers#build_widget", FileID: 21, Language: "ruby", Path: "test/support/test_data_helpers.rb", Receiver: extract.ReceiverInstance},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Unknown#build_widget",
		Kind:           model.EdgeCalls,
		SourceFileID:   20,
		BaseConfidence: extract.ConfidenceDynamic,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected test->test resolution to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
}

func TestResolveBareGuessRoutedThroughFallbackDemotes(t *testing.T) {
	// A bare receiver-unknown call (`x.count`, emitted bare at ConfidenceUnresolved)
	// coincidentally exact-matches a symbol qualified exactly `count`. The bare
	// guess must skip the exact shortcut and go through the gated fallback, which
	// demotes it below blast's floor rather than returning the exact match at 0.5.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Client#post", FileID: 10, Language: "ruby", Path: "app/client.rb"},
		{ID: 2, Qualified: "count", FileID: 11, Language: "ruby", Path: "app/helpers.rb"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "count",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceUnresolved,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected fallback resolution to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (bare guess routed through gated fallback)", r.Confidence)
	}
}

func TestResolveBareGuessCrossLanguageDropped(t *testing.T) {
	// The dominant real-world case: a Ruby production `x.count` (bare, receiver
	// unknown) whose only same-named symbol is a JS spec constant. Routed through
	// the fallback, the language gate drops it and the edge stays unresolved.
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Client#post", FileID: 10, Language: "ruby", Path: "app/client.rb"},
		{ID: 2, Qualified: "count", FileID: 60, Language: "javascript", Path: "test/system/specs/x.spec.js"},
	}
	ix := resolve.NewIndex(rs)
	if _, ok := ix.Resolve(resolve.Request{
		Target:         "count",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: extract.ConfidenceUnresolved,
	}); ok {
		t.Error("expected no resolution: bare Ruby guess must not bind a JS spec constant")
	}
}

func TestResolveCrossNamespaceColonColonDemotedBelowFloor(t *testing.T) {
	// `Stripe::Checkout::Session` misses exact lookup; only its leaf `Session`
	// matches an unrelated `User::Session`. Binding the leaf while discarding an
	// unverified namespace is a guess, so it lands below blast's 0.5 floor even
	// as a single match (it still counts for dead-code liveness).
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "StripeClient#charge", FileID: 10, Language: "ruby"},
		{ID: 2, Qualified: "User::Session", FileID: 11, Language: "ruby"},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Stripe::Checkout::Session",
		Kind:           model.EdgeCalls,
		SourceFileID:   10,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 2 {
		t.Fatalf("expected leaf fallback to id 2, got id=%d ok=%v", r.SymbolID, ok)
	}
	if r.Confidence >= 0.5 {
		t.Errorf("Confidence = %v, want < 0.5 (cross-namespace :: guess below blast floor)", r.Confidence)
	}
	if r.Ambiguous {
		t.Error("single match should not be flagged ambiguous")
	}
}

// The four tests below are the fixture-first gate for the namespaced-inheritance
// fix. The extractor emits an inherits edge with the bare ancestor "Base" (see
// ruby.TestRelativeSuperclassEmitsBareAncestorName); the resolver must resolve it
// by walking the source's enclosing scopes outward, mirroring Ruby/Rust constant
// lookup. Collision-free: a single Base, a single Widget per case.

// TestResolveInheritsRelativeAncestorViaLexicalScope — RED until resolveLexical
// lands. A subclass nested in Shop::Items names its superclass relatively
// (`< Base`); the real base is Shop::Base one level up. Resolution must try
// Shop::Items::Base then Shop::Base and bind the first that exists.
func TestResolveInheritsRelativeAncestorViaLexicalScope(t *testing.T) {
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Shop::Base", FileID: 1},
		{ID: 2, Qualified: "Shop::Items::Widget", FileID: 1},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:                "Base",
		Kind:                  model.EdgeInherits,
		SourceFileID:          1,
		SourceQualified:       "Shop::Items::Widget",
		SourceParentQualified: "Shop::Items",
		BaseConfidence:        1.0,
	})
	if !ok || r.SymbolID != 1 {
		t.Fatalf("relative ancestor should resolve to Shop::Base (id 1) via lexical scope, got ok=%v id=%d", ok, r.SymbolID)
	}
	if r.Confidence != 1.0 {
		t.Errorf("unique nesting hit should keep full confidence, got %v", r.Confidence)
	}
}

// TestResolveInheritsRelativeAncestorInnermostWins — RED until the fix. When a
// matching constant exists at more than one enclosing level, Ruby binds the
// innermost; resolution must too.
func TestResolveInheritsRelativeAncestorInnermostWins(t *testing.T) {
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Shop::Base", FileID: 1},
		{ID: 3, Qualified: "Shop::Items::Base", FileID: 1},
		{ID: 2, Qualified: "Shop::Items::Widget", FileID: 1},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:                "Base",
		Kind:                  model.EdgeInherits,
		SourceFileID:          1,
		SourceQualified:       "Shop::Items::Widget",
		SourceParentQualified: "Shop::Items",
		BaseConfidence:        1.0,
	})
	if !ok || r.SymbolID != 3 {
		t.Fatalf("innermost Shop::Items::Base (id 3) should win, got ok=%v id=%d", ok, r.SymbolID)
	}
}

// TestResolveInheritsRelativeAncestorNoCandidateDropped — precision guard (green
// today and after the fix). A relative ancestor with no matching symbol at ANY
// enclosing level must NOT resolve: a known gap beats a fabricated base.
func TestResolveInheritsRelativeAncestorNoCandidateDropped(t *testing.T) {
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Shop::Base", FileID: 1},
		{ID: 2, Qualified: "Shop::Items::Widget", FileID: 1},
	}
	ix := resolve.NewIndex(rs)
	if _, ok := ix.Resolve(resolve.Request{
		Target:                "Nonexistent",
		Kind:                  model.EdgeInherits,
		SourceFileID:          1,
		SourceQualified:       "Shop::Items::Widget",
		SourceParentQualified: "Shop::Items",
		BaseConfidence:        1.0,
	}); ok {
		t.Error("a relative ancestor with no matching symbol must not resolve")
	}
}

// TestResolveInheritsQualifiedAncestorExactMatch — no-regression: a fully-
// qualified ancestor still resolves via the existing exact path at full
// confidence (this is why `< Spree::Base` already works).
func TestResolveInheritsQualifiedAncestorExactMatch(t *testing.T) {
	rs := []model.SymbolRef{
		{ID: 1, Qualified: "Shop::Base", FileID: 1},
		{ID: 2, Qualified: "Shop::Items::Widget", FileID: 1},
	}
	ix := resolve.NewIndex(rs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "Shop::Base",
		Kind:           model.EdgeInherits,
		SourceFileID:   1,
		BaseConfidence: 1.0,
	})
	if !ok || r.SymbolID != 1 {
		t.Errorf("qualified ancestor should resolve to id 1, got ok=%v id=%d", ok, r.SymbolID)
	}
}
