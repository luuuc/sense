package search

import (
	"math"
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

func TestFuseRRFReturnsSortedByScore(t *testing.T) {
	// Keyword rank determines RRF score (1/(k+rank+1)), strictly decreasing
	// with position. fuseRRF must return results already sorted by score —
	// callers consume slice position as rank, so map-iteration order would be
	// noise. The test does NOT sort: it asserts the production sort holds.
	const n = 12
	kw := make([]sqlite.SearchResult, n)
	for i := range kw {
		kw[i] = sqlite.SearchResult{SymbolID: int64(i + 1), Name: "s"}
	}

	got := fuseRRF(kw, nil, 1.0, 0.0)
	if len(got) != n {
		t.Fatalf("fuseRRF returned %d, want %d", len(got), n)
	}
	// Strictly descending by score, and rank-0 symbol (id=1) leads.
	for i := 1; i < len(got); i++ {
		if got[i-1].Score < got[i].Score {
			t.Fatalf("not sorted at %d: %v >= %v expected (order %v)",
				i, got[i-1].Score, got[i].Score, ids(got))
		}
	}
	if got[0].SymbolID != 1 {
		t.Errorf("top result = symbol %d, want symbol 1 (keyword rank 0)", got[0].SymbolID)
	}
}

func TestMergeMultiQueryRanksByPosition(t *testing.T) {
	// mergeMultiQuery uses each input's slice position as its RRF rank. A
	// symbol at rank 0 in both sub-queries must outscore one at rank 5 in
	// both — proving position, not map order, drives the merged score.
	mk := func(early, late int64) []Result {
		out := []Result{{SymbolID: early, Name: "early"}}
		for i := 0; i < 4; i++ {
			out = append(out, Result{SymbolID: int64(100 + i), Name: "filler"})
		}
		out = append(out, Result{SymbolID: late, Name: "late"})
		return out
	}
	got := mergeMultiQuery([][]Result{mk(1, 2), mk(1, 2)})

	score := map[int64]float64{}
	for _, r := range got {
		score[r.SymbolID] = r.Score
	}
	if score[1] <= score[2] {
		t.Errorf("rank-0 symbol (%.5f) should outscore rank-5 symbol (%.5f)", score[1], score[2])
	}
}

func ids(rs []Result) []int64 {
	out := make([]int64, len(rs))
	for i, r := range rs {
		out[i] = r.SymbolID
	}
	return out
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
	srcMap := map[int64]string{}
	for _, r := range got {
		scoreMap[r.SymbolID] = r.Score
		srcMap[r.SymbolID] = r.Source
	}
	// Symbol 1 appears in both sources, should have highest score
	if scoreMap[1] <= scoreMap[2] || scoreMap[1] <= scoreMap[3] {
		t.Errorf("shared symbol should have highest score: %v", scoreMap)
	}
	// Provenance must be honest: 1 from both legs, 2 keyword-only, 3 vector-only.
	if srcMap[1] != SourceHybrid {
		t.Errorf("symbol 1 source = %q, want %q", srcMap[1], SourceHybrid)
	}
	if srcMap[2] != SourceKeyword {
		t.Errorf("symbol 2 source = %q, want %q", srcMap[2], SourceKeyword)
	}
	if srcMap[3] != SourceVector {
		t.Errorf("symbol 3 source = %q, want %q", srcMap[3], SourceVector)
	}
}

func TestFuseRRFKeywordOnlyTagsSource(t *testing.T) {
	kw := []sqlite.SearchResult{{SymbolID: 1, Name: "a"}, {SymbolID: 2, Name: "b"}}
	// vecWeight 0 means the vector leg is ignored entirely (keyword-only mode).
	got := fuseRRF(kw, []VectorResult{{SymbolID: 1, Similarity: 0.9}}, 1.0, 0.0)
	for _, r := range got {
		if r.Source != SourceKeyword {
			t.Errorf("symbol %d source = %q, want %q", r.SymbolID, r.Source, SourceKeyword)
		}
	}
}

func TestFuseRRFKeywordDuplicateAccumulates(t *testing.T) {
	// A symbol appearing twice in the keyword list accumulates both RRF
	// contributions and stays keyword-sourced.
	kw := []sqlite.SearchResult{
		{SymbolID: 1, Name: "dup"},
		{SymbolID: 1, Name: "dup"},
		{SymbolID: 2, Name: "other"},
	}
	got := fuseRRF(kw, nil, 1.0, 0.0)
	var dup, other Result
	for _, r := range got {
		switch r.SymbolID {
		case 1:
			dup = r
		case 2:
			other = r
		}
	}
	if dup.Source != SourceKeyword {
		t.Errorf("dup source = %q, want %q", dup.Source, SourceKeyword)
	}
	if dup.Score <= other.Score {
		t.Errorf("twice-listed symbol should outscore single: dup=%v other=%v", dup.Score, other.Score)
	}
}

func TestQueryTargetsTests(t *testing.T) {
	yes := []string{"test for the parser", "spec coverage", "mock server", "unit Test", "test_helpers setup"}
	// Whole-word matching: these contain test/spec/mock as substrings but
	// are not about tests.
	no := []string{"build the scanner", "how does auth work", "payment flow",
		"latest changes", "specification format", "inspect the request", "hammock config"}
	for _, q := range yes {
		if !queryTargetsTests(q) {
			t.Errorf("queryTargetsTests(%q) = false, want true", q)
		}
	}
	for _, q := range no {
		if queryTargetsTests(q) {
			t.Errorf("queryTargetsTests(%q) = true, want false", q)
		}
	}
}

func TestIsTestSymbol(t *testing.T) {
	for _, n := range []string{"TestParse", "MockClient", "FakeDB", "StubServer"} {
		if !isTestSymbol(n) {
			t.Errorf("isTestSymbol(%q) = false, want true", n)
		}
	}
	for _, n := range []string{"Parse", "FindUser", "Authenticator"} {
		if isTestSymbol(n) {
			t.Errorf("isTestSymbol(%q) = true, want false", n)
		}
	}
}

func TestIsTestPath(t *testing.T) {
	for _, p := range []string{"internal/dead/dead_test.go", "spec/models/user_spec.rb", "test/foo.rb", "app/__tests__/x.js", "internal/scan/testdata/x.go"} {
		if !isTestPath(p) {
			t.Errorf("isTestPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"app/models/user.rb", "internal/dead/dead.go", ""} {
		if isTestPath(p) {
			t.Errorf("isTestPath(%q) = true, want false", p)
		}
	}
}

func TestApplyTestDemotion(t *testing.T) {
	paths := map[int64]string{1: "internal/dead/dead.go", 2: "internal/dead/dead_test.go", 3: "internal/x/y.go"}
	mk := func() []Result {
		return []Result{
			{SymbolID: 1, Name: "FindDead", FileID: 1, Score: 0.6},     // impl
			{SymbolID: 2, Name: "TestFindDead", FileID: 2, Score: 1.0}, // test by path+name
			{SymbolID: 3, Name: "MockThing", FileID: 3, Score: 0.9},    // test by name only
		}
	}

	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

	// Non-test query: tests demoted, impl untouched.
	r := mk()
	applyTestDemotion(r, paths, "find dead code")
	if r[0].Score != 0.6 {
		t.Errorf("impl score changed: %v", r[0].Score)
	}
	if !near(r[1].Score, 1.0*testRankPenalty) {
		t.Errorf("test-by-path score = %v, want %v", r[1].Score, testRankPenalty)
	}
	if !near(r[2].Score, 0.9*testRankPenalty) {
		t.Errorf("mock-by-name score = %v, want %v", r[2].Score, 0.9*testRankPenalty)
	}
	// After demotion the implementation outranks both tests.
	if r[0].Score <= r[1].Score || r[0].Score <= r[2].Score {
		t.Error("implementation should outrank tests after demotion")
	}

	// Test-oriented query: no demotion at all.
	r2 := mk()
	applyTestDemotion(r2, paths, "test for dead code")
	if r2[1].Score != 1.0 || r2[2].Score != 0.9 {
		t.Error("test-oriented query must not demote test results")
	}
}

func TestSourceLabel(t *testing.T) {
	cases := []struct {
		kw, vec bool
		want    string
	}{
		{true, false, SourceKeyword},
		{false, true, SourceVector},
		{true, true, SourceHybrid},
		{false, false, SourceKeyword}, // defensive default
	}
	for _, c := range cases {
		if got := sourceLabel(c.kw, c.vec); got != c.want {
			t.Errorf("sourceLabel(%v, %v) = %q, want %q", c.kw, c.vec, got, c.want)
		}
	}
}

func TestMergeSource(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{SourceKeyword, SourceKeyword, SourceKeyword},
		{SourceVector, SourceVector, SourceVector},
		{SourceKeyword, SourceVector, SourceHybrid},
		{SourceVector, SourceKeyword, SourceHybrid},
		{"", SourceVector, SourceVector},
		{SourceKeyword, "", SourceKeyword},
		{SourceHybrid, SourceKeyword, SourceHybrid},
	}
	for _, c := range cases {
		if got := mergeSource(c.a, c.b); got != c.want {
			t.Errorf("mergeSource(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestMergeMultiQueryCombinesSource(t *testing.T) {
	// Symbol 1 is keyword-sourced in query A and vector-sourced in query B:
	// the merged provenance must reflect both legs.
	queryResults := [][]Result{
		{{SymbolID: 1, Source: SourceKeyword}},
		{{SymbolID: 1, Source: SourceVector}},
	}
	got := mergeMultiQuery(queryResults)
	if len(got) != 1 {
		t.Fatalf("mergeMultiQuery returned %d, want 1", len(got))
	}
	if got[0].Source != SourceHybrid {
		t.Errorf("merged source = %q, want %q", got[0].Source, SourceHybrid)
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
