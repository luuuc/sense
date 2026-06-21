package mcpio

import (
	"context"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
)

func TestBlastCompletenessComplete(t *testing.T) {
	// total_affected counts the direct+indirect caller union. The inherit/
	// include/compose groups are subset views of those same callers, so they
	// must NOT inflate the enumerated count — only direct+indirect do.
	resp := &BlastResponse{
		DirectCallers:      []BlastCaller{{}, {}, {}, {}, {}},
		AffectedSubclasses: []BlastCaller{{}, {}}, // a view into DirectCallers, not extra
	}
	c := blastCompleteness(resp, 5) // 5 direct enumerated == total
	if c.Verdict != "complete" {
		t.Fatalf("verdict = %q, want complete", c.Verdict)
	}
	if c.Resolved != 5 {
		t.Errorf("resolved = %d, want 5 (direct+indirect only)", c.Resolved)
	}
	if c.Hidden != 0 {
		t.Errorf("hidden = %d, want 0", c.Hidden)
	}
	if !strings.Contains(c.Advice, "do not re-grep") {
		t.Errorf("advice missing stop signal: %q", c.Advice)
	}
}

// The kind groups must not be summed into the enumerated count — if they were,
// hidden would be understated and "complete" could mask dropped callers.
func TestBlastCompletenessIgnoresKindGroupsInCount(t *testing.T) {
	resp := &BlastResponse{
		DirectCallers:          []BlastCaller{{}, {}}, // only 2 callers enumerated
		AffectedSubclasses:     []BlastCaller{{}, {}}, // subset views — must not add
		AffectedViaIncludes:    []BlastCaller{{}},     // to the count
		AffectedViaComposition: []BlastCaller{{}},
	}
	c := blastCompleteness(resp, 6) // 2 callers vs 6 affected -> 4 hidden
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial (kind groups must not mask the 4 hidden)", c.Verdict)
	}
	if c.Hidden != 4 {
		t.Errorf("hidden = %d, want 4", c.Hidden)
	}
}

func TestBlastCompletenessPartialHidden(t *testing.T) {
	resp := &BlastResponse{DirectCallers: []BlastCaller{{}, {}}}
	c := blastCompleteness(resp, 5) // 2 enumerated, 5 affected -> 3 hidden
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial", c.Verdict)
	}
	if c.Hidden != 3 {
		t.Errorf("hidden = %d, want 3", c.Hidden)
	}
}

func TestBlastCompletenessPartialCapped(t *testing.T) {
	// Enumerated direct_callers are capped at directEnumCap, but the by-area
	// map records more direct callers than were enumerated. total ==
	// enumerated so `hidden` is 0, yet the response is partial because the
	// inline list does not show every direct caller.
	callers := make([]BlastCaller, directEnumCap)
	resp := &BlastResponse{
		DirectCallers:       callers,
		DirectCallersByArea: map[string]int{"app/models": 200},
	}
	c := blastCompleteness(resp, directEnumCap)
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial (enum cap hit, by-area carries the rest)", c.Verdict)
	}
}

func TestGraphCompletenessComplete(t *testing.T) {
	c := graphCompleteness(&GraphResponse{}, 4)
	if c.Verdict != "complete" {
		t.Fatalf("verdict = %q, want complete", c.Verdict)
	}
	if c.Resolved != 4 {
		t.Errorf("resolved = %d, want 4", c.Resolved)
	}
}

func TestGraphCompletenessPartialLowConfidence(t *testing.T) {
	c := graphCompleteness(&GraphResponse{LowConfidenceHidden: 2}, 3)
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial", c.Verdict)
	}
	if c.Hidden != 2 {
		t.Errorf("hidden = %d, want 2", c.Hidden)
	}
	if !strings.Contains(c.Advice, "min_confidence") {
		t.Errorf("advice should point at min_confidence: %q", c.Advice)
	}
}

func TestGraphCompletenessPartialOmitted(t *testing.T) {
	c := graphCompleteness(&GraphResponse{OmittedEdges: 3}, 10)
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial", c.Verdict)
	}
	if !strings.Contains(c.Advice, "token budget") {
		t.Errorf("advice should mention token budget: %q", c.Advice)
	}
}

func TestGraphCompletenessPartialTruncated(t *testing.T) {
	c := graphCompleteness(&GraphResponse{Truncated: true}, 10)
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial", c.Verdict)
	}
}

// BuildBlastResponse should stamp a per-caller relation reflecting the edge
// kind of the bucket each affected symbol came from.
func TestBuildBlastResponseRelation(t *testing.T) {
	r := blast.Result{
		Symbol:                 model.Symbol{ID: 1, Name: "Hub", Qualified: "Hub"},
		Risk:                   blast.RiskLow,
		DirectCallers:          []model.Symbol{{ID: 10, Qualified: "Caller", FileID: 2}},
		AffectedSubclasses:     []model.Symbol{{ID: 11, Qualified: "Sub", FileID: 2}},
		AffectedViaIncludes:    []model.Symbol{{ID: 12, Qualified: "Inc", FileID: 2}},
		AffectedViaComposition: []model.Symbol{{ID: 13, Qualified: "Comp", FileID: 2}},
		AffectedTests:          []string{},
		TotalAffected:          4,
	}
	files := func(id int64) (string, bool) {
		if id == 2 {
			return "x.rb", true
		}
		return "", false
	}
	resp := BuildBlastResponse(context.Background(), r, files, nil)

	if got := resp.DirectCallers[0].Relation; got != "calls Hub" {
		t.Errorf("direct relation = %q, want %q", got, "calls Hub")
	}
	if got := resp.AffectedSubclasses[0].Relation; got != "inherits Hub" {
		t.Errorf("subclass relation = %q, want %q", got, "inherits Hub")
	}
	if got := resp.AffectedViaIncludes[0].Relation; got != "includes Hub" {
		t.Errorf("include relation = %q, want %q", got, "includes Hub")
	}
	if got := resp.AffectedViaComposition[0].Relation; got != "composes Hub" {
		t.Errorf("composition relation = %q, want %q", got, "composes Hub")
	}
	if resp.Completeness == nil {
		t.Error("completeness should be set on every blast response")
	}
}

// A budget trim that drops callers must downgrade a "complete" verdict so it
// never survives a trim that actually shed dependents.
func TestApplyBlastBudgetDowngradesCompleteness(t *testing.T) {
	var callers []BlastCaller
	for i := 0; i < 50; i++ {
		callers = append(callers, BlastCaller{Symbol: "C", File: "f.rb", Relation: "calls Hub"})
	}
	resp := &BlastResponse{
		Symbol:        "Hub",
		DirectCallers: callers,
		TotalAffected: 50,
		Completeness:  &Completeness{Verdict: "complete", Resolved: 50},
	}
	ApplyBlastBudget(resp, 200) // tiny budget forces a caller trim
	if !resp.Truncated {
		t.Fatal("expected Truncated after trim")
	}
	if resp.Completeness.Verdict != "partial" {
		t.Errorf("verdict = %q, want partial after trim", resp.Completeness.Verdict)
	}
}
