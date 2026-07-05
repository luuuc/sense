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
		if resp.SeenElsewhere == nil {
			return 0
		}
		return resp.SeenElsewhere.Count
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

// TestGraphFanWalkCollapsesSharedLayer is the sibling fan-walk regression:
// two callers-direction graph calls on siblings sharing a depth-2 hub
// (HandleRequest calls both Verify and FindUser; main.main calls
// HandleRequest). The first call renders the hub in its layer; the second
// must collapse it into the layer's seen_elsewhere instead of re-sending
// it — while its own depth-1 called_by renders in full even though the
// caller was already seen (the root's edges are the direct answer).
func TestGraphFanWalkCollapsesSharedLayer(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	graph := func(symbol string) map[string]any {
		result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
			"symbol": symbol, "direction": "callers",
		}))
		if err != nil {
			t.Fatalf("handleGraph(%s): %v", symbol, err)
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
			t.Fatalf("unmarshal %s: %v", symbol, err)
		}
		return resp
	}
	layerOf := func(resp map[string]any) map[string]any {
		layers, _ := resp["layers"].([]any)
		if len(layers) == 0 {
			t.Fatalf("expected a depth-2 layer, got %v", resp["layers"])
		}
		return layers[0].(map[string]any)
	}

	// First sibling: the shared hub (main.main) renders in the layer.
	first := layerOf(graph("auth.Verify"))
	if _, collapsed := first["seen_elsewhere"]; collapsed {
		t.Errorf("first call has nothing to collapse, got %v", first["seen_elsewhere"])
	}
	firstCallers, _ := first["edges"].(map[string]any)["called_by"].([]any)
	if len(firstCallers) == 0 {
		t.Fatal("fixture should render main.main in the first call's layer")
	}

	// Second sibling: same depth-2 hub — must collapse, not re-send.
	second := graph("model.FindUser")
	rootCallers, _ := second["edges"].(map[string]any)["called_by"].([]any)
	if len(rootCallers) == 0 {
		t.Error("root depth-1 called_by must render in full even when already seen")
	}
	l2 := layerOf(second)
	se, ok := l2["seen_elsewhere"].(map[string]any)
	if !ok || se["count"].(float64) < 1 {
		t.Fatalf("expected the shared depth-2 hub collapsed into seen_elsewhere, got %v", l2)
	}
	l2Callers, _ := l2["edges"].(map[string]any)["called_by"].([]any)
	if len(l2Callers) != 0 {
		t.Errorf("collapsed layer should not re-enumerate the seen hub, got %v", l2Callers)
	}
}

// TestGraphLayerMarkingHonorsBudgetTrim is the layer twin of the caller-leak
// regression: layer targets must be marked seen AFTER the budget trim, never
// from the pre-trim response. Call 1 runs with a starvation budget that
// sheds its depth-2 layer, so the layer's hub target (main.main) was never
// delivered; call 2 (full budget) shares that hub at depth 2 and must
// RENDER it, not collapse it — collapsing would tell the agent "already
// returned to this session", which would be false.
func TestGraphLayerMarkingHonorsBudgetTrim(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	graphLayer := func(symbol string, budget int) (map[string]any, []any) {
		ts.handlers.defaults.GraphTokenBudget = budget
		result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
			"symbol": symbol, "direction": "callers",
		}))
		if err != nil {
			t.Fatalf("handleGraph(%s, budget=%d): %v", symbol, budget, err)
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
			t.Fatalf("unmarshal %s: %v", symbol, err)
		}
		layers, _ := resp["layers"].([]any)
		return resp, layers
	}

	// Call 1: starve the budget so the depth-2 layer (main.main) is shed.
	resp1, layers1 := graphLayer("auth.Verify", 1)
	if len(layers1) != 0 {
		t.Skipf("budget=1 did not shed the layer (got %v) — fixture grew; retune the starvation budget", resp1["layers"])
	}

	// Call 2: full budget on the sibling sharing the depth-2 hub. The hub
	// was never delivered, so it must render, and nothing may collapse.
	_, layers2 := graphLayer("model.FindUser", 1_000_000)
	if len(layers2) == 0 {
		t.Fatal("expected a depth-2 layer on the second call")
	}
	l2 := layers2[0].(map[string]any)
	if se, collapsed := l2["seen_elsewhere"]; collapsed {
		t.Fatalf("layer collapsed against budget-shed targets the agent never received: %v", se)
	}
	callers, _ := l2["edges"].(map[string]any)["called_by"].([]any)
	if len(callers) == 0 {
		t.Error("the never-delivered depth-2 hub must render in full on the second call")
	}
}

// TestBlastCollapsesCallerFirstSeenAsLayerTarget pins the cross-tool rider:
// marking layer targets means a later sense_blast collapses a direct caller
// the session first received only as a graph layer entry. The flow is the
// fan-walk's natural next step — graph a sibling (layer shows the hub's
// caller), then blast the hub.
func TestBlastCollapsesCallerFirstSeenAsLayerTarget(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// graph model.FindUser callers: depth-1 = HandleRequest, depth-2 layer =
	// main.main (rendered, hence marked).
	if _, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "model.FindUser", "direction": "callers",
	})); err != nil {
		t.Fatalf("handleGraph: %v", err)
	}

	// blast HandleRequest: its direct caller main.main was delivered as a
	// layer entry above, so it collapses into seen_elsewhere.
	v := runBlast(t, ts.handlers, "handler.HandleRequest")
	if v.SeenElsewhere == nil || v.SeenElsewhere.Count < 1 {
		t.Fatalf("expected the layer-delivered caller collapsed into seen_elsewhere, got %+v", v)
	}
	for _, c := range v.DirectCallers {
		if c["symbol"] == "main.main" {
			t.Errorf("main.main must be collapsed, not re-enumerated: %v", v.DirectCallers)
		}
	}
}

// TestIdsToMarkSeen pins what the graph response marks seen: the rendered
// depth-1 called_by callers (blast's direct-caller set) plus the rendered
// layer call-edge targets (feeding the layer seen-collapse) — never the
// root's callees, and never unresolved view edges (ID 0).
func TestIdsToMarkSeen(t *testing.T) {
	resp := &mcpio.GraphResponse{
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{
				{ID: 1, Symbol: "A#m"},
				{ID: 2, Symbol: "B#n"},
				{ID: 0, Symbol: "app/views/x.erb"}, // unresolved view edge — skip
				{ID: 3, Symbol: "C#o"},
			},
			// Root callees must NOT be collected — deliberate conservatism,
			// not principle: marking them would widen what a later blast
			// collapses, an expansion that needs its own bench (see
			// idsToMarkSeen's doc), not a rider on this fixture.
			Calls: []mcpio.CallEdgeRef{{ID: 88, Symbol: "callee"}},
		},
		Layers: []mcpio.GraphLayer{{
			Depth: 2,
			Edges: mcpio.GraphEdges{
				CalledBy: []mcpio.CallEdgeRef{{ID: 4, Symbol: "D#p"}, {ID: 0, Symbol: "view"}},
				Calls:    []mcpio.CallEdgeRef{{ID: 5, Symbol: "E#q"}},
			},
		}},
	}

	got := idsToMarkSeen(resp)
	want := map[int64]bool{1: true, 2: true, 3: true, 4: true, 5: true}
	if len(got) != len(want) {
		t.Fatalf("idsToMarkSeen = %v, want called_by {1,2,3} + layer targets {4,5}", got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("collected id %d — root callees must not be marked seen", id)
		}
	}
}

// parseBlast unmarshals a blast tool result into the fields the dedup tests
// assert on. Keeps the test focused on the dedup contract.
type blastView struct {
	DirectCallers       []map[string]any `json:"direct_callers"`
	TotalAffected       int              `json:"total_affected"`
	DirectCallersByArea map[string]int   `json:"direct_callers_by_area"`
	SeenElsewhere       *struct {
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
	if first.SeenElsewhere != nil {
		t.Errorf("fresh-session blast must collapse nothing, got seen_elsewhere=%+v", first.SeenElsewhere)
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
	if second.SeenElsewhere == nil || second.SeenElsewhere.Count != firstCount {
		t.Errorf("second blast seen_elsewhere = %+v, want count %d (all callers already returned)", second.SeenElsewhere, firstCount)
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
