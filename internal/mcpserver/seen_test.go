package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
)

// TestSeenPredicateAndMarkSeen pins the read/write sides of the per-session
// dedup state: markSeen records ids, seenPredicate reports them, and a handler
// with no seenSymbols map degrades to "track nothing" instead of panicking.
func TestSeenPredicateAndMarkSeen(t *testing.T) {
	h := &handlers{seenSymbols: map[int64]bool{}}
	pred := h.seenPredicate()

	if pred(7) {
		t.Error("nothing marked yet — id 7 must not be seen")
	}
	h.markSeen([]int64{7, 9})
	if !pred(7) || !pred(9) {
		t.Error("markSeen([7,9]) must make both seen")
	}
	if pred(8) {
		t.Error("id 8 was never marked — must not be seen")
	}

	// markSeen with no ids is a no-op (no panic, no entries).
	h.markSeen(nil)

	// A handler without a seen-set disables dedup gracefully.
	nilH := &handlers{}
	nilH.markSeen([]int64{1}) // must not panic
	if nilH.seenPredicate()(1) {
		t.Error("a nil seen-set must report nothing as seen")
	}
}

// TestSeenDedupCollapsesOnlyRenderedCallers is the LEAK regression. graph marks
// the FINAL rendered called_by (post budget-trim), never the pre-trim query set
// — collapsing a caller the model never received would silently drop it (the
// bug that once leaked 27 unshown callers). It is a differential: a graph whose
// called_by the budget TRIMMED must make a later blast collapse STRICTLY FEWER
// callers than an untrimmed graph. If the marking ever reverts to the untrimmed
// query, both runs mark the same full set, the counts converge, and this fails.
func TestSeenDedupCollapsesOnlyRenderedCallers(t *testing.T) {
	ctx := context.Background()
	const sym = "auth.Verify" // a fixture symbol with two static callers

	seenAfterGraph := func(graphBudget int) int {
		// Fresh server → fresh seen-set. graph then blast in ONE session: graph
		// marks its rendered callers, blast collapses the ones it also lists.
		ts := setupTestServer(t)
		ts.handlers.defaults.GraphTokenBudget = graphBudget
		if _, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{"symbol": sym, "direction": "callers"})); err != nil {
			t.Fatalf("handleGraph(budget=%d): %v", graphBudget, err)
		}
		result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{"symbol": sym}))
		if err != nil {
			t.Fatalf("handleBlast(budget=%d): %v", graphBudget, err)
		}
		var resp mcpio.BlastResponse
		if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if resp.SeenVia == nil {
			return 0
		}
		return resp.SeenVia.Count
	}

	trimmed := seenAfterGraph(1)      // budget=1 trims called_by to ~one entry
	full := seenAfterGraph(1_000_000) // no trim: every rendered caller marked

	if full < 2 {
		t.Skipf("fixture symbol %q collapses only %d callers at full budget — too few to test the differential", sym, full)
	}
	if full <= trimmed {
		t.Fatalf("blast collapsed %d callers after a TRIMMED graph and %d after a FULL graph; "+
			"a trimmed graph renders fewer callers so it must collapse strictly fewer — equal/greater "+
			"means the marking ignored the budget trim and leaked unshown callers", trimmed, full)
	}
}

// TestRenderedCallerIDs pins that only the callers the graph response actually
// rendered in called_by are collected (so blast collapses only what the model
// received), and that unresolved-source view edges (ID 0) are skipped.
func TestRenderedCallerIDs(t *testing.T) {
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{
				{ID: 1, Symbol: "A#m"},
				{ID: 2, Symbol: "B#n"},
				{ID: 0, Symbol: "app/views/x.erb"}, // unresolved view edge — skip
				{ID: 3, Symbol: "C#o"},
			},
			// Calls (callees) and other buckets must NOT be collected — they are
			// not blast direct callers.
			Calls: []mcpio.CallEdgeRef{{ID: 88, Symbol: "callee"}},
		},
	}

	got := renderedCallerIDs(resp)
	want := map[int64]bool{1: true, 2: true, 3: true}
	if len(got) != len(want) {
		t.Fatalf("renderedCallerIDs = %v, want the 3 rendered called_by ids {1,2,3}", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("collected id %d — only rendered called_by ids should be marked seen", id)
		}
	}
}

// parseBlast unmarshals a blast tool result into the fields the dedup tests
// assert on. Keeps the test focused on the dedup contract.
type blastView struct {
	DirectCallers       []map[string]any `json:"direct_callers"`
	TotalAffected       int              `json:"total_affected"`
	DirectCallersByArea map[string]int   `json:"direct_callers_by_area"`
	SeenVia             *struct {
		Count int `json:"count"`
	} `json:"seen_elsewhere"`
	Completeness *struct {
		Verdict string `json:"verdict"`
	} `json:"completeness"`
}

func runBlast(t *testing.T, h *handlers, symbol string) blastView {
	t.Helper()
	res, err := h.handleBlast(context.Background(), toolReq(map[string]any{"symbol": symbol}))
	if err != nil {
		t.Fatalf("handleBlast: %v", err)
	}
	var v blastView
	if err := json.Unmarshal([]byte(resultText(t, res)), &v); err != nil {
		t.Fatalf("parse blast: %v", err)
	}
	return v
}

// TestBlastSingleCallIsFullThenGraphDedups pins order-independence end to end:
//  1. A blast in a FRESH session enumerates every direct caller (empty seen-set
//     ⇒ no collapse), recording them as seen.
//  2. A graph on the same symbol next would dedup against blast's recorded ids
//     (the symmetric direction). We assert the recording half here.
//  3. A SECOND blast on the same symbol collapses the callers the first blast
//     recorded — proving the dedup is order-independent and blast records its
//     own callers for later calls.
func TestBlastSingleCallIsFullThenGraphDedups(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers

	// 1. Fresh session: blast on auth.Verify enumerates its callers in full.
	first := runBlast(t, h, "auth.Verify")
	if first.SeenVia != nil {
		t.Errorf("fresh-session blast must collapse nothing, got seen_elsewhere=%+v", first.SeenVia)
	}
	if len(first.DirectCallers) == 0 {
		t.Fatal("fresh blast should enumerate at least one direct caller")
	}
	if first.Completeness == nil || first.Completeness.Verdict != "complete" {
		t.Fatalf("fresh blast verdict = %+v, want complete", first.Completeness)
	}
	firstCount := len(first.DirectCallers)
	fullArea := sumIntMap(first.DirectCallersByArea)

	// 2+3. Second blast on the same symbol: the callers recorded by the first
	// blast are now collapsed, but the radius is preserved.
	second := runBlast(t, h, "auth.Verify")
	if second.SeenVia == nil || second.SeenVia.Count != firstCount {
		t.Errorf("second blast seen_elsewhere = %+v, want count %d (all callers already returned)", second.SeenVia, firstCount)
	}
	if len(second.DirectCallers) != 0 {
		t.Errorf("second blast enumerated %d callers, want 0 (all already seen)", len(second.DirectCallers))
	}
	if second.TotalAffected != first.TotalAffected {
		t.Errorf("total_affected drifted: %d vs %d (magnitude must survive collapse)", second.TotalAffected, first.TotalAffected)
	}
	if sumIntMap(second.DirectCallersByArea) != fullArea {
		t.Errorf("by_area sum drifted: %d vs %d", sumIntMap(second.DirectCallersByArea), fullArea)
	}
	if second.Completeness == nil || second.Completeness.Verdict != "complete" {
		t.Errorf("second blast verdict = %+v, want complete (dedup is not truncation)", second.Completeness)
	}
}

func sumIntMap(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
