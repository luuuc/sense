package search

import (
	"math"
	"strings"
	"unicode"
)

// normalizeScores applies min-max normalization to map scores into [0, 1].
// Preserves rank order. Single result gets 1.0; tied scores all get 1.0.
func normalizeScores(results []Result) {
	if len(results) <= 1 {
		for i := range results {
			results[i].Score = 1.0
		}
		return
	}
	minScore, maxScore := results[0].Score, results[0].Score
	for _, r := range results[1:] {
		if r.Score < minScore {
			minScore = r.Score
		}
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}
	span := maxScore - minScore
	if span == 0 {
		for i := range results {
			results[i].Score = 1.0
		}
		return
	}
	for i := range results {
		results[i].Score = (results[i].Score - minScore) / span
	}
}

const (
	testPathDemotion = 0.5 // symbols in test file paths
	testNameDemotion = 0.3 // symbols whose name signals test/mock
)

var testNamePrefixes = []string{"Test", "Mock", "Fake", "Stub"}

// demote applies a multiplicative penalty (expected in [0,1]) that lowers a
// result's rank. It guards the demotion invariant against a signed score ever
// re-entering the pipeline: on a positive score it returns score*penalty (a
// demotion), but a negative score — where ×penalty would raise the value
// toward zero and silently invert the demotion into a promotion — is returned
// unchanged. Today every score reaching these passes is non-negative (RRF is a
// sum of positive reciprocals, and the cross-encoder that once produced
// negative logits has been removed), so this is cheap insurance, not a live
// fix. A zero score is also returned unchanged (0*penalty == 0).
func demote(score, penalty float64) float64 {
	if score <= 0 {
		return score
	}
	return score * penalty
}

// applyKindWeights demotes modules (namespaces, rarely the search target)
// and applies a pre-normalization test-name penalty. This pre-norm pass
// is NOT redundant with applyTestDemotion (which runs post-norm): it
// keeps a verbatim-matching test from becoming the normalize-max, since
// normalizeScores pins the lowest score to 0 and the multiplicative
// centrality boost cannot lift a 0 — so without it an implementation
// that is the weakest keyword match gets stuck at the bottom. The two
// passes are complementary: this one shapes which symbol anchors the
// rescale; applyTestDemotion is the decisive reorder afterward.
func applyKindWeights(results []Result) {
	for i := range results {
		if results[i].Kind == "module" {
			results[i].Score = demote(results[i].Score, 0.5)
		}
		for _, prefix := range testNamePrefixes {
			if strings.HasPrefix(results[i].Name, prefix) {
				results[i].Score = demote(results[i].Score, testNameDemotion)
				break
			}
		}
	}
}

// demotedPathSegments lists path segments for infrastructure and test
// code that should be ranked below application code. Matched with
// strings.Contains to catch nested paths (e.g. plugins/chat/spec/).
var demotedPathSegments = []struct {
	segment string
	penalty float64
}{
	{"db/migrate/", 0.3},
	{"db/post_migrate/", 0.3},
	{"script/", 0.3},
	{"scripts/", 0.3},
	{"/test/", testPathDemotion},
	{"/tests/", testPathDemotion},
	{"/spec/", testPathDemotion},
	{"/mock/", testPathDemotion},
	{"/mocks/", testPathDemotion},
	{"/fixture/", testPathDemotion},
	{"/fixtures/", testPathDemotion},
	{"/generated/", 0.4},
	{"/testdata/", testPathDemotion},
	{"_test.rb", testPathDemotion},
	{"_spec.rb", testPathDemotion},
	{"_test.go", testPathDemotion},
	{".test.ts", testPathDemotion},
	{".test.js", testPathDemotion},
	{".spec.ts", testPathDemotion},
	{".spec.js", testPathDemotion},
	{"Test.java", testPathDemotion},
	{"Test.kt", testPathDemotion},
	{"_test.py", testPathDemotion},
	{"/__tests__/", testPathDemotion},
}

var demotedPathPrefixes = []struct {
	prefix  string
	penalty float64
}{
	{"spec/", testPathDemotion},
	{"test/", testPathDemotion},
}

// boostedPathPrefixes lists path prefixes for primary source directories
// that get a mild ranking boost.
var boostedPathPrefixes = []string{
	"app/",
	"lib/",
	"src/",
}

const sourceBoost = 1.1

// applyPathWeights demotes symbols in infrastructure/test paths and
// boosts symbols in primary source directories.
func applyPathWeights(results []Result, pathByID map[int64]string) {
	for i := range results {
		path, ok := pathByID[results[i].FileID]
		if !ok {
			continue
		}
		demoted := false
		for _, d := range demotedPathPrefixes {
			if strings.HasPrefix(path, d.prefix) {
				results[i].Score = demote(results[i].Score, d.penalty)
				demoted = true
				break
			}
		}
		if !demoted {
			for _, d := range demotedPathSegments {
				if strings.Contains(path, d.segment) {
					results[i].Score = demote(results[i].Score, d.penalty)
					demoted = true
					break
				}
			}
		}
		if !demoted {
			for _, prefix := range boostedPathPrefixes {
				if strings.HasPrefix(path, prefix) {
					results[i].Score *= sourceBoost
					break
				}
			}
		}
	}
}

// testRankPenalty multiplies the final, normalized score of a test or
// mock symbol. Tuned so an implementation of even modest relevance
// (normalized ~0.3) outranks a test that matched the query verbatim
// (normalized 1.0 → 0.2 after the penalty).
const testRankPenalty = 0.2

// testFilePathSegments identifies test/spec/mock paths across languages.
var testFilePathSegments = []string{
	"_test.", "_spec.", ".test.", ".spec.",
	"/test/", "/tests/", "/spec/", "/specs/",
	"/mock/", "/mocks/", "/__tests__/", "/testdata/",
}

var testFilePathPrefixes = []string{"test/", "spec/"}

// applyTestDemotion multiplies test/mock symbols' scores by
// testRankPenalty as the final ranking step, so implementations outrank
// the tests that exercise them. It is a no-op when the query is itself
// about tests (e.g. "test for the parser", "spec coverage"), where the
// caller genuinely wants test code surfaced.
func applyTestDemotion(results []Result, pathByID map[int64]string, query string) {
	if queryTargetsTests(query) {
		return
	}
	for i := range results {
		if isTestSymbol(results[i].Name) || isTestPath(pathByID[results[i].FileID]) {
			results[i].Score = demote(results[i].Score, testRankPenalty)
		}
	}
}

// testQueryWords are whole-word signals that the caller wants test code.
var testQueryWords = map[string]struct{}{
	"test": {}, "tests": {}, "testing": {},
	"spec": {}, "specs": {}, "mock": {}, "mocks": {}, "mocked": {},
}

// queryTargetsTests reports whether the query is explicitly about test
// code, in which case test results should not be demoted. It matches on
// whole words so "latest", "specification", and "inspect" do not count
// as test queries.
func queryTargetsTests(query string) bool {
	for _, tok := range strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if _, ok := testQueryWords[tok]; ok {
			return true
		}
	}
	return false
}

// isTestSymbol reports whether a symbol name signals test/mock code.
func isTestSymbol(name string) bool {
	for _, prefix := range testNamePrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// isTestPath reports whether a file path is a test/spec/mock file.
// Mirrors mcpio.IsTestPath; kept separate because search cannot import
// the mcpio marshalling layer. Keep the two in sync if patterns change.
func isTestPath(path string) bool {
	if path == "" {
		return false
	}
	for _, p := range testFilePathPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	for _, seg := range testFilePathSegments {
		if strings.Contains(path, seg) {
			return true
		}
	}
	return false
}

const vectorConfidenceTopK = 3

// vectorConfidence returns the mean cosine similarity of the top-K
// vector results. Returns 0 when there are no vector results.
func vectorConfidence(results []VectorResult) float64 {
	if len(results) == 0 {
		return 0
	}
	n := vectorConfidenceTopK
	if n > len(results) {
		n = len(results)
	}
	var sum float64
	for i := range n {
		sum += float64(results[i].Similarity)
	}
	return sum / float64(n)
}

// centralityCoefficient controls the multiplicative centrality boost.
// 50 callers → 1.28x; 200 callers → 1.39x; 1 caller → 1.05x.
const centralityCoefficient = 0.05

func applyGraphCentrality(results []Result, centrality map[int64]int) {
	if len(centrality) == 0 {
		return
	}
	for i := range results {
		if count, ok := centrality[results[i].SymbolID]; ok && count > 0 {
			boost := 1.0 + math.Log2(1+float64(count))*centralityCoefficient
			results[i].Score *= boost
			results[i].References = count
		}
	}
}
