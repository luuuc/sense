package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/sqlite"
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

// searchContext is the per-invocation state assembled by prepareSearch and
// consumed by the fuse and rank phases: corpus size, the candidate budget,
// whether the vector leg can run (plus the index snapshot), the expanded
// sub-queries and their batched embeddings, the resolved query shape, and the
// non-generic term set used for token demotion.
type searchContext struct {
	symbolCount          int
	candidateLimit       int
	canVector            bool
	vectors              VectorIndex
	queries              []string
	queryVecs            [][]float32
	shape                QueryShape
	nonGenericQueryTerms map[string]struct{}
}

// Search runs hybrid search and returns fused, re-ranked results. It runs the
// pipeline in order: prepare (classify + embed) → fuse (retrieve + RRF) →
// rank (the score-mutating passes, whose order is load-bearing) → enrich
// (graph callees + parent promotion) → finalize (min-score, limit, drops).
func (e *Engine) Search(ctx context.Context, opts Options) ([]Result, SearchMeta, error) {
	if opts.Limit <= 0 {
		opts.Limit = 10
	}
	if opts.MinScore <= 0 {
		opts.MinScore = 0.0
	}

	sc, err := e.prepareSearch(ctx, opts)
	if err != nil {
		return nil, SearchMeta{}, err
	}

	fused, kwWeight, vecWeight, err := e.fuseQueries(ctx, opts, sc)
	if err != nil {
		return nil, SearchMeta{}, err
	}

	if err := e.rankResults(ctx, opts, sc, fused); err != nil {
		return nil, SearchMeta{}, err
	}

	fused, err = e.enrichResults(ctx, opts, fused)
	if err != nil {
		return nil, SearchMeta{}, err
	}

	results := finalizeResults(opts, fused)

	mode := ModeKeyword
	if sc.canVector {
		mode = ModeHybrid
	}
	return results, SearchMeta{
		SymbolCount:   sc.symbolCount,
		Mode:          mode,
		Shape:         sc.shape.String(),
		KeywordWeight: kwWeight,
		VectorWeight:  vecWeight,
	}, nil
}

// prepareSearch assembles the per-invocation searchContext: corpus size, the
// candidate budget, whether the vector leg can run, the expanded sub-queries,
// the once-classified query shape (propagated to every sub-query so identifier
// splits do not re-introduce keyword bias on an NL search), the generic-token
// set, and the batched sub-query embeddings.
func (e *Engine) prepareSearch(ctx context.Context, opts Options) (*searchContext, error) {
	symbolCount, err := e.adapter.SymbolCount(ctx)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
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
			return nil, fmt.Errorf("search df: %w", dfErr)
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
			return nil, fmt.Errorf("search embed: %w", err)
		}
	}

	return &searchContext{
		symbolCount:          symbolCount,
		candidateLimit:       candidateLimit,
		canVector:            canVector,
		vectors:              vectors,
		queries:              queries,
		queryVecs:            queryVecs,
		shape:                shape,
		nonGenericQueryTerms: nonGenericQueryTerms,
	}, nil
}

// fuseQueries runs each sub-query through the keyword+vector pipeline, fuses
// each per-query with RRF, then merges the per-query result lists. It returns
// the fused results and the primary query's keyword/vector weights (reported
// in SearchMeta).
func (e *Engine) fuseQueries(ctx context.Context, opts Options, sc *searchContext) ([]Result, float64, float64, error) {
	var kwWeight, vecWeight float64
	queryResults := make([][]Result, len(sc.queries))
	for i, q := range sc.queries {
		kwResults, vecResults, err := e.retrieveCandidates(ctx, i, q, opts, sc)
		if err != nil {
			return nil, 0, 0, err
		}

		kwResults = e.substringFallback(ctx, kwResults, q, opts.Language, sc.candidateLimit)

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
		if sc.canVector {
			vecConf := vectorConfidence(vecResults)
			qKwWeight, qVecWeight = shapeWeights(sc.shape, vecConf)
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
	if len(queryResults) == 1 {
		return queryResults[0], kwWeight, vecWeight, nil
	}
	return mergeMultiQuery(queryResults), kwWeight, vecWeight, nil
}

// retrieveCandidates runs the keyword leg (and, when the vector leg can run,
// the vector leg in parallel) for one sub-query. i indexes the pre-embedded
// query vectors; it is only read on the vector path, so the keyword-only path
// is safe when queryVecs is nil.
func (e *Engine) retrieveCandidates(ctx context.Context, i int, q string, opts Options, sc *searchContext) ([]sqlite.SearchResult, []VectorResult, error) {
	var kwResults []sqlite.SearchResult
	var vecResults []VectorResult

	if sc.canVector {
		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			var err error
			kwResults, err = e.adapter.KeywordSearch(gctx, q, opts.Language, sc.candidateLimit)
			return err
		})
		qVec := sc.queryVecs[i]
		g.Go(func() error {
			vecResults = sc.vectors.Search(qVec, sc.candidateLimit)
			return nil
		})
		if err := g.Wait(); err != nil {
			return nil, nil, fmt.Errorf("search: %w", err)
		}
		return kwResults, vecResults, nil
	}

	var err error
	kwResults, err = e.adapter.KeywordSearch(ctx, q, opts.Language, sc.candidateLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("search: %w", err)
	}
	return kwResults, nil, nil
}

// rankResults applies the score-mutating ranking passes to the fused results
// in place and sorts by final score. The pass ORDER is load-bearing — each
// pass mutates scores the next reads — so it must be preserved exactly:
// hydrate → kind weights → path weights → generic-token demotion →
// normalize → graph centrality → test demotion.
func (e *Engine) rankResults(ctx context.Context, opts Options, sc *searchContext, fused []Result) error {
	// Hydrate metadata for vector-only results that have no name/qualified.
	if err := e.hydrateResults(ctx, fused); err != nil {
		return fmt.Errorf("search: %w", err)
	}

	// Graph centrality re-ranking.
	symbolIDs := make([]int64, len(fused))
	for i, r := range fused {
		symbolIDs[i] = r.SymbolID
	}
	centrality, err := e.adapter.InboundEdgeCounts(ctx, symbolIDs)
	if err != nil {
		return fmt.Errorf("search centrality: %w", err)
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
		return fmt.Errorf("search paths: %w", err)
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
	genericTokenPenalty(fused, sc.nonGenericQueryTerms)

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
	return nil
}

// enrichResults runs the graph-augmented passes after ranking: it boosts and
// injects callees of the top results, then promotes a parent class when
// enough of its methods cluster in the top results.
func (e *Engine) enrichResults(ctx context.Context, opts Options, fused []Result) ([]Result, error) {
	// Graph-augmented enrichment: boost callees of top results.
	fused, err := e.enrichFromGraph(ctx, fused)
	if err != nil {
		return nil, fmt.Errorf("search enrich: %w", err)
	}

	// Parent promotion: when 2+ methods of the same class appear in top
	// results, replace them with the parent class at the highest score.
	fused, err = e.promoteParents(ctx, fused, opts.Limit)
	if err != nil {
		return nil, fmt.Errorf("search promote: %w", err)
	}
	return fused, nil
}

// finalizeResults applies the min_score filter and limit, dropping synthetic
// plumbing symbols (ruby-core:Struct / ruby-core:Data and route helpers,
// emitted only so edges resolve) here — the single chokepoint every retrieval
// leg funnels through, so they are never user-facing.
func finalizeResults(opts Options, fused []Result) []Result {
	var results []Result
	for _, r := range fused {
		if r.Score < opts.MinScore {
			continue
		}
		if strings.HasPrefix(r.Qualified, extract.PrefixRubyCore) ||
			strings.HasPrefix(r.Qualified, extract.PrefixRoute) {
			continue
		}
		results = append(results, r)
		if len(results) >= opts.Limit {
			break
		}
	}
	return results
}
