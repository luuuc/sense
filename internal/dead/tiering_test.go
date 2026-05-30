package dead

import "testing"

// Ruby classes, modules, and constants are reachable through dynamic paths
// the indexer cannot see (const_get, constantize, autoloading, STI) — but
// only when the project actually uses a framework that relies on them.
func TestRubyDynamicTypesTieredUnderFramework(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"class", ConfidencePossibly},
		{"module", ConfidencePossibly},
		{"constant", ConfidencePossibly},
		{"method", ConfidenceDead},
	}
	for _, c := range cases {
		got := annotateConfidence([]Symbol{{Language: "ruby", Kind: c.kind, Name: "X"}}, confidenceInputs{dynamicFramework: true})
		if got[0].Confidence != c.want {
			t.Errorf("ruby %s (framework): confidence = %q, want %q", c.kind, got[0].Confidence, c.want)
		}
	}
	// Without a dynamic framework, an unreferenced Ruby class is plain dead.
	got := annotateConfidence([]Symbol{{Language: "ruby", Kind: "class", Name: "X"}}, confidenceInputs{dynamicFramework: false})
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("ruby class (no framework): confidence = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
	// Non-Ruby types are never tiered by this rule.
	got = annotateConfidence([]Symbol{{Language: "python", Kind: "class", Name: "X"}}, confidenceInputs{dynamicFramework: true})
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("python class: confidence = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
}

// Ruby service-object `call` methods follow a dynamic-dispatch convention
// the indexer under-resolves, so with no detected caller they are
// possibly-dead, not dead — but only under a dynamic framework.
func TestRubyDynamicServiceCallTiering(t *testing.T) {
	cases := []struct {
		name      string
		sym       Symbol
		framework bool
		want      string
	}{
		{
			"service call under framework",
			Symbol{Language: "ruby", Kind: "method", Name: "call", Qualified: "Checkout::ProcessPaymentService#call"},
			true, ConfidencePossibly,
		},
		{
			"plain call on a non-service class stays dead",
			Symbol{Language: "ruby", Kind: "method", Name: "call", Qualified: "Widget#call"},
			true, ConfidenceDead,
		},
		{
			"service call without a dynamic framework stays dead",
			Symbol{Language: "ruby", Kind: "method", Name: "call", Qualified: "Checkout::ProcessPaymentService#call"},
			false, ConfidenceDead,
		},
		{
			"non-ruby call is untouched",
			Symbol{Language: "go", Kind: "method", Name: "call", Qualified: "FooService#call"},
			true, ConfidenceDead,
		},
	}
	for _, c := range cases {
		got := annotateConfidence([]Symbol{c.sym}, confidenceInputs{dynamicFramework: c.framework})
		if got[0].Confidence != c.want {
			t.Errorf("%s: confidence = %q, want %q", c.name, got[0].Confidence, c.want)
		}
	}
}

// Value-object recognition replaces the old blanket "every `foo?` predicate
// is possibly-dead" net with a rule keyed on the synthetic Struct/Data
// inheritance edge. A predicate on a value-object class is soft; the SAME
// predicate name on an ordinary class — the control — stays hard dead, which
// proves the rule is targeted, not a blanket mute. This softening is a pure-
// Ruby idiom, so it does not require a dynamic framework.
func TestValueObjectMethodTiering(t *testing.T) {
	const voClassID, plainClassID = int64(42), int64(43)
	vo := map[int64]struct{}{voClassID: {}}
	voID, plainID := voClassID, plainClassID

	cases := []struct {
		name      string
		sym       Symbol
		framework bool
		want      string
	}{
		{
			"value-object predicate is soft (no framework needed)",
			Symbol{Language: "ruby", Kind: "method", Name: "success?", Qualified: "Checkout::ProcessPaymentService::Result#success?", ParentID: &voID},
			false, ConfidencePossibly,
		},
		{
			"value-object non-predicate instance method is soft",
			Symbol{Language: "ruby", Kind: "method", Name: "amount", Qualified: "Money::Amount#amount", ParentID: &voID},
			false, ConfidencePossibly,
		},
		{
			// CONTROL: identical predicate name, ordinary parent, no
			// framework → stays dead. (framework=false isolates the
			// value-object rule from the framework-gated predicate rule.)
			"predicate on an ordinary class stays dead",
			Symbol{Language: "ruby", Kind: "method", Name: "success?", Qualified: "Widget#success?", ParentID: &plainID},
			false, ConfidenceDead,
		},
		{
			// Singleton methods (`Result.build`) are not the duck-typed
			// instance surface, so the value-object rule does not soften them.
			"singleton value-object method stays dead",
			Symbol{Language: "ruby", Kind: "method", Name: "build", Qualified: "Result.build", ParentID: &voID},
			false, ConfidenceDead,
		},
	}
	for _, c := range cases {
		got := annotateConfidence([]Symbol{c.sym}, confidenceInputs{valueObjectClassIDs: vo, dynamicFramework: c.framework})
		if got[0].Confidence != c.want {
			t.Errorf("%s: confidence = %q, want %q", c.name, got[0].Confidence, c.want)
		}
	}
}

// Ruby predicate methods stay soft under a dynamic framework: maket
// validation showed they are pervasively invoked on duck-typed receivers
// the indexer cannot resolve (pending? had 28 live call sites yet zero
// static callers), so hard-flagging them dead is a false negative. Without
// a framework, a zero-caller predicate is plain dead.
func TestRubyPredicateSoftenedUnderFramework(t *testing.T) {
	pred := Symbol{Language: "ruby", Kind: "method", Name: "pending?", Qualified: "PaymentTransaction#pending?"}

	got := annotateConfidence([]Symbol{pred}, confidenceInputs{dynamicFramework: true})
	if got[0].Confidence != ConfidencePossibly {
		t.Errorf("predicate under framework = %q, want %q", got[0].Confidence, ConfidencePossibly)
	}

	got = annotateConfidence([]Symbol{pred}, confidenceInputs{dynamicFramework: false})
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("predicate without framework = %q, want %q", got[0].Confidence, ConfidenceDead)
	}

	// A non-predicate method is unaffected by this rule.
	plain := Symbol{Language: "ruby", Kind: "method", Name: "process", Qualified: "Widget#process"}
	got = annotateConfidence([]Symbol{plain}, confidenceInputs{dynamicFramework: true})
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("non-predicate under framework = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
}

func TestRubyMethodParentName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Checkout::ProcessPaymentService#call", "ProcessPaymentService"},
		{"A.b", "A"},
		{"Foo::Bar.baz", "Bar"},
		{"top_level", ""},
	}
	for _, c := range cases {
		if got := rubyMethodParentName(c.in); got != c.want {
			t.Errorf("rubyMethodParentName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGoConstructorPossiblyDead(t *testing.T) {
	got := annotateConfidence([]Symbol{{Language: "go", Kind: "function", Name: "NewThing"}}, confidenceInputs{dynamicFramework: false})
	if got[0].Confidence != ConfidencePossibly {
		t.Errorf("go constructor confidence = %q, want %q", got[0].Confidence, ConfidencePossibly)
	}
}

func TestInterfaceAndImplementorTiering(t *testing.T) {
	interfaceIDs := map[int64]struct{}{10: {}}
	implementorIDs := map[int64]struct{}{20: {}}
	pid10, pid20 := int64(10), int64(20)
	cases := []struct {
		name string
		sym  Symbol
	}{
		{"interface method", Symbol{Kind: "method", Name: "Do", ParentID: &pid10}},
		{"implementor method", Symbol{Kind: "method", Name: "Do", ParentID: &pid20}},
	}
	for _, c := range cases {
		got := annotateConfidence([]Symbol{c.sym}, confidenceInputs{interfaceIDs: interfaceIDs, implementorIDs: implementorIDs})
		if got[0].Confidence != ConfidencePossibly {
			t.Errorf("%s: confidence = %q, want %q", c.name, got[0].Confidence, ConfidencePossibly)
		}
	}
}

func TestUsesDynamicAutoload(t *testing.T) {
	if !usesDynamicAutoload(map[string]struct{}{"Rails": {}}) {
		t.Error("Rails should be treated as a dynamic-autoload framework")
	}
	if usesDynamicAutoload(map[string]struct{}{"Sinatra": {}}) {
		t.Error("non-Rails framework should not enable dynamic tiering")
	}
	if usesDynamicAutoload(nil) {
		t.Error("no frameworks should not enable dynamic tiering")
	}
}
