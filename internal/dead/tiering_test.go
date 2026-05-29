package dead

import "testing"

// Ruby classes, modules, and constants are reachable through dynamic
// paths the indexer cannot see (const_get, constantize, autoloading),
// so a dead candidate of those kinds is tiered possibly_dead, not dead.
func TestRubyDynamicTypesArePossiblyDead(t *testing.T) {
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
		got := annotateConfidence([]Symbol{{Language: "ruby", Kind: c.kind, Name: "X"}}, nil, nil)
		if got[0].Confidence != c.want {
			t.Errorf("ruby %s confidence = %q, want %q", c.kind, got[0].Confidence, c.want)
		}
	}
	// Non-Ruby types keep the plain dead tier.
	got := annotateConfidence([]Symbol{{Language: "python", Kind: "class", Name: "X"}}, nil, nil)
	if got[0].Confidence != ConfidenceDead {
		t.Errorf("python class confidence = %q, want %q", got[0].Confidence, ConfidenceDead)
	}
}

// Go constructors remain possibly_dead — guards the branch adjacent to
// the Ruby dynamic-type tiering so annotateConfidence stays exercised.
func TestGoConstructorPossiblyDead(t *testing.T) {
	got := annotateConfidence([]Symbol{{Language: "go", Kind: "function", Name: "NewThing"}}, nil, nil)
	if got[0].Confidence != ConfidencePossibly {
		t.Errorf("go constructor confidence = %q, want %q", got[0].Confidence, ConfidencePossibly)
	}
}

// Interface methods and trait/interface-implementor methods are tiered
// possibly_dead — covers the two ParentID branches in annotateConfidence.
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
		got := annotateConfidence([]Symbol{c.sym}, interfaceIDs, implementorIDs)
		if got[0].Confidence != ConfidencePossibly {
			t.Errorf("%s: confidence = %q, want %q", c.name, got[0].Confidence, ConfidencePossibly)
		}
	}
}
