package dead

import "testing"

// fakeVoice is a test voice that raises a fixed reason for symbols whose
// name is in raiseFor, and serves the language lang ("" = core).
type fakeVoice struct {
	lang     string
	code     string
	raiseFor map[string]struct{}
}

func (v fakeVoice) Lang() string { return v.lang }

func (v fakeVoice) Inspect(s Symbol, _ Facts) *Reason {
	if _, ok := v.raiseFor[s.Name]; ok {
		return &Reason{Code: v.code, Hint: "test hint"}
	}
	return nil
}

func sym(name, lang string) Symbol {
	return Symbol{Name: name, Qualified: name, Language: lang, Kind: "method"}
}

// TestArbiterTwoSidedGate is the control the coverage gate demands: with a
// language voice registered, a symbol no voice flags earns `dead`, AND a
// symbol the voice flags stays `possibly_dead`. The gate is two-sided, not a
// blanket mute or a blanket soften.
func TestArbiterTwoSidedGate(t *testing.T) {
	voice := fakeVoice{lang: "ruby", code: ReasonReflection, raiseFor: map[string]struct{}{"flagged": {}}}
	a := NewArbiter(voice)

	// A non-empty mention set that does NOT contain "clean" lets the soundness
	// gate prove "clean" is mentioned nowhere a hidden caller could be.
	f := Facts{MentionedNames: map[string]struct{}{"flagged": {}}}
	got := a.Decide([]Symbol{sym("flagged", "ruby"), sym("clean", "ruby")}, f)

	if got[0].Verdict != VerdictPossiblyDead {
		t.Errorf("flagged symbol: got %q, want possibly_dead", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonReflection {
		t.Errorf("flagged symbol reason = %+v, want code %q", got[0].Reason, ReasonReflection)
	}
	if got[1].Verdict != VerdictDead {
		t.Errorf("clean symbol: got %q, want dead (closed-world proven)", got[1].Verdict)
	}
	if got[1].Reason != nil {
		t.Errorf("dead symbol must carry no reason, got %+v", got[1].Reason)
	}
}

// TestArbiterNoLanguageVoiceNeverDead proves the safety invariant: a symbol
// whose language has no registered voice is always possibly_dead with
// core_no_language_voice — `dead` is never emitted on an unsupported stack,
// even when no voice raises a hand.
func TestArbiterNoLanguageVoiceNeverDead(t *testing.T) {
	// A ruby voice is registered, but the symbol is Go.
	a := NewArbiter(fakeVoice{lang: "ruby", code: ReasonReflection})

	got := a.Decide([]Symbol{sym("GoThing", "go")}, Facts{})

	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("unsupported-stack symbol: got %q, want possibly_dead", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNoLanguageVoice {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNoLanguageVoice)
	}
}

// TestArbiterEmptyRegistryAllPossiblyDead: with no language voices at all,
// every symbol is possibly_dead (the Card 2 outcome before any voice lands).
func TestArbiterEmptyRegistryAllPossiblyDead(t *testing.T) {
	a := NewArbiter(coreVoice{})
	got := a.Decide([]Symbol{sym("A", "ruby"), sym("B", "go")}, Facts{})
	for _, f := range got {
		if f.Verdict != VerdictPossiblyDead {
			t.Errorf("%s: got %q, want possibly_dead (no language voice registered)", f.Symbol.Name, f.Verdict)
		}
		if f.Reason == nil || f.Reason.Code != ReasonNoLanguageVoice {
			t.Errorf("%s: reason = %+v, want %q", f.Symbol.Name, f.Reason, ReasonNoLanguageVoice)
		}
	}
}

// TestArbiterPicksMostLikelyLiveReason: when multiple voices raise hands, the
// reason with the lowest removability priority (most likely live) wins, since
// its verify recipe is the check most likely to find a hidden caller.
func TestArbiterPicksMostLikelyLiveReason(t *testing.T) {
	// ReasonExportedAPI priority 50; ReasonReflection priority 30 (lower → wins).
	high := fakeVoice{lang: "ruby", code: ReasonExportedAPI, raiseFor: map[string]struct{}{"X": {}}}
	low := fakeVoice{lang: "", code: ReasonReflection, raiseFor: map[string]struct{}{"X": {}}}
	a := NewArbiter(high, low)

	got := a.Decide([]Symbol{sym("X", "ruby")}, Facts{})
	if got[0].Reason == nil || got[0].Reason.Code != ReasonReflection {
		t.Errorf("reason = %+v, want lowest-priority %q to win", got[0].Reason, ReasonReflection)
	}
}

// TestArbiterTieBreakFirstRegistered: equal-priority reasons resolve to the
// first registered voice, for deterministic output.
func TestArbiterTieBreakFirstRegistered(t *testing.T) {
	first := fakeVoice{lang: "ruby", code: "ruby_a", raiseFor: map[string]struct{}{"X": {}}}
	second := fakeVoice{lang: "ruby", code: "ruby_b", raiseFor: map[string]struct{}{"X": {}}}
	// Both unknown codes → priority 0 (tie).
	a := NewArbiter(first, second)
	got := a.Decide([]Symbol{sym("X", "ruby")}, Facts{})
	if got[0].Reason.Code != "ruby_a" {
		t.Errorf("tie-break: got %q, want first-registered ruby_a", got[0].Reason.Code)
	}
}

func TestCoreVoiceReflectionGate(t *testing.T) {
	f := Facts{DispatchNames: map[string]struct{}{"process": {}}}
	r := coreVoice{}.Inspect(Symbol{Name: "process", Kind: "method", Language: "ruby"}, f)
	if r == nil || r.Code != ReasonReflection {
		t.Errorf("reflection gate: got %+v, want %q", r, ReasonReflection)
	}
}

func TestCoreVoiceExportGate(t *testing.T) {
	f := Facts{IsLibrary: true}
	pub := Symbol{Name: "Charge", Kind: "function", Visibility: "public", Language: "go"}
	if r := (coreVoice{}).Inspect(pub, f); r == nil || r.Code != ReasonExportedAPI {
		t.Errorf("export gate (public lib func): got %+v, want %q", r, ReasonExportedAPI)
	}
	// Not a library → export gate silent.
	if r := (coreVoice{}).Inspect(pub, Facts{}); r != nil {
		t.Errorf("export gate should be silent for a non-library: got %+v", r)
	}
	// Private symbol → export gate silent even in a library.
	priv := Symbol{Name: "helper", Kind: "method", Visibility: "private", Language: "go"}
	if r := (coreVoice{}).Inspect(priv, f); r != nil {
		t.Errorf("export gate should be silent for a private symbol: got %+v", r)
	}
	// Constant → not API surface.
	cst := Symbol{Name: "MAX", Kind: "constant", Visibility: "public", Language: "go"}
	if r := (coreVoice{}).Inspect(cst, f); r != nil {
		t.Errorf("export gate should be silent for a constant: got %+v", r)
	}
}

func TestCoreVoiceReflectionBeatsExport(t *testing.T) {
	// A symbol that is both a dispatch target and public-in-a-library raises
	// reflection (the gate checked first / more specific).
	f := Facts{IsLibrary: true, DispatchNames: map[string]struct{}{"Charge": {}}}
	pub := Symbol{Name: "Charge", Kind: "function", Visibility: "public", Language: "go"}
	if r := (coreVoice{}).Inspect(pub, f); r == nil || r.Code != ReasonReflection {
		t.Errorf("got %+v, want reflection to win", r)
	}
}

// TestFrameworkAppMethodGetsLanguageReasonNotExportedAPI pins the behavior the
// IsLibrary = !hasMain && len(frameworks)==0 fix exists for, through the real
// voice stack rather than fakes. In a framework application IsLibrary is false,
// so the core voice stays silent and an app-internal public Ruby method earns
// the accurate ruby_public_method reason — not core_exported_api ("search
// dependent projects"), which would mislead a triager. The contrast case (a
// genuine library, IsLibrary=true) still surfaces core_exported_api, proving
// the fix corrected the premise rather than the 50-vs-60 priority ladder: the
// export reason only wins when its premise actually holds.
func TestFrameworkAppMethodGetsLanguageReasonNotExportedAPI(t *testing.T) {
	a := defaultArbiter()
	// A public Ruby instance method with no dynamic-dispatch / mixin signal —
	// the rubyVoice catch-all is ruby_public_method. Visibility must be public
	// for the export gate to be a candidate at all, so the test proves the
	// IsLibrary premise (not visibility) is what silences it.
	method := Symbol{Name: "public_helper", Qualified: "Billing::Calculator#public_helper",
		Kind: "method", Visibility: "public", Language: "ruby"}

	// Framework app: IsLibrary false ⇒ core voice silent ⇒ ruby_public_method.
	app := a.Decide([]Symbol{method}, Facts{IsLibrary: false})[0]
	if app.Reason == nil || app.Reason.Code != ReasonRubyPublicMethod {
		t.Errorf("framework-app method: reason = %+v, want %s", app.Reason, ReasonRubyPublicMethod)
	}

	// Genuine library: IsLibrary true ⇒ core voice raises the higher-priority
	// export reason, which wins. Confirms the fix changed the premise, not the
	// priority ordering.
	lib := a.Decide([]Symbol{method}, Facts{IsLibrary: true})[0]
	if lib.Reason == nil || lib.Reason.Code != ReasonExportedAPI {
		t.Errorf("library method: reason = %+v, want %s", lib.Reason, ReasonExportedAPI)
	}
}

func TestReasonCatalogLookup(t *testing.T) {
	if reasonPriority(ReasonNoLanguageVoice) != 70 {
		t.Errorf("no_language_voice priority = %d, want 70", reasonPriority(ReasonNoLanguageVoice))
	}
	if reasonPriority("totally_unknown_code") != 0 {
		t.Errorf("unknown code priority = %d, want 0", reasonPriority("totally_unknown_code"))
	}
	r := newReason(ReasonExportedAPI)
	if r.Code != ReasonExportedAPI || r.Hint == "" {
		t.Errorf("newReason = %+v, want code+hint populated", r)
	}
}

func TestArbiterEmptyCandidates(t *testing.T) {
	a := NewArbiter(coreVoice{})
	if got := a.Decide(nil, Facts{}); len(got) != 0 {
		t.Errorf("Decide(nil) = %v, want empty", got)
	}
}
