package mcpserver

import (
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
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
	if hints[0].Tool != "sense.blast" {
		t.Errorf("tool = %q, want sense.blast", hints[0].Tool)
	}
	if hints[0].Args["symbol"] != "server.HandleRequest" {
		t.Errorf("args.symbol = %q, want server.HandleRequest", hints[0].Args["symbol"])
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
	if hints[0].Tool != "sense.search" {
		t.Errorf("tool = %q, want sense.search", hints[0].Tool)
	}
	if hints[0].Args["query"] != "Orphan" {
		t.Errorf("args.query = %q, want Orphan", hints[0].Args["query"])
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

func TestGraphHintsCallersOnlyDirection(t *testing.T) {
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
	hints := graphHints(resp, "callers")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.graph" {
		t.Errorf("tool = %q, want sense.graph", hints[0].Tool)
	}
	if hints[0].Args["direction"] != "callees" {
		t.Errorf("args.direction = %q, want callees", hints[0].Args["direction"])
	}
}

func TestGraphHintsManyCallersAndCallersDirection(t *testing.T) {
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
	if len(hints) != 2 {
		t.Fatalf("want 2 hints (blast + callees), got %d", len(hints))
	}
	if hints[0].Tool != "sense.blast" {
		t.Errorf("hints[0].tool = %q, want sense.blast", hints[0].Tool)
	}
	if hints[1].Tool != "sense.graph" {
		t.Errorf("hints[1].tool = %q, want sense.graph", hints[1].Tool)
	}
}

func TestGraphHintsEmpty(t *testing.T) {
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
	hints := graphHints(resp, "both")
	if hints != nil {
		t.Fatalf("want nil hints (1-4 callers, both direction), got %d", len(hints))
	}
}

func TestSearchHintsStrongMatch(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "auth.Verify", File: "internal/auth/verify.go", Score: 0.92},
			{Symbol: "auth.Token", File: "internal/auth/token.go", Score: 0.71},
		},
	}
	hints := searchHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.graph" {
		t.Errorf("tool = %q, want sense.graph", hints[0].Tool)
	}
	if hints[0].Args["symbol"] != "auth.Verify" {
		t.Errorf("args.symbol = %q, want auth.Verify", hints[0].Args["symbol"])
	}
}

func TestSearchHintsFileCluster(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "Foo", File: "internal/models/user.go", Score: 0.5},
			{Symbol: "Bar", File: "internal/models/user.go", Score: 0.4},
			{Symbol: "Baz", File: "internal/models/user.go", Score: 0.3},
		},
	}
	hints := searchHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.conventions" {
		t.Errorf("tool = %q, want sense.conventions", hints[0].Tool)
	}
	if hints[0].Args["domain"] != "internal/models" {
		t.Errorf("args.domain = %q, want internal/models", hints[0].Args["domain"])
	}
}

func TestSearchHintsStrongMatchAndCluster(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "Foo", File: "internal/models/user.go", Score: 0.95},
			{Symbol: "Bar", File: "internal/models/user.go", Score: 0.8},
			{Symbol: "Baz", File: "internal/models/user.go", Score: 0.7},
		},
	}
	hints := searchHints(resp)
	if len(hints) != 2 {
		t.Fatalf("want 2 hints (graph + conventions), got %d", len(hints))
	}
	if hints[0].Tool != "sense.graph" {
		t.Errorf("hints[0].tool = %q, want sense.graph", hints[0].Tool)
	}
	if hints[1].Tool != "sense.conventions" {
		t.Errorf("hints[1].tool = %q, want sense.conventions", hints[1].Tool)
	}
}

func TestSearchHintsEmpty(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "Foo", File: "a.go", Score: 0.3},
			{Symbol: "Bar", File: "b.go", Score: 0.2},
		},
	}
	hints := searchHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints, got %d", len(hints))
	}
}

func TestSearchHintsEmptyResults(t *testing.T) {
	resp := mcpio.SearchResponse{Results: []mcpio.SearchResultEntry{}}
	hints := searchHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints for empty results, got %d", len(hints))
	}
}

func TestSearchHintsClusterDeterminism(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "A", File: "pkg/alpha.go", Score: 0.5},
			{Symbol: "B", File: "pkg/beta.go", Score: 0.4},
			{Symbol: "C", File: "pkg/beta.go", Score: 0.3},
			{Symbol: "D", File: "pkg/beta.go", Score: 0.3},
			{Symbol: "E", File: "pkg/alpha.go", Score: 0.2},
			{Symbol: "F", File: "pkg/alpha.go", Score: 0.2},
		},
	}
	first := searchHints(resp)
	for i := 0; i < 20; i++ {
		got := searchHints(resp)
		if len(got) != len(first) {
			t.Fatalf("iteration %d: length changed from %d to %d", i, len(first), len(got))
		}
		for j := range first {
			if got[j].Tool != first[j].Tool || got[j].Reason != first[j].Reason {
				t.Fatalf("iteration %d: hint[%d] changed", i, j)
			}
		}
	}
	if len(first) != 1 {
		t.Fatalf("want 1 hint, got %d", len(first))
	}
	if first[0].Args["domain"] != "pkg" {
		t.Errorf("expected first qualifying file's dir (pkg/beta.go → pkg), got %q", first[0].Args["domain"])
	}
}

func TestBlastHintsHighRisk(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "User#destroy",
		Risk:          "high",
		TotalAffected: 15,
		AffectedTests: []string{"test/user_test.rb"},
	}
	hints := blastHints(resp)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.conventions" {
		t.Errorf("tool = %q, want sense.conventions", hints[0].Tool)
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
	if hints[0].Tool != "sense.search" {
		t.Errorf("tool = %q, want sense.search", hints[0].Tool)
	}
}

func TestBlastHintsHighRiskNoTests(t *testing.T) {
	resp := mcpio.BlastResponse{
		Symbol:        "Critical#method",
		Risk:          "high",
		TotalAffected: 10,
		AffectedTests: []string{},
	}
	hints := blastHints(resp)
	if len(hints) != 2 {
		t.Fatalf("want 2 hints, got %d", len(hints))
	}
	if hints[0].Tool != "sense.conventions" {
		t.Errorf("hints[0].tool = %q, want sense.conventions", hints[0].Tool)
	}
	if hints[1].Tool != "sense.search" {
		t.Errorf("hints[1].tool = %q, want sense.search", hints[1].Tool)
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

func TestConventionsHintsStrongConvention(t *testing.T) {
	resp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Description: "models inherit from Base", Strength: 0.5},
			{Description: "controllers follow REST", Strength: 0.85},
		},
	}
	hints := conventionsHints(resp, "")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.search" {
		t.Errorf("tool = %q, want sense.search", hints[0].Tool)
	}
	if hints[0].Args["query"] != "controllers follow REST" {
		t.Errorf("args.query = %q, want description of strong convention", hints[0].Args["query"])
	}
}

func TestConventionsHintsDomainScoped(t *testing.T) {
	resp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Description: "naming pattern", Strength: 0.4},
		},
	}
	hints := conventionsHints(resp, "models")
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.conventions" {
		t.Errorf("tool = %q, want sense.conventions", hints[0].Tool)
	}
}

func TestConventionsHintsStrongAndDomain(t *testing.T) {
	resp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Description: "strong pattern", Strength: 0.9},
		},
	}
	hints := conventionsHints(resp, "controllers")
	if len(hints) != 2 {
		t.Fatalf("want 2 hints, got %d", len(hints))
	}
	if hints[0].Tool != "sense.search" {
		t.Errorf("hints[0].tool = %q, want sense.search", hints[0].Tool)
	}
	if hints[1].Tool != "sense.conventions" {
		t.Errorf("hints[1].tool = %q, want sense.conventions", hints[1].Tool)
	}
}

func TestConventionsHintsEmpty(t *testing.T) {
	resp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{
			{Description: "weak pattern", Strength: 0.3},
		},
	}
	hints := conventionsHints(resp, "")
	if hints != nil {
		t.Fatalf("want nil hints, got %d", len(hints))
	}
}

func TestStatusHintsStaleFiles(t *testing.T) {
	stale := 5
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp, 3)
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

func TestStatusHintsFirstQuery(t *testing.T) {
	stale := 0
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp, 0)
	if len(hints) != 1 {
		t.Fatalf("want 1 hint, got %d", len(hints))
	}
	if hints[0].Tool != "sense.conventions" {
		t.Errorf("tool = %q, want sense.conventions", hints[0].Tool)
	}
}

func TestStatusHintsStaleAndFirstQuery(t *testing.T) {
	stale := 3
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp, 0)
	if len(hints) != 2 {
		t.Fatalf("want 2 hints, got %d", len(hints))
	}
	if hints[0].Tool != "" {
		t.Errorf("hints[0].tool = %q, want empty (advisory)", hints[0].Tool)
	}
	if hints[1].Tool != "sense.conventions" {
		t.Errorf("hints[1].tool = %q, want sense.conventions", hints[1].Tool)
	}
}

func TestStatusHintsEmpty(t *testing.T) {
	stale := 0
	resp := mcpio.StatusResponse{
		Freshness: mcpio.Freshness{
			StaleFilesSeen: &stale,
		},
	}
	hints := statusHints(resp, 5)
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
	firstStatus := statusHints(statusResp, 0)

	for i := 0; i < 50; i++ {
		assertSameHints(t, "graphHints", firstGraph, graphHints(graphResp, "callers"))
		assertSameHints(t, "blastHints", firstBlast, blastHints(blastResp))
		assertSameHints(t, "conventionsHints", firstConv, conventionsHints(convResp, "models"))
		assertSameHints(t, "statusHints", firstStatus, statusHints(statusResp, 0))
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
