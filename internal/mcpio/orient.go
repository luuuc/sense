package mcpio

// ApplyOrientTokenBudget trims an OrientResponse to fit within the
// given token budget. Progressive truncation: search hits are trimmed
// first, then conventions. Structure is always preserved.
func ApplyOrientTokenBudget(r *OrientResponse, budget int) {
	r.TokenBudget = budget

	for len(r.SearchHits) > 0 && estimateJSONTokens(r) > budget {
		r.SearchHits = r.SearchHits[:len(r.SearchHits)-1]
		r.Truncated = true
	}

	for len(r.Conventions) > 0 && estimateJSONTokens(r) > budget {
		r.Conventions = r.Conventions[:len(r.Conventions)-1]
		r.Truncated = true
	}
}
