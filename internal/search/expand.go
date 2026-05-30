package search

import (
	"strings"
	"unicode"
)

const maxSubQueries = 3

// expandQuery decomposes a query into up to 3 sub-queries for multi-angle
// search. Returns at least the original query.
func expandQuery(query string) []string {
	queries := []string{query}

	// Sub-query 2: split identifiers into constituent words.
	// "HandleHTTPRequest" → "handle http request"
	// "user_auth_token" → "user auth token"
	identTokens := splitIdentifiers(query)
	if identTokens != "" && identTokens != strings.ToLower(query) {
		queries = append(queries, identTokens)
	}

	// Sub-query 3: for long queries (4+ words), take first 3 words.
	words := strings.Fields(query)
	if len(words) >= 4 {
		short := strings.Join(words[:3], " ")
		if short != query && short != identTokens {
			queries = append(queries, short)
		}
	}

	if len(queries) > maxSubQueries {
		queries = queries[:maxSubQueries]
	}
	return queries
}

// splitIdentifiers extracts words from camelCase, PascalCase, and
// snake_case tokens in the query, returning them as a lowercase
// space-separated string.
func splitIdentifiers(query string) string {
	var words []string
	for _, token := range strings.Fields(query) {
		words = append(words, splitToken(token)...)
	}
	if len(words) == 0 {
		return ""
	}
	return strings.Join(words, " ")
}

// splitToken breaks a single token by camelCase boundaries and underscores.
func splitToken(s string) []string {
	// First split on underscores/hyphens.
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || r == '.'
	})

	var words []string
	for _, part := range parts {
		words = append(words, splitCamelCase(part)...)
	}
	return words
}

// splitCamelCase splits "HandleHTTPRequest" → ["handle", "http", "request"].
func splitCamelCase(s string) []string {
	if s == "" {
		return nil
	}

	var words []string
	runes := []rune(s)
	start := 0

	for i := 1; i < len(runes); i++ {
		// Split before an uppercase letter following a lowercase letter:
		// "handleRequest" → split before 'R'
		if unicode.IsUpper(runes[i]) && unicode.IsLower(runes[i-1]) {
			words = append(words, strings.ToLower(string(runes[start:i])))
			start = i
			continue
		}
		// Split before the last uppercase in an acronym run:
		// "HTTPRequest" → split before 'R' (keeping "HTTP" together then "Request")
		if i+1 < len(runes) && unicode.IsUpper(runes[i]) && unicode.IsUpper(runes[i-1]) && unicode.IsLower(runes[i+1]) {
			words = append(words, strings.ToLower(string(runes[start:i])))
			start = i
			continue
		}
	}

	if start < len(runes) {
		words = append(words, strings.ToLower(string(runes[start:])))
	}
	return words
}

// mergeMultiQuery merges per-query result lists using RRF. Symbols that
// appear in multiple sub-query results get boosted. Each input list is
// consumed in rank order — slice position is the RRF rank — so callers must
// pass score-sorted lists (fuseRRF guarantees this); unsorted input would
// fuse on map-iteration noise instead of the weighted fusion ranking.
func mergeMultiQuery(queryResults [][]Result) []Result {
	type entry struct {
		result Result
		score  float64
	}
	merged := make(map[int64]*entry)

	for _, results := range queryResults {
		for rank, r := range results {
			rrfScore := 1.0 / float64(rrfK+rank+1)
			if e, ok := merged[r.SymbolID]; ok {
				e.score += rrfScore
				e.result.Source = mergeSource(e.result.Source, r.Source)
			} else {
				merged[r.SymbolID] = &entry{result: r, score: rrfScore}
			}
		}
	}

	out := make([]Result, 0, len(merged))
	for _, e := range merged {
		e.result.Score = e.score
		out = append(out, e.result)
	}
	return out
}
