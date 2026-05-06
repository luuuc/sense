package search

import (
	"testing"
)


func TestExpandQueryIdenticalSplit(t *testing.T) {
	// "hello world" splits into "hello world" (identical) -> no second sub-query
	got := expandQuery("hello world")
	if len(got) != 1 {
		t.Errorf("expandQuery(\"hello world\") = %v, want 1 sub-query (no duplicate)", got)
	}
}

func TestExpandQueryLong5Words(t *testing.T) {
	got := expandQuery("one two three four five")
	// Should have original + ident split (same) + short 3-word variant
	foundShort := false
	for _, q := range got {
		if q == "one two three" {
			foundShort = true
		}
	}
	if !foundShort {
		t.Errorf("expected short variant 'one two three' in %v", got)
	}
}

func TestExpandQueryShortMatchesIdent(t *testing.T) {
	// "UserAuth something else more" -> identTokens="user auth something else more"
	// short = "UserAuth something else" (first 3 words)
	got := expandQuery("UserAuth something else more")
	if len(got) > 3 {
		t.Errorf("expandQuery should cap at 3, got %d", len(got))
	}
}

func TestSplitIdentifiersEmpty(t *testing.T) {
	got := splitIdentifiers("")
	if got != "" {
		t.Errorf("splitIdentifiers(\"\") = %q, want \"\"", got)
	}
}

func TestSplitIdentifiersHyphenSeparator(t *testing.T) {
	got := splitIdentifiers("user-profile-handler")
	if got != "user profile handler" {
		t.Errorf("splitIdentifiers(\"user-profile-handler\") = %q, want \"user profile handler\"", got)
	}
}

func TestSplitIdentifiersDotSeparator(t *testing.T) {
	got := splitIdentifiers("com.example.service")
	if got != "com example service" {
		t.Errorf("splitIdentifiers(\"com.example.service\") = %q, want \"com example service\"", got)
	}
}

func TestSplitCamelCaseEmpty(t *testing.T) {
	got := splitCamelCase("")
	if got != nil {
		t.Errorf("splitCamelCase(\"\") = %v, want nil", got)
	}
}

func TestSplitCamelCaseAllLower(t *testing.T) {
	got := splitCamelCase("simple")
	if len(got) != 1 || got[0] != "simple" {
		t.Errorf("splitCamelCase(\"simple\") = %v, want [\"simple\"]", got)
	}
}

func TestMergeMultiQueryOverlappingCoverage(t *testing.T) {
	r1 := []Result{
		{SymbolID: 1, Name: "A", Score: 0.9},
		{SymbolID: 2, Name: "B", Score: 0.8},
	}
	r2 := []Result{
		{SymbolID: 2, Name: "B", Score: 0.7},
		{SymbolID: 3, Name: "C", Score: 0.6},
	}
	got := mergeMultiQuery([][]Result{r1, r2})
	if len(got) != 3 {
		t.Errorf("mergeMultiQuery returned %d results, want 3", len(got))
	}
	// Symbol 2 (B) should have boosted score from appearing in both
	for _, r := range got {
		if r.SymbolID == 2 && r.Score <= 0 {
			t.Error("symbol B should have positive score from merge")
		}
	}
}

func TestMergeMultiQueryEmpty(t *testing.T) {
	got := mergeMultiQuery(nil)
	if len(got) != 0 {
		t.Errorf("mergeMultiQuery(nil) = %d results, want 0", len(got))
	}
}

func TestMergeMultiQuerySingle(t *testing.T) {
	r := []Result{
		{SymbolID: 1, Name: "A", Score: 0.9},
		{SymbolID: 2, Name: "B", Score: 0.8},
	}
	got := mergeMultiQuery([][]Result{r})
	if len(got) != 2 {
		t.Errorf("mergeMultiQuery single list = %d results, want 2", len(got))
	}
}
