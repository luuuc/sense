package mcpserver

import (
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
)

func TestGraphHintsManyCallers(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "HandleRequest",
			Qualified: "server.HandleRequest",
			File:      "internal/server/handler.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: make([]mcpio.CallEdgeRef, 7),
		},
	}
	hints := graphHints(resp, "both")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense_blast" {
		t.Errorf("tool = %q, want sense_blast", hints[0].Tool)
	}
	if hints[0].Args["symbol"] != "server.HandleRequest" {
		t.Errorf("args.symbol = %q, want server.HandleRequest", hints[0].Args["symbol"])
	}
}

func TestGraphHintsManyCallersViaTestSummary(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "Service",
			Qualified: "pkg.Service",
			File:      "internal/pkg/service.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: make([]mcpio.CallEdgeRef, 3),
		},
		TestCallerSummary: &mcpio.TestCallerSummary{Count: 3},
	}
	hints := graphHints(resp, "both")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense_blast" {
		t.Errorf("tool = %q, want sense_blast", hints[0].Tool)
	}
}

func TestGraphHintsNoCallers(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "Orphan",
			Qualified: "models.Orphan",
			File:      "internal/models/orphan.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{},
		},
	}
	hints := graphHints(resp, "both")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense_search" {
		t.Errorf("tool = %q, want sense_search", hints[0].Tool)
	}
	if hints[0].Args["query"] != "Orphan" {
		t.Errorf("args.query = %q, want Orphan", hints[0].Args["query"])
	}
}

func TestGraphHintsNoShownButManyHiddenLowConfidence(t *testing.T) {
	// 0 shown callers but 111 below the confidence floor is a HIGH-reach symbol
	// (reach = shown + hidden). It must get the blast jump first (blast at the
	// default 0.3 floor includes those weak edges and shows what holds it), then
	// the min_confidence knob as the second hint. The go glm/consul loss was a
	// milder version of this: 2 shown + 73 hidden fell through the old
	// shown-only gate and never got routed to blast.
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "current_user",
			Qualified: "Authentication#current_user",
			File:      "app/controllers/concerns/authentication.rb",
		},
		Edges:               mcpio.GraphEdges{CalledBy: []mcpio.CallEdgeRef{}},
		LowConfidenceHidden: 111,
	}
	hints := graphHints(resp, "callers")
	if len(hints) != 2 {
		t.Fatalf("want 2 hints (blast + min_confidence knob), got %d", len(hints))
	}
	if hints[0].Tool != "sense_blast" {
		t.Errorf("hints[0].tool = %q, want sense_blast (high reach routes to blast first)", hints[0].Tool)
	}
	if hints[1].Tool != "sense_graph" || hints[1].Args["min_confidence"] != 0.3 {
		t.Errorf("hints[1] = %+v, want the min_confidence=0.3 knob", hints[1])
	}
	if hints[1].Args["direction"] != "callers" {
		t.Errorf("hints[1].args.direction = %v, want callers", hints[1].Args["direction"])
	}
}

func TestGraphHintsFewShownManyHiddenRoutesToBlast(t *testing.T) {
	// The exact go glm/consul shape: NewServer with 2 shown callers and 73 below
	// the floor. reach = 75 >= 5, so the blast jump fires - the old
	// `>= 5 shown` gate missed this and left the model with no route to the
	// retention ring.
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "NewServer",
			Qualified: "consul.NewServer",
			File:      "agent/consul/server.go",
		},
		Edges:               mcpio.GraphEdges{CalledBy: make([]mcpio.CallEdgeRef, 2)},
		LowConfidenceHidden: 73,
	}
	hints := graphHints(resp, "callers")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint (blast), got %d", len(hints))
	}
	if hints[0].Tool != "sense_blast" {
		t.Errorf("tool = %q, want sense_blast", hints[0].Tool)
	}
	if hints[0].Args["symbol"] != "consul.NewServer" {
		t.Errorf("args.symbol = %q, want consul.NewServer", hints[0].Args["symbol"])
	}
}

func TestGraphHintsNoCallersTestFile(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "TestFoo",
			Qualified: "pkg.TestFoo",
			File:      "internal/pkg/handler_test.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{},
		},
	}
	hints := graphHints(resp, "both")
	if len(hints) != 0 {
		t.Fatalf("want 0 hints for test file with no callers, got %d", len(hints))
	}
}

func TestGraphHintsFewCallersNoFiller(t *testing.T) {
	// 1-4 callers with no hidden tail: the old code appended a "see what this
	// symbol depends on" callees hint here (46 of 605 issuances) purely because
	// direction was callers. It restated the payload and is dropped - a
	// low-reach symbol with everything already shown earns no hint, in any
	// direction.
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "Foo",
			Qualified: "pkg.Foo",
			File:      "internal/pkg/foo.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: []mcpio.CallEdgeRef{{Symbol: "Bar"}},
		},
	}
	for _, dir := range []model.Direction{"callers", "both"} {
		if hints := graphHints(resp, dir); hints != nil {
			t.Fatalf("direction %q: want nil hints (1 caller, all shown), got %d", dir, len(hints))
		}
	}
}

func TestGraphHintsManyCallersSingleBlastHint(t *testing.T) {
	// A high-reach symbol with everything shown gets exactly the blast jump -
	// no callees filler tagged on for the callers direction.
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name:      "Hub",
			Qualified: "pkg.Hub",
			File:      "internal/pkg/hub.go",
		},
		Edges: mcpio.GraphEdges{
			CalledBy: make([]mcpio.CallEdgeRef, 10),
		},
	}
	hints := graphHints(resp, "callers")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint (blast only, no callees filler), got %d", len(hints))
	}
	if hints[0].Tool != "sense_blast" {
		t.Errorf("hints[0].tool = %q, want sense_blast", hints[0].Tool)
	}
}

func TestSearchHintsAlwaysEmpty(t *testing.T) {
	// searchHints is a deliberate no-op: both hints it used to emit (the
	// most-issued "strong match, explore its relationships" and the file-cluster
	// conventions nudge) only restated the payload. Whatever the results shape,
	// it returns nothing now.
	cases := []mcpio.SearchResponse{
		{Results: []mcpio.SearchResultEntry{{Symbol: "auth.Verify", File: "internal/auth/verify.go", Score: 0.92}}},
		{Results: []mcpio.SearchResultEntry{
			{Symbol: "Foo", File: "internal/models/user.go", Score: 0.5},
			{Symbol: "Bar", File: "internal/models/user.go", Score: 0.4},
			{Symbol: "Baz", File: "internal/models/user.go", Score: 0.3},
		}},
		{Results: []mcpio.SearchResultEntry{}},
	}
	for i, resp := range cases {
		if hints := searchHints(resp); hints != nil {
			t.Errorf("case %d: want nil hints, got %d", i, len(hints))
		}
	}
}

func TestBlastHintsHighRiskNoLongerHinted(t *testing.T) {
	// The risk="high" → conventions hint restated the `risk` field and pushed
	// the least-followed tool; it is dropped. A high-risk symbol that HAS tests
	// now earns no hint at all.
	resp := mcpio.BlastResponse{
		Symbol:        "User#destroy",
		Risk:          "high",
		TotalAffected: 15,
		AffectedTests: []string{"test/user_test.rb"},
	}
	if hints := blastHints(resp); hints != nil {
		t.Fatalf("want nil hints (risk hint dropped, tests present), got %d", len(hints))
	}
}

func TestBlastHintsNoTests(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "Order#finalize",
		Risk:          "medium",
		TotalAffected: 3,
		AffectedTests: []string{},
	}
	hints := blastHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense_search" {
		t.Errorf("tool = %q, want sense_search", hints[0].Tool)
	}
}

func TestBlastHintsHighRiskNoTests(t *testing.T) {
	// Only the no-test-coverage gap survives; risk="high" adds nothing.
	resp := mcpio.BlastResponse{
		Symbol:        "Critical#method",
		Risk:          "high",
		TotalAffected: 10,
		AffectedTests: []string{},
	}
	hints := blastHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint (search for tests only), got %d", len(hints))
	}
	if hints[0].Tool != "sense_search" {
		t.Errorf("hints[0].tool = %q, want sense_search", hints[0].Tool)
	}
}

func TestBlastHintsEmpty(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "Leaf#method",
		Risk:          "low",
		TotalAffected: 1,
		AffectedTests: []string{"test/leaf_test.rb"},
	}
	hints := blastHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints, got %d", len(hints))
	}
}

func TestBlastHintsNoTestsZeroAffected(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "Isolated",
		Risk:          "low",
		TotalAffected: 0,
		AffectedTests: []string{},
	}
	hints := blastHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints (no tests but 0 affected), got %d", len(hints))
	}
}

func TestConventionsHintsAlwaysEmpty(t *testing.T) {
	// conventionsHints is a deliberate no-op: its two hints restated the
	// convention text and the domain arg already in the request/response and
	// circled among the least-followed tools.
	cases := []struct {
		resp   mcpio.ConventionsResponse
		domain string
	}{
		{mcpio.ConventionsResponse{Conventions: []mcpio.ConventionEntry{{Description: "controllers follow REST", Strength: 0.85}}}, ""},
		{mcpio.ConventionsResponse{Conventions: []mcpio.ConventionEntry{{Description: "naming pattern", Strength: 0.4}}}, "models"},
		{mcpio.ConventionsResponse{Conventions: []mcpio.ConventionEntry{{Description: "strong pattern", Strength: 0.9}}}, "controllers"},
	}
	for i, c := range cases {
		if hints := conventionsHints(c.resp, c.domain); hints != nil {
			t.Errorf("case %d: want nil hints, got %d", i, len(hints))
		}
	}
}

func TestStatusHintsStaleFiles(t *testing.T) {
	stale := 5
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "" {
		t.Errorf("tool = %q, want empty (advisory)", hints[0].Tool)
	}
	if hints[0].Reason == "" {
		t.Error("reason should not be empty")
	}
}

func TestStatusHintsFirstQueryNoLongerHinted(t *testing.T) {
	// The "start of session - check project conventions" hint fired on every
	// fresh session regardless of task; it is dropped. A fresh, non-stale index
	// now earns no hint.
	stale := 0
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	if hints := statusHints(resp); hints != nil {
		t.Fatalf("want nil hints (session-start hint dropped, index fresh), got %d", len(hints))
	}
}

func TestStatusHintsEmpty(t *testing.T) {
	stale := 0
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints, got %d", len(hints))
	}
}

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"internal/pkg/handler_test.go", true},
		{"test/models/user_test.rb", true},
		{"tests/integration/smoke.py", true},
		{"spec/models/user_spec.rb", true},
		{"internal/pkg/handler.go", false},
		{"app/models/contest.rb", false},
		{"internal/attestation/verify.go", false},
		{"app/controllers/protest_controller.rb", false},
	}
	for _, tt := range tests {
		got := isTestFile(tt.path)
		if got != tt.want {
			t.Errorf("isTestFile(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestHintDeterminism(t *testing.T) {
	graphResp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{
			Name: "Hub", Qualified: "pkg.Hub", File: "pkg/hub.go",
		},
		Edges: mcpio.GraphEdges{CalledBy: make([]mcpio.CallEdgeRef, 8)},
	}
	blastResp := mcpio.BlastResponse{
		Symbol: "Critical", Risk: "high",
		TotalAffected: 5, AffectedTests: []string{},
	}
	convResp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Description: "strong", Strength: 0.9},
		},
	}
	stale := 2
	statusResp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{StaleFilesSeen: &stale},
	}

	firstGraph := graphHints(graphResp, "callers")
	firstBlast := blastHints(blastResp)
	firstConv := conventionsHints(convResp, "models")
	firstStatus := statusHints(statusResp)

	for i := 0; i < 50; i++ {
		assertSameHints(t, "graphHints", firstGraph, graphHints(graphResp, "callers"))
		assertSameHints(t, "blastHints", firstBlast, blastHints(blastResp))
		assertSameHints(t, "conventionsHints", firstConv, conventionsHints(convResp, "models"))
		assertSameHints(t, "statusHints", firstStatus, statusHints(statusResp))
	}
}

func assertSameHints(t *testing.T, name string, want, got []mcpio.NextStep) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: length changed from %d to %d", name, len(want), len(got))
	}
	for i := range want {
		if want[i].Tool != got[i].Tool || want[i].Reason != got[i].Reason {
			t.Fatalf("%s: hint[%d] changed: %+v → %+v", name, i, want[i], got[i])
		}
	}
}

func TestResponseCapsNextSteps(t *testing.T) {
	graphResp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Qualified: "X", File: "x.go"},
		Edges:  mcpio.GraphEdges{CalledBy: make([]mcpio.CallEdgeRef, 20)},
	}
	if n := len(graphHints(graphResp, "callers")); n > mcpio.MaxNextSteps {
		t.Errorf("graphHints returned %d hints, max is %d", n, mcpio.MaxNextSteps)
	}

	searchResp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "A", File: "pkg/a.go", Score: 0.95},
			{Symbol: "B", File: "pkg/a.go", Score: 0.9},
			{Symbol: "C", File: "pkg/a.go", Score: 0.85},
		},
	}
	if n := len(searchHints(searchResp)); n > mcpio.MaxNextSteps {
		t.Errorf("searchHints returned %d hints, max is %d", n, mcpio.MaxNextSteps)
	}

	blastResp := mcpio.BlastResponse{
		Risk:          "high",
		TotalAffected: 10,
	}
	if n := len(blastHints(blastResp)); n > mcpio.MaxNextSteps {
		t.Errorf("blastHints returned %d hints, max is %d", n, mcpio.MaxNextSteps)
	}

	convResp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Strength: 0.9, Description: "test pattern"},
		},
	}
	if n := len(conventionsHints(convResp, "models")); n > mcpio.MaxNextSteps {
		t.Errorf("conventionsHints returned %d hints, max is %d", n, mcpio.MaxNextSteps)
	}

	statusResp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{StaleFilesSeen: intPtr(5)},
	}
	if n := len(statusHints(statusResp)); n > mcpio.MaxNextSteps {
		t.Errorf("statusHints returned %d hints, max is %d", n, mcpio.MaxNextSteps)
	}
}

func TestDeadCodeHintsWithDead(t *testing.T) {
	resp := mcpio.UnreferencedResponse{
		DeadCount: 2,
		Unreferenced: mcpio.UnreferencedSymbols{
			Dead: []mcpio.DeadEntry{
				{Qualified: "model.Order", File: "internal/model/order.go"},
				{Qualified: "pkg.Unused", File: "internal/pkg/unused.go"},
			},
		},
	}
	hints := deadCodeHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense_graph" {
		t.Errorf("tool = %q, want sense_graph", hints[0].Tool)
	}
	if hints[0].Args["symbol"] != "model.Order" {
		t.Errorf("args.symbol = %q, want model.Order", hints[0].Args["symbol"])
	}
}

func TestDeadCodeHintsEmpty(t *testing.T) {
	resp := mcpio.UnreferencedResponse{
		DeadCount: 0,
		Unreferenced: mcpio.UnreferencedSymbols{
			Dead: []mcpio.DeadEntry{},
		},
	}
	hints := deadCodeHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints for no dead symbols, got %d", len(hints))
	}
}

func TestDeadCodeHintsCountMismatch(t *testing.T) {
	resp := mcpio.UnreferencedResponse{
		DeadCount: 5,
		Unreferenced: mcpio.UnreferencedSymbols{
			Dead: []mcpio.DeadEntry{},
		},
	}
	hints := deadCodeHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints when Dead is empty despite DeadCount>0, got %d", len(hints))
	}
}

func intPtr(v int) *int { return &v }
