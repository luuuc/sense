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

	got := a.Decide([]Symbol{priv, pub}, Facts{})
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
