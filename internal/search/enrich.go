package search

import (
	"context"
	"sort"

	"github.com/luuuc/sense/internal/sqlite"
)

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

const (
	enrichTopN      = 3
	enrichBoost     = 0.15
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
				LineStart: sym.LineStart,
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

// parentGroup accumulates the top-K children that share one parent symbol,
// tracking the best child score (and its provenance) the promoted parent
// inherits.
type parentGroup struct {
	info      sqlite.ParentInfo
	children  []int
	maxScore  float64
	maxSource string
}

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

	groups := groupByParent(results, parents, topK)

	existing := map[int64]bool{}
	for _, r := range results {
		existing[r.SymbolID] = true
	}

	promoted, remove := selectPromotions(groups, existing)
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

// groupByParent buckets the top-K results by their parent symbol, recording
// each group's children (by index) and the highest child score and source.
func groupByParent(results []Result, parents map[int64]sqlite.ParentInfo, topK int) map[int64]*parentGroup {
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
	return groups
}

// selectPromotions turns parent groups into promoted parent results and the
// set of child indices they replace. A group is promoted only when it has the
// threshold number of children and the parent is not already in the results.
func selectPromotions(groups map[int64]*parentGroup, existing map[int64]bool) ([]Result, map[int]bool) {
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
	return promoted, remove
}
