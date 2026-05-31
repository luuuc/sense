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

	// The soundness gate needs a non-empty mention set that does not contain
	// "orphan" to prove the private method is mentioned nowhere a caller could be.
	f := Facts{MentionedNames: map[string]struct{}{"render": {}}}
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
	f := Facts{DispatchNames: map[string]struct{}{"handler": {}}}

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
	f := Facts{MentionedNames: map[string]struct{}{
		"amount_meets_minimum_threshold": {},
		"other":                          {},
	}}
	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("mentioned private verdict = %q, want possibly_dead", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}

// TestSoundnessGateIsRubyOnlyToday is the per-language tripwire. The mention set
// is project-GLOBAL, but only Ruby has both a voice and a mention harvest today,
// so only Ruby symbols may earn `dead`. A non-Ruby symbol must NOT earn `dead`
// off the global (Ruby-derived) mention set, even when its name is absent from
// that set. This assertion fails the day a second language voice is registered
// without its own mention harvest — at which point that voice's symbols would
// start earning `dead` here off another language's mentions, forcing the
// harvest question. See decideOne's per-language NOTE.
func TestSoundnessGateIsRubyOnlyToday(t *testing.T) {
	a := defaultArbiter()
	goSym := sym("UnusedGoFunc", "go")
	// Populated mention set that does NOT contain the Go symbol's name.
	f := Facts{MentionedNames: map[string]struct{}{"some_ruby_name": {}}}

	got := a.Decide([]Symbol{goSym}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("non-Ruby symbol earned %q off the global mention set — a new "+
			"language voice needs its own mention harvest before earning dead", got[0].Verdict)
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
	f := Facts{MentionedNames: map[string]struct{}{"process": {}, "other": {}}}

	got := a.Decide([]Symbol{priv}, f)
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("name-collision verdict = %q, want possibly_dead (recall trade-off)", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}

// TestSoundnessGateEmptySetFailsClosed proves the fail-closed default: when the
// mention harvest is unavailable (an empty set — a pre-feature index or corrupt
// meta), the gate cannot prove the name is unmentioned, so it refuses `dead`
// rather than risk re-admitting a false one.
func TestSoundnessGateEmptySetFailsClosed(t *testing.T) {
	a := NewArbiter(coreVoice{}, rubyVoice{}, railsVoice{})
	priv := rubySym("orphan", "Report#orphan", "method", "private", id(1))

	got := a.Decide([]Symbol{priv}, Facts{}) // no MentionedNames
	if got[0].Verdict != VerdictPossiblyDead {
		t.Fatalf("empty-mention-set verdict = %q, want possibly_dead (fail-closed)", got[0].Verdict)
	}
	if got[0].Reason == nil || got[0].Reason.Code != ReasonNameMentioned {
		t.Errorf("reason = %+v, want %q", got[0].Reason, ReasonNameMentioned)
	}
}
