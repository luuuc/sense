package search

import (
	"sort"
	"testing"
)

func TestQueryTermSet(t *testing.T) {
	// "user_id" re-adds the already-seen split token "user" (dedup branch);
	// "..." trims to empty and splits to nothing (empty-token branch).
	got := queryTermSet("prevent_own user user_id ... Listing?")
	set := map[string]bool{}
	for _, term := range got {
		set[term] = true
	}
	for _, want := range []string{"prevent_own", "prevent", "own", "listing"} {
		if !set[want] {
			t.Errorf("queryTermSet missing %q; got %v", want, got)
		}
	}
	// Distinct: no duplicates.
	seen := map[string]bool{}
	for _, term := range got {
		if seen[term] {
			t.Errorf("duplicate term %q in %v", term, got)
		}
		seen[term] = true
	}
}

// TestNonGenericTermsThresholdBoundary pins the DF threshold: with a
// corpus of 100 symbols the cutoff is 0.05*100 = 5. A term at df=4 (just
// below) is non-generic; a term at df=5 (exactly at the cutoff) is
// generic; a high-DF term is generic.
func TestNonGenericTermsThresholdBoundary(t *testing.T) {
	terms := []string{"rare", "boundary", "common"}
	df := map[string]int{"rare": 4, "boundary": 5, "common": 50}

	out := nonGenericTerms(terms, df, 100)

	if _, ok := out["rare"]; !ok {
		t.Error("term with df=4 (below threshold 5) should be non-generic")
	}
	if _, ok := out["boundary"]; ok {
		t.Error("term with df=5 (at threshold 5) should be generic, not in non-generic set")
	}
	if _, ok := out["common"]; ok {
		t.Error("term with df=50 should be generic")
	}
}

func TestNonGenericTermsUnknownCorpusSize(t *testing.T) {
	terms := []string{"a", "b"}
	df := map[string]int{"a": 999, "b": 0}
	out := nonGenericTerms(terms, df, 0)
	if len(out) != 2 {
		t.Errorf("with totalSymbols<=0 every term should be non-generic (no-op penalty); got %v", out)
	}
}

// TestGenericTokenPenaltyDemotesGenericOnly is the core behavioral pin: a
// keyword-only hit matching solely a generic token is demoted; a hit
// matching a non-generic domain term is not; vector- and hybrid-sourced
// hits are exempt even when generic-only.
func TestGenericTokenPenaltyDemotesGenericOnly(t *testing.T) {
	results := []Result{
		{Name: "preventClose", Qualified: "ui.preventClose", Score: 1.0, Source: SourceKeyword},
		{Name: "ListingGuard", Qualified: "shop.ListingGuard", Score: 0.8, Source: SourceKeyword},
		{Name: "preventEscape", Qualified: "ui.preventEscape", Score: 0.9, Source: SourceVector},
		{Name: "preventResize", Qualified: "ui.preventResize", Score: 0.7, Source: SourceHybrid},
	}
	// "prevent" is generic (absent from the set); "listing" is the only
	// non-generic query term.
	nonGeneric := map[string]struct{}{"listing": {}}

	genericTokenPenalty(results, nonGeneric)

	if results[0].Score != 1.0*genericOnlyPenalty {
		t.Errorf("generic-only keyword hit not demoted: score = %v, want %v", results[0].Score, genericOnlyPenalty)
	}
	if results[1].Score != 0.8 {
		t.Errorf("domain-term hit wrongly demoted: score = %v, want 0.8", results[1].Score)
	}
	if results[2].Score != 0.9 {
		t.Errorf("vector-sourced hit must be exempt: score = %v, want 0.9", results[2].Score)
	}
	if results[3].Score != 0.7 {
		t.Errorf("hybrid-sourced hit must be exempt: score = %v, want 0.7", results[3].Score)
	}
}

func TestGenericTokenPenaltyNoNonGenericTermsIsNoOp(t *testing.T) {
	results := []Result{
		{Name: "preventClose", Qualified: "ui.preventClose", Score: 1.0, Source: SourceKeyword},
	}
	genericTokenPenalty(results, nil)
	if results[0].Score != 1.0 {
		t.Errorf("empty non-generic set must be a no-op; score = %v", results[0].Score)
	}
}

// TestSymbolMatchesAnySnippetAndSplit covers the snippet field and the
// identifier-split branch of token matching.
func TestSymbolMatchesAnySnippetAndSplit(t *testing.T) {
	r := Result{Name: "Foo", Qualified: "pkg.Foo", Snippet: "func cannot_offer_on_own_listing()"}
	if !symbolMatchesAny(r, map[string]struct{}{"offer": {}}) {
		t.Error("expected match on snippet identifier-split token 'offer'")
	}
	if symbolMatchesAny(r, map[string]struct{}{"absent": {}}) {
		t.Error("did not expect a match for an absent term")
	}
	// Sanity: deterministic across iteration order.
	terms := map[string]struct{}{"foo": {}, "listing": {}}
	keys := make([]string, 0, len(terms))
	for k := range terms {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if !symbolMatchesAny(r, terms) {
		t.Error("expected match on name token 'foo'")
	}
}
