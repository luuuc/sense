package search

import "testing"

// TestDemoteNeverPromotes pins the score-domain guard: a multiplicative
// penalty must lower a positive score and must NEVER raise a score. The
// negative cases are the defensive ones — ×penalty on a negative value would
// move it toward zero (a promotion) and silently invert every demotion pass,
// which is exactly the bug a signed cross-encoder logit once caused.
func TestDemoteNeverPromotes(t *testing.T) {
	cases := []struct {
		name    string
		score   float64
		penalty float64
		want    float64
	}{
		{"positive demotes", 0.6, 0.5, 0.3},
		{"positive heavy penalty", 1.0, 0.2, 0.2},
		{"zero unchanged", 0, 0.5, 0},
		{"negative not promoted", -1.0, 0.5, -1.0},
		{"negative small penalty not promoted", -0.4, 0.8, -0.4},
	}
	for _, c := range cases {
		got := demote(c.score, c.penalty)
		if got != c.want {
			t.Errorf("%s: demote(%v, %v) = %v, want %v", c.name, c.score, c.penalty, got, c.want)
		}
		if got > c.score {
			t.Errorf("%s: penalty PROMOTED %v to %v", c.name, c.score, got)
		}
	}
}

// TestPenaltyPassesNeverPromoteNegative drives the guard through the real
// penalty passes: even if a signed score reaches them, the demotion must not
// raise it. Without demote, applyKindWeights' ×0.5 would lift -1.0 to -0.5.
func TestPenaltyPassesNeverPromoteNegative(t *testing.T) {
	kind := []Result{{SymbolID: 1, Name: "Mod", Kind: "module", Score: -1.0}}
	applyKindWeights(kind)
	if kind[0].Score > -1.0 {
		t.Errorf("applyKindWeights promoted a negative module score to %v", kind[0].Score)
	}

	// genericTokenPenalty on a keyword-only hit matching no specific term.
	gen := []Result{{SymbolID: 2, Name: "preventClose", Qualified: "ui.preventClose",
		Kind: "function", Source: SourceKeyword, Score: -1.0}}
	genericTokenPenalty(gen, map[string]struct{}{"listing": {}})
	if gen[0].Score > -1.0 {
		t.Errorf("genericTokenPenalty promoted a negative score to %v", gen[0].Score)
	}

	// applyTestDemotion on a test symbol.
	test := []Result{{SymbolID: 3, Name: "TestThing", FileID: 1, Score: -1.0}}
	applyTestDemotion(test, map[int64]string{1: "x_test.go"}, "find thing")
	if test[0].Score > -1.0 {
		t.Errorf("applyTestDemotion promoted a negative score to %v", test[0].Score)
	}
}
