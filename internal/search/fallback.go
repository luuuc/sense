package search

import (
	"context"
	"strings"

	"github.com/luuuc/sense/internal/sqlite"
)

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
