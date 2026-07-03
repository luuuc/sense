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

// A sizable inherited_by (direct-subtype) list gets anti-over-traversal steering
// so the agent trusts the enumeration instead of graph-walking each subclass
// (the litellm 31-calls-for-a-2-call-set blowup).
const subtypeNote = "complete set of direct subtypes"

func graphWithInheritedBy(n int) *GraphResponse {
	return &GraphResponse{Edges: GraphEdges{InheritedBy: make([]InheritEdgeRef, n)}}
}

func TestGraphCompletenessSubtypeNoteComplete(t *testing.T) {
	c := graphCompleteness(graphWithInheritedBy(manySubtypes), 5)
	if c.Verdict != "complete" {
		t.Fatalf("verdict = %q, want complete", c.Verdict)
	}
	if !strings.Contains(c.Advice, subtypeNote) {
		t.Errorf("advice should steer against per-subclass expansion: %q", c.Advice)
	}
}

func TestGraphCompletenessSubtypeNoteBelowThreshold(t *testing.T) {
	c := graphCompleteness(graphWithInheritedBy(manySubtypes-1), 4)
	if strings.Contains(c.Advice, subtypeNote) {
		t.Errorf("small subtype list must not trigger the note: %q", c.Advice)
	}
}

// called_by must NOT trigger the note: a resolved caller set is a SUBSET of a
// model's true dependents (behavioral deps reach it via a bare local Sense never
// resolved), so claiming it complete suppressed saleor's grep and dropped it to
// 0/4. Even a large called_by list stays unsteered.
func TestGraphCompletenessCalledByDoesNotTriggerNote(t *testing.T) {
	c := graphCompleteness(&GraphResponse{Edges: GraphEdges{CalledBy: make([]CallEdgeRef, 125)}}, 125)
	if strings.Contains(c.Advice, subtypeNote) {
		t.Errorf("called_by must not get the subtype note (saleor grep-trap regression): %q", c.Advice)
	}
}

// Low-confidence-hidden is call-edge noise that never sheds the inherited_by
// set, so the note stands even though the verdict is partial.
func TestGraphCompletenessSubtypeNotePartialLowConf(t *testing.T) {
	resp := graphWithInheritedBy(6)
	resp.LowConfidenceHidden = 3
	c := graphCompleteness(resp, 6)
	if c.Verdict != "partial" {
		t.Fatalf("verdict = %q, want partial", c.Verdict)
	}
	if !strings.Contains(c.Advice, subtypeNote) {
		t.Errorf("inherited_by not shed by low-conf hidden — note should stand: %q", c.Advice)
	}
}

// When budget actually shed edges (truncated / omitted), the list is NOT the
// full set, so the "ARE the complete set" claim must be suppressed.
func TestGraphCompletenessEnumeratedNoteSuppressedWhenShed(t *testing.T) {
	trunc := graphWithInheritedBy(6)
	trunc.Truncated = true
	if strings.Contains(graphCompleteness(trunc, 6).Advice, subtypeNote) {
		t.Error("truncated response must not claim the enumerated set is complete")
	}
	omit := graphWithInheritedBy(6)
	omit.OmittedEdges = 2
	if strings.Contains(graphCompleteness(omit, 6).Advice, subtypeNote) {
		t.Error("omitted-edges response must not claim the enumerated set is complete")
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
