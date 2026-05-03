package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
	"golang.org/x/sync/errgroup"
)

// Reranker scores (query, document) pairs for relevance using a
// cross-encoder that sees both inputs jointly.
type Reranker interface {
	Score(query string, docs []string) ([]float32, error)
}

// Result is a single fused search hit with metadata from both backends.
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
}

// Options controls search behavior.
type Options struct {
	Query       string
	Limit       int
	Language    string
	MinScore    float64
	KeywordBias float64
}

// Engine orchestrates hybrid search: keyword (FTS5) and vector (HNSW)
// in parallel, fused with reciprocal rank fusion, re-ranked by graph
// centrality. The vector index can be swapped at runtime (e.g. after
// background embedding completes) via SetVectors.
type Engine struct {
	mu       sync.RWMutex
	adapter  *sqlite.Adapter
	vectors  VectorIndex
	embedder embed.Embedder
	reranker Reranker
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

// SetReranker sets the cross-encoder reranker. When non-nil, search
// results are reranked after RRF fusion for improved precision.
func (e *Engine) SetReranker(r Reranker) {
	e.mu.Lock()
	e.reranker = r
	e.mu.Unlock()
}

const (
	ModeHybrid  = "hybrid"
	ModeKeyword = "keyword"
)

// SearchMeta carries non-result metadata from a search invocation.
type SearchMeta struct {
	SymbolCount   int
	Mode          string
	KeywordWeight float64
	VectorWeight  float64
	Reranked      bool
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
	reranker := e.reranker
	e.mu.RUnlock()

	canVector := vectors != nil && vectors.Len() > 0 && e.embedder != nil

	queries := expandQuery(opts.Query)

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

		qKwWeight, qVecWeight := 1.0, 0.0
		if canVector {
			vecConf := vectorConfidence(vecResults)
			qKwWeight, qVecWeight = fusionWeights(vecConf)
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

	// Cross-encoder reranking: replace RRF scores with joint relevance scores.
	reranked := false
	if reranker != nil && len(fused) > 0 {
		docs := make([]string, len(fused))
		for i, r := range fused {
			docs[i] = r.Kind + " " + r.Qualified
			if r.Snippet != "" {
				docs[i] += "\n" + r.Snippet
			}
		}
		scores, rerankErr := reranker.Score(opts.Query, docs)
		if rerankErr != nil {
			return nil, SearchMeta{}, fmt.Errorf("search rerank: %w", rerankErr)
		}
		for i := range fused {
			fused[i].Score = float64(scores[i])
		}
		reranked = true
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
	applyGraphCentrality(fused, centrality)
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

	normalizeScores(fused)

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
		KeywordWeight: kwWeight,
		VectorWeight:  vecWeight,
		Reranked:      reranked,
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
		return 0.7, 0.3
	default:
		return 0.8, 0.2
	}
}

// fuseRRF merges keyword and vector result lists using reciprocal rank
// fusion with configurable weights: score(symbol) = Σ weight/(k + rank).
// Symbols appearing in both lists get contributions from both.
func fuseRRF(keyword []sqlite.SearchResult, vector []VectorResult, kwWeight, vecWeight float64) []Result {
	type entry struct {
		result Result
		score  float64
	}
	merged := make(map[int64]*entry)

	for rank, kr := range keyword {
		id := kr.SymbolID
		rrfScore := kwWeight / float64(rrfK+rank+1)
		if e, ok := merged[id]; ok {
			e.score += rrfScore
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
			}
		}
	}

	if vecWeight > 0 {
		for rank, vr := range vector {
			id := vr.SymbolID
			rrfScore := vecWeight / float64(rrfK+rank+1)
			if e, ok := merged[id]; ok {
				e.score += rrfScore
			} else {
				merged[id] = &entry{
					result: Result{
						SymbolID: vr.SymbolID,
					},
					score: rrfScore,
				}
			}
		}
	}

	results := make([]Result, 0, len(merged))
	for _, e := range merged {
		e.result.Score = e.score
		results = append(results, e.result)
	}
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

// applyKindWeights demotes namespace-level symbols (modules) which
// tend to appear in many files and dominate search results over the
// specific classes/methods users actually want.
func applyKindWeights(results []Result) {
	for i := range results {
		if results[i].Kind == "module" {
			results[i].Score *= 0.5
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
	{"/test/", 0.5},
	{"/tests/", 0.5},
	{"/spec/", 0.5},
	{"/mock/", 0.5},
	{"/mocks/", 0.5},
	{"/fixture/", 0.5},
	{"/fixtures/", 0.5},
	{"/generated/", 0.4},
	{"/testdata/", 0.5},
	{"_test.rb", 0.5},
	{"_spec.rb", 0.5},
	{"_test.go", 0.5},
	{".test.ts", 0.5},
	{".test.js", 0.5},
	{".spec.ts", 0.5},
	{".spec.js", 0.5},
	{"Test.java", 0.5},
	{"Test.kt", 0.5},
	{"_test.py", 0.5},
	{"/__tests__/", 0.5},
}

// demotedPathPrefixes lists root-level test/spec directories that should
// be demoted. Separate from demotedPathSegments because these must use
// HasPrefix to avoid false positives (e.g. "spec/" inside "specification/").
var demotedPathPrefixes = []struct {
	prefix  string
	penalty float64
}{
	{"spec/", 0.5},
	{"test/", 0.5},
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
				results[i].Score *= d.penalty
				demoted = true
				break
			}
		}
		if !demoted {
			for _, d := range demotedPathSegments {
				if strings.Contains(path, d.segment) {
					results[i].Score *= d.penalty
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

// applyGraphCentrality boosts scores by graph importance. Symbols with
// more inbound edges (callers, inheritors, testers) are hub nodes and
// rank higher. The boost is additive and log-scaled:
// boost = log2(1 + inbound_count) * 0.01.
func applyGraphCentrality(results []Result, centrality map[int64]int) {
	if len(centrality) == 0 {
		return
	}
	for i := range results {
		if count, ok := centrality[results[i].SymbolID]; ok && count > 0 {
			results[i].Score += math.Log2(1+float64(count)) * 0.01
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
		info     sqlite.ParentInfo
		children []int
		maxScore float64
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
