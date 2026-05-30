package dead

import "testing"

func rubySym(name, qualified, kind, visibility string, parentID *int64) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Language:   "ruby",
		Visibility: visibility,
		ParentID:   parentID,
	}
}

func id(v int64) *int64 { return &v }

// assertReason runs a voice and checks the raised reason code (or nil).
func assertReason(t *testing.T, v Voice, s Symbol, f Facts, wantCode string) {
	t.Helper()
	got := v.Inspect(s, f)
	if wantCode == "" {
		if got != nil {
			t.Errorf("%s: got reason %q, want silent (nil)", s.Qualified, got.Code)
		}
		return
	}
	if got == nil {
		t.Errorf("%s: got silent, want reason %q", s.Qualified, wantCode)
		return
	}
	if got.Code != wantCode {
		t.Errorf("%s: got reason %q, want %q", s.Qualified, got.Code, wantCode)
	}
	if got.Hint == "" {
		t.Errorf("%s: reason %q has empty hint", s.Qualified, got.Code)
	}
}

func TestRubyVoiceClassModuleConstant(t *testing.T) {
	v := rubyVoice{}
	assertReason(t, v, rubySym("Foo", "Foo", "class", "public", nil), Facts{}, ReasonRubyClass)
	assertReason(t, v, rubySym("Bar", "Bar", "module", "public", nil), Facts{}, ReasonRubyModule)
	assertReason(t, v, rubySym("MAX", "MAX", "constant", "public", nil), Facts{}, ReasonRubyConstant)
}

func TestRubyVoicePublicMethodRaises(t *testing.T) {
	v := rubyVoice{}
	// Public instance method → ruby_public_method.
	assertReason(t, v, rubySym("pending?", "Order#pending?", "method", "public", id(1)), Facts{}, ReasonRubyPublicMethod)
	// Visibility-unknown (empty) defaults to public-treatment.
	assertReason(t, v, rubySym("process", "Order#process", "method", "", id(1)), Facts{}, ReasonRubyPublicMethod)
}

func TestRubyVoicePrivateMethodSilent(t *testing.T) {
	v := rubyVoice{}
	// Private method with no special reach → silent → may earn dead.
	assertReason(t, v, rubySym("orphan", "Order#orphan", "method", "private", id(1)), Facts{}, "")
	assertReason(t, v, rubySym("hidden", "Order#hidden", "method", "protected", id(1)), Facts{}, "")
}

func TestRubyVoiceValueObjectMethod(t *testing.T) {
	v := rubyVoice{}
	vo := map[int64]struct{}{7: {}}
	// Instance method (#) on a value-object class → ruby_value_object,
	// regardless of visibility (it's a duck-typed surface).
	s := rubySym("success?", "PaymentResult#success?", "method", "private", id(7))
	assertReason(t, v, s, Facts{ValueObjectClassIDs: vo}, ReasonRubyValueObject)
}

func TestRubyVoiceValueObjectSingletonNotMatched(t *testing.T) {
	v := rubyVoice{}
	vo := map[int64]struct{}{7: {}}
	// A singleton method (.) on a value-object class is not the duck-typed
	// instance surface; a public one falls to ruby_public_method.
	s := rubySym("build", "PaymentResult.build", "method", "public", id(7))
	assertReason(t, v, s, Facts{ValueObjectClassIDs: vo}, ReasonRubyPublicMethod)
}

func TestRubyVoiceServiceCall(t *testing.T) {
	v := rubyVoice{}
	s := rubySym("call", "NotifyUserService#call", "method", "public", id(1))
	assertReason(t, v, s, Facts{}, ReasonRubyServiceCall)

	// A `call` on a non-service class is just a public method.
	plain := rubySym("call", "Widget#call", "method", "public", id(1))
	assertReason(t, v, plain, Facts{}, ReasonRubyPublicMethod)
}

func TestRubyVoiceModuleMixin(t *testing.T) {
	v := rubyVoice{}
	inc := map[int64]struct{}{3: {}}
	// A private method on a module included somewhere → ruby_module_mixin
	// (more specific than the public catch-all, and it overrides private
	// silence because the mixin makes it reachable).
	s := rubySym("helper", "Auditable#helper", "method", "private", id(3))
	assertReason(t, v, s, Facts{IncludedModuleIDs: inc}, ReasonRubyModuleMixin)
}

func TestRubyVoiceServiceCallBeatsModuleMixin(t *testing.T) {
	v := rubyVoice{}
	inc := map[int64]struct{}{3: {}}
	// `call` on a Service that is also an included module: service-call is
	// checked first (most specific entry-point semantics).
	s := rubySym("call", "PaymentService#call", "method", "public", id(3))
	assertReason(t, v, s, Facts{IncludedModuleIDs: inc}, ReasonRubyServiceCall)
}

func TestIsServiceCall(t *testing.T) {
	if !isServiceCall(rubySym("call", "FooService#call", "method", "public", nil)) {
		t.Error("FooService#call should be a service call")
	}
	if isServiceCall(rubySym("call", "Foo#call", "method", "public", nil)) {
		t.Error("Foo#call (no service suffix) should not be a service call")
	}
	if isServiceCall(rubySym("run", "FooService#run", "method", "public", nil)) {
		t.Error("FooService#run (not 'call') should not be a service call")
	}
}

func TestRubyVoiceLang(t *testing.T) {
	if (rubyVoice{}).Lang() != "ruby" {
		t.Errorf("rubyVoice.Lang() = %q, want ruby", (rubyVoice{}).Lang())
	}
}

// TestRubyVoiceNonMethodKindFallthrough covers a kind the switch does not
// special-case (e.g. a function), which the voice ignores.
func TestRubyVoiceNonMethodKindFallthrough(t *testing.T) {
	v := rubyVoice{}
	assertReason(t, v, rubySym("top_level", "top_level", "function", "public", nil), Facts{}, "")
}
