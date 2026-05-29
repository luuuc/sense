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
		got := annotateConfidence([]Symbol{{Language: "ruby", Kind: c.kind, Name: "X"}}, nil, nil, true)
		if got[0].Confidence != c.want {
			t.Errorf("ruby %s (framework): confidence = %q, want %q", c.kind, got[0].Confidence, c.want)
		}
	}
	// Without a dynamic framework, an unreferenced Ruby class is plain dead.
	got := annotateConfidence([]Symbol{{Language: "ruby", Kind: "class", Name: "X"}}, nil, nil, false)
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("ruby class (no framework): confidence = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
	// Non-Ruby types are never tiered by this rule.
	got = annotateConfidence([]Symbol{{Language: "python", Kind: "class", Name: "X"}}, nil, nil, true)
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("python class: confidence = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
}

func TestGoConstructorPossiblyDead(t *testing.T) {
	got := annotateConfidence([]Symbol{{Language: "go", Kind: "function", Name: "NewThing"}}, nil, nil, false)
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
		got := annotateConfidence([]Symbol{c.sym}, interfaceIDs, implementorIDs, false)
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
