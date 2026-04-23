package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
	"golang.org/x/sync/errgroup"
)

// Result is a single fused search hit with metadata from both backends.
type Result struct {
	SymbolID  int64
	Name      string
	Qualified string
	Kind      string
	FileID    int64
	LineStart int
	Snippet   string
	Score     float64
}

// Options controls search behavior.
type Options struct {
	Query    string
	Limit    int
	Language string
	MinScore float64
}

// Engine orchestrates hybrid search: keyword (FTS5) and vector (HNSW)
// in parallel, fused with reciprocal rank fusion, re-ranked by graph
// centrality.
type Engine struct {
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

// Search runs hybrid search and returns fused, re-ranked results.
func (e *Engine) Search(ctx context.Context, opts Options) ([]Result, int, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.0
	}

	symbolCount, err := e.adapter.SymbolCount(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("search: %w", err)
	}

	// Fetch more candidates than requested so RRF has enough to fuse.
	candidateLimit := opts.Limit * 5
	if candidateLimit < 50 {
		candidateLimit = 50
	}

	var keywordResults []sqlite.SearchResult
	var vectorResults []VectorResult

	canVector := e.vectors != nil && e.vectors.Len() > 0 && e.embedder != nil

	if canVector {
		g, gctx := errgroup.WithContext(ctx)

		g.Go(func() error {
			var err error
			keywordResults, err = e.adapter.KeywordSearch(gctx, opts.Query, opts.Language, candidateLimit)
			return err
		})

		g.Go(func() error {
			queryVec, err := EmbedQuery(gctx, e.embedder, opts.Query)
			if err != nil {
				return err
			}
			vectorResults = e.vectors.Search(queryVec, candidateLimit)
			return nil
		})

		if err := g.Wait(); err != nil {
			return nil, 0, fmt.Errorf("search: %w", err)
		}
	} else {
		keywordResults, err = e.adapter.KeywordSearch(ctx, opts.Query, opts.Language, candidateLimit)
		if err != nil {
			return nil, 0, fmt.Errorf("search: %w", err)
		}
	}

	fused := fuseRRF(keywordResults, vectorResults)

	// Hydrate metadata for vector-only results that have no name/qualified.
	if err := e.hydrateResults(ctx, fused); err != nil {
		return nil, 0, fmt.Errorf("search: %w", err)
	}

	// Graph centrality re-ranking.
	symbolIDs := make([]int64, len(fused))
	for i, r := range fused {
		symbolIDs[i] = r.SymbolID
	}
	centrality, err := e.adapter.InboundEdgeCounts(ctx, symbolIDs)
	if err != nil {
		return nil, 0, fmt.Errorf("search centrality: %w", err)
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
		return nil, 0, fmt.Errorf("search paths: %w", err)
	}
	applyPathWeights(fused, pathByID)

	normalizeScores(fused)

	// Sort by final score descending.
	sort.Slice(fused, func(i, j int) bool {
		return fused[i].Score > fused[j].Score
	})

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

	return results, symbolCount, nil
}

const rrfK = 60

// fuseRRF merges keyword and vector result lists using reciprocal rank
// fusion: score(symbol) = Σ 1/(k + rank_in_list). Symbols appearing
// in both lists get contributions from both, naturally ranking higher.
func fuseRRF(keyword []sqlite.SearchResult, vector []VectorResult) []Result {
	type entry struct {
		result Result
		score  float64
	}
	merged := make(map[int64]*entry)

	for rank, kr := range keyword {
		id := kr.SymbolID
		rrfScore := 1.0 / float64(rrfK+rank+1)
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

	for rank, vr := range vector {
		id := vr.SymbolID
		rrfScore := 1.0 / float64(rrfK+rank+1)
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

// applyGraphCentrality boosts scores by graph importance. Symbols with
// more inbound edges (callers, inheritors, testers) are hub nodes and
// rank higher. The boost is additive and scaled to not overwhelm the
// RRF scores: boost = log2(1 + inbound_count) * 0.001.
func applyGraphCentrality(results []Result, centrality map[int64]int) {
	if len(centrality) == 0 {
		return
	}
	for i := range results {
		if count, ok := centrality[results[i].SymbolID]; ok && count > 0 {
			results[i].Score += math.Log2(1+float64(count)) * 0.001
		}
	}
}
