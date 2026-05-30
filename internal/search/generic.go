package search

import "strings"

const (
	// genericTokenDFRatio is the document-frequency fraction at or above
	// which a query term is "generic": it appears in too many symbols to
	// rank a hit on its own. Chosen to mark common verbs like
	// prevent/create/handle as generic while leaving domain nouns
	// (listing, negotiation) specific. The threshold is corpus-relative
	// (DF / total symbols), not a hardcoded English stopword list; see the
	// pitch's generic-token rabbit hole. Validated against the maket
	// corpus distribution in the ground-truth suite.
	genericTokenDFRatio = 0.05

	// genericOnlyPenalty multiplies the (normalized) score of a
	// keyword-sourced hit whose only overlap with the query is generic
	// tokens. Tuned, like testRankPenalty, to push such a hit below a
	// genuine domain match.
	genericOnlyPenalty = 0.3
)

// queryTermSet returns the distinct, lowercased query terms used for
// document-frequency lookup and generic-token analysis. Each whitespace
// token is also split on identifier boundaries so a camelCase/snake_case
// query term contributes its constituent words (matching how the FTS
// index decomposes name_parts).
func queryTermSet(query string) []string {
	seen := map[string]struct{}{}
	var terms []string
	add := func(w string) {
		if w == "" {
			return
		}
		if _, ok := seen[w]; ok {
			return
		}
		seen[w] = struct{}{}
		terms = append(terms, w)
	}
	for _, tok := range strings.Fields(query) {
		add(strings.ToLower(strings.Trim(tok, ".,;:?!()\"'`")))
		for _, w := range splitToken(tok) {
			add(w)
		}
	}
	return terms
}

// nonGenericTerms returns the subset of query terms that are NOT generic —
// specific enough that a hit matching one should be trusted. A term is
// generic when its document frequency / total symbols >= the threshold.
// When the corpus size is unknown (<= 0) every term is treated as specific
// so the penalty becomes a no-op rather than demoting indiscriminately.
func nonGenericTerms(terms []string, df map[string]int, totalSymbols int) map[string]struct{} {
	out := make(map[string]struct{}, len(terms))
	if totalSymbols <= 0 {
		for _, t := range terms {
			out[t] = struct{}{}
		}
		return out
	}
	threshold := genericTokenDFRatio * float64(totalSymbols)
	for _, t := range terms {
		if float64(df[t]) < threshold {
			out[t] = struct{}{}
		}
	}
	return out
}

// genericTokenPenalty demotes keyword-sourced hits whose only overlap with
// the query is high-frequency generic tokens. A hit is left untouched if
// any non-generic query term appears in its name/qualified/snippet; only
// when none do — meaning it surfaced purely on a generic token like
// "prevent" — is its score multiplied down.
//
// It is applied pre-normalize (like applyKindWeights): the fused RRF
// scores of single-term keyword matches are tightly clustered, so the
// multiplier is decisive here, whereas a post-normalize multiplier is
// neutralized whenever the genuine domain match is the rescale floor
// (pinned to 0). Vector and hybrid hits are exempt: the vector leg
// vouching for a hit means it is not generic-only. The penalty is applied
// via demote so it can never invert into a promotion.
func genericTokenPenalty(results []Result, nonGeneric map[string]struct{}) {
	if len(nonGeneric) == 0 {
		return
	}
	for i := range results {
		if results[i].Source != SourceKeyword {
			continue
		}
		if symbolMatchesAny(results[i], nonGeneric) {
			continue
		}
		results[i].Score = demote(results[i].Score, genericOnlyPenalty)
	}
}

// symbolMatchesAny reports whether any term appears as a token in the
// result's name, qualified name, or snippet (after identifier splitting).
func symbolMatchesAny(r Result, terms map[string]struct{}) bool {
	for _, field := range []string{r.Name, r.Qualified, r.Snippet} {
		for _, tok := range strings.Fields(field) {
			if _, ok := terms[strings.ToLower(tok)]; ok {
				return true
			}
			for _, w := range splitToken(tok) {
				if _, ok := terms[w]; ok {
					return true
				}
			}
		}
	}
	return false
}
