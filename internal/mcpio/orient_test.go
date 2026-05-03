package mcpio

import (
	"fmt"
	"testing"
)

func makeSearchHits(n int) []SearchResultEntry {
	hits := make([]SearchResultEntry, n)
	for i := range hits {
		hits[i] = SearchResultEntry{
			Symbol:  fmt.Sprintf("pkg.Function%d", i),
			File:    fmt.Sprintf("internal/pkg/file%d.go", i),
			Line:    i*10 + 1,
			Kind:    "function",
			Score:   SearchScore(1.0 - float64(i)*0.02),
			Snippet: fmt.Sprintf("func Function%d(ctx context.Context) error {", i),
		}
	}
	return hits
}

func makeLargeStructure() *StatusStructure {
	namespaces := make([]StatusNamespace, 8)
	for i := range namespaces {
		namespaces[i] = StatusNamespace{
			Name:    fmt.Sprintf("internal/pkg%d", i),
			Symbols: (8 - i) * 100,
			Kind:    "directory",
		}
	}
	hubs := make([]StatusHub, 5)
	for i := range hubs {
		hubs[i] = StatusHub{
			Name:    fmt.Sprintf("Hub%d", i),
			Callers: (5 - i) * 50,
			Kind:    "function",
			Role:    "hub",
		}
	}
	return &StatusStructure{
		Fingerprint:   "Go project. 75000 symbols.",
		TopNamespaces: namespaces,
		HubSymbols:    hubs,
		EntryPoints:   []StatusEntryPoint{{Name: "main", File: "cmd/app/main.go", Kind: "function"}},
	}
}

func TestApplyOrientTokenBudgetNoTruncation(t *testing.T) {
	resp := OrientResponse{
		Structure:   makeLargeStructure(),
		Conventions: makeConventions(3),
		SearchHits:  makeSearchHits(3),
	}
	ApplyOrientTokenBudget(&resp, 8000)
	if resp.Truncated {
		t.Error("expected truncated=false for small response")
	}
	if len(resp.Conventions) != 3 {
		t.Errorf("expected 3 conventions, got %d", len(resp.Conventions))
	}
	if len(resp.SearchHits) != 3 {
		t.Errorf("expected 3 search hits, got %d", len(resp.SearchHits))
	}
}

func TestApplyOrientTokenBudgetTrimsSearchHitsFirst(t *testing.T) {
	resp := OrientResponse{
		Structure:   makeLargeStructure(),
		Conventions: makeConventions(5),
		SearchHits:  makeSearchHits(15),
	}
	origConv := len(resp.Conventions)

	full := estimateJSONTokens(&resp)
	budget := full / 2
	ApplyOrientTokenBudget(&resp, budget)

	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
	if len(resp.SearchHits) >= 15 {
		t.Errorf("expected search hits to be trimmed, got %d", len(resp.SearchHits))
	}
	if len(resp.Conventions) > origConv {
		t.Error("conventions should not grow")
	}
	if len(resp.SearchHits) > 0 && len(resp.Conventions) < origConv {
		t.Error("conventions should not be trimmed while search hits remain")
	}
}

func TestApplyOrientTokenBudgetTinyBudget(t *testing.T) {
	resp := OrientResponse{
		Structure:   makeLargeStructure(),
		Conventions: makeConventions(5),
		SearchHits:  makeSearchHits(10),
	}
	ApplyOrientTokenBudget(&resp, 1)

	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
	if len(resp.SearchHits) != 0 {
		t.Errorf("expected 0 search hits with budget=1, got %d", len(resp.SearchHits))
	}
	if len(resp.Conventions) != 0 {
		t.Errorf("expected 0 conventions with budget=1, got %d", len(resp.Conventions))
	}
	if resp.Structure == nil {
		t.Error("structure must be preserved even at tiny budget")
	}
}

func TestApplyOrientTokenBudgetEmpty(t *testing.T) {
	resp := OrientResponse{}
	ApplyOrientTokenBudget(&resp, 8000)
	if resp.Truncated {
		t.Error("expected truncated=false for empty response")
	}
}

func TestApplyOrientTokenBudgetLargeFixtureUnderBudget(t *testing.T) {
	resp := OrientResponse{
		Fingerprint: "Go project. 75000 symbols. Heaviest areas: lib (25000), app (18000), packages (12000).",
		Structure:   makeLargeStructure(),
		Conventions: makeConventions(5),
		SearchHits:  makeSearchHits(15),
		SenseMetrics: OrientMetrics{
			SymbolsAnalyzed:           75000,
			EstimatedFileReadsAvoided: 30,
			EstimatedTokensSaved:      24000,
		},
		NextSteps: []NextStep{
			{Tool: "sense.search", Reason: "explore specific areas"},
		},
	}

	budgets := []struct {
		tier   string
		budget int
	}{
		{"small", 8000},
		{"medium", 6000},
		{"large", 4000},
	}

	for _, b := range budgets {
		t.Run(b.tier, func(t *testing.T) {
			r := resp
			r.Conventions = make([]ConventionEntry, len(resp.Conventions))
			copy(r.Conventions, resp.Conventions)
			r.SearchHits = make([]SearchResultEntry, len(resp.SearchHits))
			copy(r.SearchHits, resp.SearchHits)

			ApplyOrientTokenBudget(&r, b.budget)

			actual := estimateJSONTokens(&r)
			if actual > b.budget {
				t.Errorf("tier %s: response %d tokens exceeds budget %d", b.tier, actual, b.budget)
			}
			if r.Structure == nil {
				t.Errorf("tier %s: structure must be preserved", b.tier)
			}
		})
	}
}
