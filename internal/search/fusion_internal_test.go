package search

import (
	"sort"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestFusionWeightsHigh(t *testing.T) {
	kw, vec := fusionWeights(0.9)
	if kw != 0.5 || vec != 0.5 {
		t.Errorf("fusionWeights(0.9) = (%v, %v), want (0.5, 0.5)", kw, vec)
	}
}

func TestFusionWeightsMedium(t *testing.T) {
	kw, vec := fusionWeights(0.5)
	if kw != 0.6 || vec != 0.4 {
		t.Errorf("fusionWeights(0.5) = (%v, %v), want (0.6, 0.4)", kw, vec)
	}
}

func TestFusionWeightsLow(t *testing.T) {
	kw, vec := fusionWeights(0.2)
	if kw != 0.7 || vec != 0.3 {
		t.Errorf("fusionWeights(0.2) = (%v, %v), want (0.7, 0.3)", kw, vec)
	}
}

func TestVectorConfidenceEmpty(t *testing.T) {
	got := vectorConfidence(nil)
	if got != 0 {
		t.Errorf("vectorConfidence(nil) = %v, want 0", got)
	}
}

func TestVectorConfidenceSingleResult(t *testing.T) {
	results := []VectorResult{{SymbolID: 1, Similarity: 0.8}}
	got := vectorConfidence(results)
	if got < 0.79 || got > 0.81 {
		t.Errorf("vectorConfidence = %v, want ~0.8", got)
	}
}

func TestVectorConfidenceMultipleResults(t *testing.T) {
	results := []VectorResult{
		{SymbolID: 1, Similarity: 0.9},
		{SymbolID: 2, Similarity: 0.6},
		{SymbolID: 3, Similarity: 0.3},
		{SymbolID: 4, Similarity: 0.1},
	}
	// Top-3 mean: (0.9 + 0.6 + 0.3) / 3 = 0.6
	got := vectorConfidence(results)
	if got < 0.59 || got > 0.61 {
		t.Errorf("vectorConfidence = %v, want ~0.6", got)
	}
}

func TestMergeMultiQuerySingleList(t *testing.T) {
	input := [][]Result{
		{
			{SymbolID: 1, Name: "a", Score: 1.0},
			{SymbolID: 2, Name: "b", Score: 0.5},
		},
	}
	got := mergeMultiQuery(input)
	if len(got) != 2 {
		t.Fatalf("mergeMultiQuery returned %d results, want 2", len(got))
	}
}

func TestMergeMultiQueryOverlapping(t *testing.T) {
	input := [][]Result{
		{
			{SymbolID: 1, Name: "shared"},
			{SymbolID: 2, Name: "only-first"},
		},
		{
			{SymbolID: 1, Name: "shared"},
			{SymbolID: 3, Name: "only-second"},
		},
	}
	got := mergeMultiQuery(input)
	if len(got) != 3 {
		t.Fatalf("mergeMultiQuery returned %d results, want 3", len(got))
	}
	// The shared symbol (id=1) should have a higher score than either unique symbol
	scoreMap := map[int64]float64{}
	for _, r := range got {
		scoreMap[r.SymbolID] = r.Score
	}
	if scoreMap[1] <= scoreMap[2] || scoreMap[1] <= scoreMap[3] {
		t.Errorf("shared symbol (id=1) should have higher score: %v", scoreMap)
	}
}

func TestFuseRRFKeywordOnly(t *testing.T) {
	kw := []sqlite.SearchResult{
		{SymbolID: 1, Name: "first", Qualified: "pkg.first"},
		{SymbolID: 2, Name: "second", Qualified: "pkg.second"},
	}
	got := fuseRRF(kw, nil, 1.0, 0.0)
	if len(got) != 2 {
		t.Fatalf("fuseRRF returned %d, want 2", len(got))
	}
	// Sort by score descending
	sort.Slice(got, func(i, j int) bool { return got[i].Score > got[j].Score })
	if got[0].SymbolID != 1 {
		t.Error("first result should be symbol 1 (rank 0)")
	}
}

func TestFuseRRFBothSources(t *testing.T) {
	kw := []sqlite.SearchResult{
		{SymbolID: 1, Name: "shared"},
		{SymbolID: 2, Name: "keyword-only"},
	}
	vec := []VectorResult{
		{SymbolID: 1, Similarity: 0.9},
		{SymbolID: 3, Similarity: 0.8},
	}
	got := fuseRRF(kw, vec, 0.5, 0.5)
	if len(got) != 3 {
		t.Fatalf("fuseRRF returned %d, want 3", len(got))
	}
	scoreMap := map[int64]float64{}
	for _, r := range got {
		scoreMap[r.SymbolID] = r.Score
	}
	// Symbol 1 appears in both sources, should have highest score
	if scoreMap[1] <= scoreMap[2] || scoreMap[1] <= scoreMap[3] {
		t.Errorf("shared symbol should have highest score: %v", scoreMap)
	}
}

func TestApplyGraphCentralityNoData(t *testing.T) {
	results := []Result{{SymbolID: 1, Score: 1.0}}
	// Should not panic with nil/empty centrality
	applyGraphCentrality(results, nil)
	if results[0].Score != 1.0 {
		t.Errorf("score changed with nil centrality: %v", results[0].Score)
	}
}

func TestApplyGraphCentralityBoost(t *testing.T) {
	results := []Result{
		{SymbolID: 1, Score: 1.0},
		{SymbolID: 2, Score: 1.0},
	}
	centrality := map[int64]int{
		1: 50, // 50 callers should boost
	}
	applyGraphCentrality(results, centrality)
	if results[0].Score <= 1.0 {
		t.Error("symbol with 50 callers should be boosted")
	}
	if results[0].References != 50 {
		t.Errorf("References = %d, want 50", results[0].References)
	}
	if results[1].Score != 1.0 {
		t.Error("symbol without callers should not be boosted")
	}
}

func TestBoostPathMatches(t *testing.T) {
	results := []sqlite.SearchResult{
		{SymbolID: 1, FileID: 10, Score: 1.0},
		{SymbolID: 2, FileID: 20, Score: 1.0},
		{SymbolID: 3, FileID: 30, Score: 1.0},
	}
	paths := map[int64]string{
		10: "internal/auth/handler.go",
		20: "internal/search/engine.go",
		30: "internal/auth/middleware.go",
	}
	boostPathMatches(results, []string{"auth"}, paths)

	if results[0].Score != 1.5 {
		t.Errorf("auth file score = %v, want 1.5", results[0].Score)
	}
	if results[1].Score != 1.0 {
		t.Errorf("non-auth file score = %v, want 1.0", results[1].Score)
	}
	if results[2].Score != 1.5 {
		t.Errorf("auth middleware score = %v, want 1.5", results[2].Score)
	}
}

func TestBoostPathMatchesEmpty(_ *testing.T) {
	boostPathMatches(nil, []string{"auth"}, nil)
	boostPathMatches([]sqlite.SearchResult{{SymbolID: 1}}, nil, nil)
}

func TestDeduplicateResults(t *testing.T) {
	primary := []sqlite.SearchResult{
		{SymbolID: 1, Score: 2.0},
		{SymbolID: 2, Score: 1.5},
	}
	secondary := []sqlite.SearchResult{
		{SymbolID: 2, Score: 1.0}, // duplicate
		{SymbolID: 3, Score: 0.5}, // new
	}
	merged := deduplicateResults(primary, secondary)
	if len(merged) != 3 {
		t.Fatalf("expected 3 results, got %d", len(merged))
	}
	ids := map[int64]bool{}
	for _, r := range merged {
		ids[r.SymbolID] = true
	}
	for _, id := range []int64{1, 2, 3} {
		if !ids[id] {
			t.Errorf("missing symbol ID %d", id)
		}
	}
}
