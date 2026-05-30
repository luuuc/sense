package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
	"golang.org/x/sync/errgroup"
)

// Result is a single fused search hit with metadata from both backends.
// Source records which retrieval leg surfaced the hit so consumers can
// tell keyword matches from semantic ones — see the Source* constants.
type Result struct {
	SymbolID   int64
	Name       string
	Qualified  string
	Kind       string
	FileID     int64
	LineStart  int
	Snippet    string
	Score      float64
	References int
	Source     string
}

// Source values for Result.Source. They report provenance — which
// retrieval leg actually surfaced a hit — not where it finally ranked.
// A hit found by both the keyword (FTS5) and vector (flat index) legs is
// SourceHybrid; one injected by graph enrichment is SourceGraph; the
// substring text-fallback path sets "text" directly (see textresult.go).
const (
	SourceKeyword = "keyword"
	SourceVector  = "vector"
	SourceHybrid  = "hybrid"
	SourceGraph   = "graph"
)

// sourceLabel maps the per-leg contribution flags to a Source value.
// Keyword-only mode (no vector leg) always yields SourceKeyword because
// vec is never set.
func sourceLabel(kw, vec bool) string {
	switch {
	case kw && vec:
		return SourceHybrid
	case vec:
		return SourceVector
	default:
		return SourceKeyword
	}
}

// mergeSource combines the provenance of one symbol seen across multiple
// sub-queries. Differing non-empty legs (keyword in one, vector in
// another) mean both legs contributed somewhere, so the merge is hybrid.
func mergeSource(a, b string) string {
	switch {
	case a == b:
		return a
	case a == "":
		return b
	case b == "":
		return a
	default:
		return SourceHybrid
	}
}

// Options controls search behavior.
type Options struct {
	Query       string
	Limit       int
	Language    string
	MinScore    float64
	KeywordBias float64
	// Mode is the optional query-shape override: "" or ModeHybrid runs the
	// classifier, ModeSemantic pins NaturalLanguage (floors the vector
	// leg), ModeKeyword pins Identifier (reproduces pre-shape ranking).
	Mode string
}

// Engine orchestrates hybrid search: keyword (FTS5) and vector (exact flat
// index) in parallel, fused with reciprocal rank fusion, re-ranked by graph
// centrality. The vector index can be swapped at runtime (e.g. after
// background embedding completes) via SetVectors.
type Engine struct {
	mu       sync.RWMutex
	adapter  *sqlite.Adapter
	vectors  VectorIndex
	embedder embed.Embedder
}

// NewEngine creates a search engine. When vectors is nil or embedder
// is nil, the engine falls back to keyword-only search.
func NewEngine(adapter *sqlite.Adapter, vectors VectorIndex, embedder embed.Embedder) *Engine {
	return &Engine{
		adapter:  adapter,
		vectors:  vectors,
		embedder: embedder,
	}
}

// SetVectors swaps the in-memory vector index. Safe for concurrent use
// with Search — the next query will pick up the new index.
func (e *Engine) SetVectors(v VectorIndex) {
	e.mu.Lock()
	e.vectors = v
	e.mu.Unlock()
}

const (
	ModeHybrid  = "hybrid"
	ModeKeyword = "keyword"
	// ModeSemantic is a request-mode value for Options.Mode that pins the
	// query shape to NaturalLanguage. ModeHybrid and ModeKeyword double as
	// request-mode values; SearchMeta.Mode reuses the same strings to
	// report which retrieval legs actually ran.
	ModeSemantic = "semantic"
)

// SearchMeta carries non-result metadata from a search invocation.
type SearchMeta struct {
	SymbolCount   int
	Mode          string
	Shape         string
	KeywordWeight float64
	VectorWeight  float64
}

// Search runs hybrid search and returns fused, re-ranked results.
func (e *Engine) Search(ctx context.Context, opts Options) ([]Result, SearchMeta, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.0
	}

	symbolCount, err := e.adapter.SymbolCount(ctx)
	if err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search: %w", err)
	}

	// Fetch more candidates than requested so RRF has enough to fuse.
	candidateLimit := opts.Limit * 5
	if candidateLimit < 50 {
		candidateLimit = 50
	}

	e.mu.RLock()
	vectors := e.vectors
	e.mu.RUnlock()

	canVector := vectors != nil && vectors.Len() > 0 && e.embedder != nil

	queries := expandQuery(opts.Query)

	// Classify the ORIGINAL query once and propagate the shape to every
	// sub-query: expandQuery's identifier-split sub-queries would otherwise
	// re-classify as Identifier and silently re-introduce keyword bias on
	// an NL search.
	shape := resolveShape(opts.Mode, opts.Query)

	// Generic-token analysis: for non-Identifier queries, look up the
	// corpus document frequency of each query term so generic-only keyword
	// hits can be demoted later. Skipped for Identifier queries — the
	// keyword leg is authoritative there — which also avoids the DF lookups.
	var nonGenericQueryTerms map[string]struct{}
	if shape != ShapeIdentifier {
		terms := queryTermSet(opts.Query)
		df, dfErr := e.adapter.DocumentFrequency(ctx, terms)
		if dfErr != nil {
			return nil, SearchMeta{}, fmt.Errorf("search df: %w", dfErr)
		}
		nonGenericQueryTerms = nonGenericTerms(terms, df, symbolCount)
	}

	// Batch-embed all sub-queries in one call when in hybrid mode.
	var queryVecs [][]float32
	if canVector {
		inputs := make([]embed.EmbedInput, len(queries))
		for i, q := range queries {
			inputs[i] = embed.EmbedInput{Snippet: q}
		}
		queryVecs, err = e.embedder.Embed(ctx, inputs)
		if err != nil {
			return nil, SearchMeta{}, fmt.Errorf("search embed: %w", err)
		}
	}

	// Run each sub-query through keyword+vector pipeline and fuse per-query.
	var kwWeight, vecWeight float64
	queryResults := make([][]Result, len(queries))
	for i, q := range queries {
		var kwResults []sqlite.SearchResult
		var vecResults []VectorResult

		if canVector {
			g, gctx := errgroup.WithContext(ctx)
			g.Go(func() error {
				var err error
				kwResults, err = e.adapter.KeywordSearch(gctx, q, opts.Language, candidateLimit)
				return err
			})
			qVec := queryVecs[i]
			g.Go(func() error {
				vecResults = vectors.Search(qVec, candidateLimit)
				return nil
			})
			if err := g.Wait(); err != nil {
				return nil, SearchMeta{}, fmt.Errorf("search: %w", err)
			}
		} else {
			kwResults, err = e.adapter.KeywordSearch(ctx, q, opts.Language, candidateLimit)
			if err != nil {
				return nil, SearchMeta{}, fmt.Errorf("search: %w", err)
			}
		}

		kwResults = e.substringFallback(ctx, kwResults, q, opts.Language, candidateLimit)

		queryTerms := strings.Fields(strings.ToLower(q))
		kwFileIDs := make([]int64, 0, len(kwResults))
		kwFileSeen := map[int64]struct{}{}
		for _, r := range kwResults {
			if _, ok := kwFileSeen[r.FileID]; !ok {
				kwFileSeen[r.FileID] = struct{}{}
				kwFileIDs = append(kwFileIDs, r.FileID)
			}
		}
		kwPaths, _ := e.adapter.FilePathsByIDs(ctx, kwFileIDs)
		boostPathMatches(kwResults, queryTerms, kwPaths)

		qKwWeight, qVecWeight := 1.0, 0.0
		if canVector {
			vecConf := vectorConfidence(vecResults)
			qKwWeight, qVecWeight = shapeWeights(shape, vecConf)
		}

		// Report primary query's weights in metadata.
		if i == 0 {
			kwWeight, vecWeight = qKwWeight, qVecWeight
			if opts.KeywordBias > 0 && vecWeight > 0 {
				kwWeight = math.Min(1.0, kwWeight+opts.KeywordBias)
				vecWeight = math.Max(0.0, 1.0-kwWeight)
				qKwWeight, qVecWeight = kwWeight, vecWeight
			}
		}

		queryResults[i] = fuseRRF(kwResults, vecResults, qKwWeight, qVecWeight)
	}

	// Merge multi-query results with RRF.
	var fused []Result
	if len(queryResults) == 1 {
		fused = queryResults[0]
	} else {
		fused = mergeMultiQuery(queryResults)
	}

	// Hydrate metadata for vector-only results that have no name/qualified.
	if err := e.hydrateResults(ctx, fused); err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search: %w", err)
	}

	// Graph centrality re-ranking.
	symbolIDs := make([]int64, len(fused))
	for i, r := range fused {
		symbolIDs[i] = r.SymbolID
	}
	centrality, err := e.adapter.InboundEdgeCounts(ctx, symbolIDs)
	if err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search centrality: %w", err)
	}
	applyKindWeights(fused)

	// Path-based re-ranking: demote infrastructure code.
	fileIDs := make([]int64, 0, len(fused))
	seen := map[int64]struct{}{}
	for _, r := range fused {
		if _, ok := seen[r.FileID]; !ok {
			seen[r.FileID] = struct{}{}
			fileIDs = append(fileIDs, r.FileID)
		}
	}
	pathByID, err := e.adapter.FilePathsByIDs(ctx, fileIDs)
	if err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search paths: %w", err)
	}
	applyPathWeights(fused, pathByID)

	// Generic-token demotion: a keyword-only hit that matched the query
	// solely on high-frequency tokens (e.g. "prevent" → preventClose) is
	// noise. Applied pre-normalize, like applyKindWeights and for the same
	// reason: the fused RRF scores of single-term keyword matches are
	// tightly clustered, so the multiplier is decisive *here* but would be
	// neutralized post-normalize whenever the genuine domain match happens
	// to be the rescale floor (pinned to 0). No-op for Identifier queries
	// (nonGenericQueryTerms is nil).
	genericTokenPenalty(fused, nonGenericQueryTerms)

	normalizeScores(fused)
	applyGraphCentrality(fused, centrality)

	// Decisive test demotion, applied last so it survives the [0,1]
	// rescale above. Test symbols match a query's tokens more densely
	// than the implementation they exercise (TestParseConfig vs Parse),
	// so a pre-normalize penalty is out-voted by the raw keyword gap;
	// here every score is bounded, so the penalty reliably ranks the
	// implementation above its tests. Skipped for test-oriented queries.
	applyTestDemotion(fused, pathByID, opts.Query)

	// Sort by final score descending.
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Score > fused[j].Score
	})

	// Graph-augmented enrichment: boost callees of top results.
	fused, err = e.enrichFromGraph(ctx, fused)
	if err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search enrich: %w", err)
	}

	// Parent promotion: when 2+ methods of the same class appear in top
	// results, replace them with the parent class at the highest score.
	fused, err = e.promoteParents(ctx, fused, opts.Limit)
	if err != nil {
		return nil, SearchMeta{}, fmt.Errorf("search promote: %w", err)
	}

	// Apply min_score filter and limit.
	var results []Result
	for _, r := range fused {
		if r.Score < opts.MinScore {
			continue
		}
		results = append(results, r)
		if len(results) >= opts.Limit {
			break
		}
	}

	mode := ModeKeyword
	if canVector {
		mode = ModeHybrid
	}
	return results, SearchMeta{
		SymbolCount:   symbolCount,
		Mode:          mode,
		Shape:         shape.String(),
		KeywordWeight: kwWeight,
		VectorWeight:  vecWeight,
	}, nil
}

const rrfK = 60

const (
	confidenceHighThreshold = 0.6
	confidenceLowThreshold  = 0.4
)

// fusionWeights returns keyword and vector weights for reciprocal rank
// fusion based on vector confidence. High confidence → equal weight;
// low confidence → keyword-biased; very low → keyword-heavy but vectors
// still contribute (floor of 0.2).
func fusionWeights(vecConfidence float64) (keyword, vector float64) {
	switch {
	case vecConfidence >= confidenceHighThreshold:
		return 0.5, 0.5
	case vecConfidence >= confidenceLowThreshold:
		return 0.6, 0.4
	default:
		return 0.7, 0.3
	}
}

// fuseRRF merges keyword and vector result lists using reciprocal rank
// fusion with configurable weights: score(symbol) = Σ weight/(k + rank).
// Symbols appearing in both lists get contributions from both.
//
// The returned slice is sorted by fused score descending (ties broken by
// ascending symbol ID for determinism). This ordering is part of the
// contract: callers such as mergeMultiQuery treat each result's slice
// position as its fusion rank, so returning map-iteration order would feed
// noise into the next RRF stage instead of the weighted fusion ranking.
func fuseRRF(keyword []sqlite.SearchResult, vector []VectorResult, kwWeight, vecWeight float64) []Result {
	type entry struct {
		result Result
		score  float64
		kw     bool
		vec    bool
	}
	merged := make(map[int64]*entry)

	for rank, kr := range keyword {
		id := kr.SymbolID
		rrfScore := kwWeight / float64(rrfK+rank+1)
		if e, ok := merged[id]; ok {
			e.score += rrfScore
			e.kw = true
		} else {
			merged[id] = &entry{
				result: Result{
					SymbolID:  kr.SymbolID,
					Name:      kr.Name,
					Qualified: kr.Qualified,
					Kind:      kr.Kind,
					FileID:    kr.FileID,
					LineStart: kr.LineStart,
					Snippet:   kr.Snippet,
				},
				score: rrfScore,
				kw:    true,
			}
		}
	}

	if vecWeight > 0 {
		for rank, vr := range vector {
			id := vr.SymbolID
			rrfScore := vecWeight / float64(rrfK+rank+1)
			if e, ok := merged[id]; ok {
				e.score += rrfScore
				e.vec = true
			} else {
				merged[id] = &entry{
					result: Result{
						SymbolID: vr.SymbolID,
					},
					score: rrfScore,
					vec:   true,
				}
			}
		}
	}

	results := make([]Result, 0, len(merged))
	for _, e := range merged {
		e.result.Score = e.score
		e.result.Source = sourceLabel(e.kw, e.vec)
		results = append(results, e.result)
	}
	// Sort by fused score so callers can consume rank order (see contract
	// in the doc comment). Tie-break by symbol ID for deterministic output.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].SymbolID < results[j].SymbolID
	})
	return results
}

// hydrateResults fills in metadata (name, qualified, kind, etc.) for
// results that came from the vector backend only and lack symbol details.
func (e *Engine) hydrateResults(ctx context.Context, results []Result) error {
	var needIDs []int64
	for _, r := range results {
		if r.Qualified == "" {
			needIDs = append(needIDs, r.SymbolID)
		}
	}
	if len(needIDs) == 0 {
		return nil
	}

	symbols, err := e.adapter.SymbolsByIDs(ctx, needIDs)
	if err != nil {
		return err
	}

	for i := range results {
		if results[i].Qualified != "" {
			continue
		}
		if s, ok := symbols[results[i].SymbolID]; ok {
			results[i].Name = s.Name
			results[i].Qualified = s.Qualified
			results[i].Kind = s.Kind
			results[i].FileID = s.FileID
			results[i].LineStart = s.LineStart
			results[i].Snippet = s.Snippet
		}
	}
	return nil
}

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

const (
	enrichTopN     = 3
	enrichBoost    = 0.15
	enrichBaseScore = 0.05
)

// enrichFromGraph boosts callees of the top-N results that appear in the
// candidate set, and injects missing callees as low-score suggestions.
// Results must be sorted by score descending before calling.
func (e *Engine) enrichFromGraph(ctx context.Context, results []Result) ([]Result, error) {
	if len(results) == 0 {
		return results, nil
	}

	// Take top-N symbol IDs.
	n := enrichTopN
	if n > len(results) {
		n = len(results)
	}
	topIDs := make([]int64, n)
	for i := range n {
		topIDs[i] = results[i].SymbolID
	}

	// Fetch 1-hop callees.
	calleeMap, err := e.adapter.CalleeIDs(ctx, topIDs)
	if err != nil {
		return nil, err
	}

	// Collect all callee IDs.
	calleeSet := map[int64]struct{}{}
	for _, targets := range calleeMap {
		for _, id := range targets {
			calleeSet[id] = struct{}{}
		}
	}
	if len(calleeSet) == 0 {
		return results, nil
	}

	// Build index of existing results for fast lookup.
	existing := make(map[int64]int, len(results))
	for i, r := range results {
		existing[r.SymbolID] = i
	}

	// Boost existing candidates that are callees of top results.
	var missingIDs []int64
	for id := range calleeSet {
		if idx, ok := existing[id]; ok {
			results[idx].Score += enrichBoost
		} else {
			missingIDs = append(missingIDs, id)
		}
	}

	// Inject missing callees as graph-suggested results.
	if len(missingIDs) > 0 {
		syms, err := e.adapter.SymbolsByIDs(ctx, missingIDs)
		if err != nil {
			return nil, err
		}
		for _, sym := range syms {
			results = append(results, Result{
				SymbolID:  sym.SymbolID,
				Name:      sym.Name,
				Qualified: sym.Qualified,
				Kind:      sym.Kind,
				FileID:    sym.FileID,
				LineStart:  sym.LineStart,
				Snippet:   sym.Snippet,
				Score:     enrichBaseScore,
				Source:    SourceGraph,
			})
		}
	}

	// Re-sort after boosting and injection.
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}

const parentPromotionThreshold = 2

// promoteParents replaces multiple child methods from the same parent
// class/struct with the parent symbol when 2+ children appear in the
// top-K results. The parent inherits the highest child score.
func (e *Engine) promoteParents(ctx context.Context, results []Result, limit int) ([]Result, error) {
	if len(results) == 0 {
		return results, nil
	}

	topK := limit
	if topK > len(results) {
		topK = len(results)
	}

	topIDs := make([]int64, topK)
	for i := range topK {
		topIDs[i] = results[i].SymbolID
	}

	parents, err := e.adapter.ParentSymbols(ctx, topIDs)
	if err != nil {
		return nil, err
	}
	if len(parents) == 0 {
		return results, nil
	}

	type parentGroup struct {
		info      sqlite.ParentInfo
		children  []int
		maxScore  float64
		maxSource string
	}
	groups := map[int64]*parentGroup{}
	for i := range topK {
		pi, ok := parents[results[i].SymbolID]
		if !ok {
			continue
		}
		g, exists := groups[pi.ParentID]
		if !exists {
			g = &parentGroup{info: pi}
			groups[pi.ParentID] = g
		}
		g.children = append(g.children, i)
		if results[i].Score > g.maxScore {
			g.maxScore = results[i].Score
			g.maxSource = results[i].Source
		}
	}

	existing := map[int64]bool{}
	for _, r := range results {
		existing[r.SymbolID] = true
	}

	remove := map[int]bool{}
	var promoted []Result
	for _, g := range groups {
		if len(g.children) < parentPromotionThreshold {
			continue
		}
		if existing[g.info.ParentID] {
			continue
		}
		for _, idx := range g.children {
			remove[idx] = true
		}
		promoted = append(promoted, Result{
			SymbolID:  g.info.ParentID,
			Name:      g.info.Name,
			Qualified: g.info.Qualified,
			Kind:      g.info.Kind,
			FileID:    g.info.FileID,
			LineStart: g.info.LineStart,
			Snippet:   g.info.Snippet,
			Score:     g.maxScore,
			Source:    g.maxSource,
		})
	}

	if len(promoted) == 0 {
		return results, nil
	}

	var out []Result
	for i, r := range results {
		if !remove[i] {
			out = append(out, r)
		}
	}
	out = append(out, promoted...)

	sort.Slice(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	return out, nil
}

const substringFallbackThreshold = 5

func (e *Engine) substringFallback(ctx context.Context, kwResults []sqlite.SearchResult, query, language string, limit int) []sqlite.SearchResult {
	if len(kwResults) >= substringFallbackThreshold {
		return kwResults
	}
	subResults, err := e.adapter.SubstringSearch(ctx, query, language, limit-len(kwResults))
	if err != nil {
		return kwResults
	}
	return deduplicateResults(kwResults, subResults)
}

func deduplicateResults(primary, secondary []sqlite.SearchResult) []sqlite.SearchResult {
	seen := make(map[int64]bool, len(primary))
	for _, r := range primary {
		seen[r.SymbolID] = true
	}
	out := make([]sqlite.SearchResult, len(primary))
	copy(out, primary)
	for _, r := range secondary {
		if !seen[r.SymbolID] {
			seen[r.SymbolID] = true
			out = append(out, r)
		}
	}
	return out
}

func boostPathMatches(results []sqlite.SearchResult, queryTerms []string, pathByID map[int64]string) {
	if len(queryTerms) == 0 || len(results) == 0 {
		return
	}
	for i := range results {
		path := strings.ToLower(pathByID[results[i].FileID])
		for _, term := range queryTerms {
			if strings.Contains(path, term) {
				results[i].Score *= 1.5
				break
			}
		}
	}
}
