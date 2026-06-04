package search

import (
	"sort"

	"github.com/luuuc/sense/internal/sqlite"
)

const rrfK = 60

const (
	confidenceHighThreshold = 0.6
	confidenceLowThreshold  = 0.4
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
