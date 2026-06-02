package dead

import "testing"

func railsFacts() Facts {
	return Facts{Frameworks: map[string]struct{}{"Rails": {}}}
}

func TestRailsVoiceGatedOnRails(t *testing.T) {
	v := railsVoice{}
	// Not a Rails project → silent for everything, even a controller action.
	s := rubySym("index", "OrdersController#index", "method", "public", id(1))
	assertReason(t, v, s, Facts{}, "")
}

func TestRailsVoiceControllerClass(t *testing.T) {
	v := railsVoice{}
	s := rubySym("OrdersController", "OrdersController", "class", "public", nil)
	assertReason(t, v, s, railsFacts(), ReasonRailsRouting)
}

func TestRailsVoiceControllerAction(t *testing.T) {
	v := railsVoice{}
	s := rubySym("index", "OrdersController#index", "method", "public", id(1))
	assertReason(t, v, s, railsFacts(), ReasonRailsRouting)
}

func TestRailsVoiceCallbackName(t *testing.T) {
	v := railsVoice{}
	// A method named like a Rails callback → rails_callback.
	s := rubySym("after_commit", "Order#after_commit", "method", "public", id(1))
	assertReason(t, v, s, railsFacts(), ReasonRailsCallback)
}

func TestRailsVoiceConcernMethod(t *testing.T) {
	v := railsVoice{}
	concerns := railsFacts()
	concerns.ControllerConcernIDs = map[int64]struct{}{5: {}}
	// A method on a module mixed into a *Controller → rails_concern.
	s := rubySym("authorize", "Authorizable#authorize", "method", "public", id(5))
	assertReason(t, v, s, concerns, ReasonRailsConcern)
}

func TestRailsVoiceOrdinaryMethodSilent(t *testing.T) {
	v := railsVoice{}
	// A plain model method is not the Rails voice's concern (the Ruby voice
	// handles it); the Rails voice stays silent.
	s := rubySym("total", "Order#total", "method", "public", id(1))
	assertReason(t, v, s, railsFacts(), "")
}

func TestIsRailsCallbackName(t *testing.T) {
	if !isRailsCallbackName(rubySym("before_action", "X#before_action", "method", "public", nil)) {
		t.Error("before_action should be a callback name")
	}
	if !isRailsCallbackName(rubySym("after_commit", "X#after_commit", "method", "public", nil)) {
		t.Error("after_commit should be a callback name (railsHooks)")
	}
	if isRailsCallbackName(rubySym("total", "X#total", "method", "public", nil)) {
		t.Error("total should not be a callback name")
	}
	if isRailsCallbackName(rubySym("before_action", "X", "class", "public", nil)) {
		t.Error("a non-method should never be a callback name")
	}
}

func TestRailsVoiceLang(t *testing.T) {
	if (railsVoice{}).Lang() != "ruby" {
		t.Errorf("railsVoice.Lang() = %q, want ruby", (railsVoice{}).Lang())
	}
}

// TestRubyRailsVoicesTogetherInArbiter proves the two voices compose: under
// Rails, a controller action gets the lower-priority (more-likely-live)
// rails_routing reason rather than the ruby_public_method catch-all, because
// the arbiter picks the most-likely-live reason across all voices.
func TestRubyRailsVoicesTogetherInArbiter(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	action := rubySym("index", "OrdersController#index", "method", "public", id(1))

	got := a.Decide([]Symbol{action}, railsFacts())
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("controller action verdict = %q, want possibly_dead", got[0].Verdict)
	}
	// rails_routing (priority 20) is more-likely-live than ruby_public_method
	// (60), so the arbiter's lowest-priority pick is rails_routing.
	if got[0].Reason.Code != ReasonRailsRouting {
		t.Errorf("controller action reason = %q, want %q (most-likely-live wins)",
			got[0].Reason.Code, ReasonRailsRouting)
	}
}

// TestArbiterEarnedDeadForPrivate proves the two-sided gate end to end with
// the real voices: a non-special private Ruby method earns `dead`, while a
// public one stays possibly_dead.
func TestArbiterEarnedDeadForPrivate(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("orphan", "Report#orphan", "method", "private", id(1))
	pub := rubySym("render", "Report#render", "method", "public", id(1))

	// The soundness gate needs a non-empty Ruby mention set that does not contain
	// "orphan" to prove the private method is mentioned nowhere a caller could be.
	f := Facts{MentionedNames: langNames("ruby", "render"), HarvestedLangs: harvested("ruby")}
	got := a.Decide([]Symbol{priv, pub}, f)
	if got[0].Verdict != VerdictDead {
		t.Errorf("private orphan verdict = %q, want dead", got[0].Verdict)
	}
	if got[1].Verdict != VerdictPossiblyDead {
		t.Errorf("public render verdict = %q, want possibly_dead", got[1].Verdict)
	}
}

// TestArbiterReflectionKeepsPrivateAlive: a private method whose NAME is a
// reflective dispatch target is kept possibly_dead by the core voice, even
// though the ruby voice would let it earn dead.
func TestArbiterReflectionKeepsPrivateAlive(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("handler", "Dispatcher#handler", "method", "private", id(1))
	f := Facts{DispatchNames: langNames("ruby", "handler")}

	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("reflectively-dispatched private verdict = %q, want possibly_dead", got[0].Verdict)
	}
	if got[0].Reason.Code != ReasonReflection {
		t.Errorf("reason = %q, want %q", got[0].Reason.Code, ReasonReflection)
	}
}

// TestSoundnessGateMentionedNameStaysPossiblyDead proves the soundness gate:
// a private method no voice flags, which would otherwise earn `dead`, stays
// possibly_dead when its bare name is mentioned somewhere the resolver could
// not bind (an inherited bare call, a `**splat`, a chain receiver, a
// `validate :sym` symbol arg). This is the maket false-dead class (a live
// `validate :amount_meets_minimum_threshold` predicate) made into a unit.
func TestSoundnessGateMentionedNameStaysPossiblyDead(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("amount_meets_minimum_threshold",
		"PayoutRequestForm#amount_meets_minimum_threshold", "method", "private", id(1))

	// The name is mentioned (as a `validate :sym` symbol arg the resolver did
	// not bind to a caller edge), so the gate keeps it open-world.
	f := Facts{
		MentionedNames: langNames("ruby", "amount_meets_minimum_threshold", "other"),
		HarvestedLangs: harvested("ruby"),
	}
	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("mentioned private verdict = %q, want possibly_dead", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}

// TestSoundnessGateIsPerLanguage is the cross-language soundness control: the
// invariant 25-15 exists to guarantee. With a Ruby voice AND a Go voice both
// registered but only Ruby's mention harvest run, a private Ruby method earns
// `dead` while a Go symbol of the SAME NAME stays possibly_dead with
// core_no_harvest. This proves a future language voice can never earn `dead`
// off another language's mentions: it must ship its own harvest (register in
// HarvestedLangs) first. The Go voice is registered precisely so the Go symbol
// clears the no-language-voice gate and actually reaches the soundness gate —
// the exact scenario the per-language keying must make safe.
func TestSoundnessGateIsPerLanguage(t *testing.T) {
	goVoice := fakeVoice{lang: "go"} // stand-in for a future voice; raises nothing
	a := NewArbiter(coreVoice{}, rubyVoice{}, goVoice)

	rubyPriv := rubySym("orphan", "Report#orphan", "method", "private", id(1))
	goSym := sym("orphan", "go") // same bare name, different language

	// Only Ruby harvested; its set does not contain "orphan".
	f := Facts{
		MentionedNames: langNames("ruby", "unrelated"),
		HarvestedLangs: harvested("ruby"),
	}
	got := a.Decide([]Symbol{rubyPriv, goSym}, f)

	if got[0].Verdict != VerdictDead {
		t.Fatalf("ruby private verdict = %q, want dead (own harvest, name absent)", got[0].Verdict)
	}
	if got[1].Verdict != VerdictPossiblyDead {
		t.Fatalf("go symbol earned %q off Ruby mentions — a new voice must harvest "+
			"its own names before earning dead", got[1].Verdict)
	}
	if got[1].Reason == nil || got[1].Reason.Code != ReasonNoHarvest {
		t.Errorf("go symbol reason = %+v, want %q", got[1].Reason, ReasonNoHarvest)
	}
}

// TestSoundnessGateDemotesNameCollision pins the recall trade-off: the broad
// mention gate is name-based, so a genuinely-dead private method whose name
// COLLIDES with any unrelated token used elsewhere is demoted to possibly_dead.
// This is intentional — the safe direction for a trust feature — and pinned
// here so it is a designed property, not an accident.
func TestSoundnessGateDemotesNameCollision(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("process", "Worker#process", "method", "private", id(1))
	// `process` is mentioned elsewhere (an unrelated local/call). The gate cannot
	// tell it apart from Worker#process, so it stays cautious.
	f := Facts{MentionedNames: langNames("ruby", "process", "other"), HarvestedLangs: harvested("ruby")}

	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("name-collision verdict = %q, want possibly_dead (recall trade-off)", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}

// TestSoundnessGateNoHarvestFailsClosed proves the fail-closed default for an
// unavailable harvest: when the symbol's language never harvested mentions (a
// pre-feature index, corrupt meta, or a voice that did not ship its harvest),
// the gate has no set to prove the name unmentioned, so it refuses `dead` with
// core_no_harvest rather than risk re-admitting a false one.
func TestSoundnessGateNoHarvestFailsClosed(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("orphan", "Report#orphan", "method", "private", id(1))

	got := a.Decide([]Symbol{priv}, Facts{}) // no harvest for ruby
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("no-harvest verdict = %q, want possibly_dead (fail-closed)", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNoHarvest {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNoHarvest)
	}
}

// TestSoundnessGateHarvestedEmptySetFailsClosed proves the second fail-closed
// branch: the language DID harvest (it is in HarvestedLangs) but produced an
// empty set, so the gate still cannot prove the name unmentioned and refuses
// `dead` with core_name_mentioned. Harvest-ran and set-non-empty are distinct
// preconditions; both must hold to earn `dead`.
func TestSoundnessGateHarvestedEmptySetFailsClosed(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("orphan", "Report#orphan", "method", "private", id(1))

	f := Facts{HarvestedLangs: harvested("ruby")} // harvested, but empty mention set
	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("harvested-empty verdict = %q, want possibly_dead (fail-closed)", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}
