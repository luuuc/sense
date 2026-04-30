package search

import (
	"testing"
)

func TestExpandQuerySingleWord(t *testing.T) {
	got := expandQuery("payment")
	if len(got) != 1 || got[0] != "payment" {
		t.Errorf("expandQuery(\"payment\") = %v, want [\"payment\"]", got)
	}
}

func TestExpandQueryCamelCase(t *testing.T) {
	got := expandQuery("HandleHTTPRequest")
	if len(got) < 2 {
		t.Fatalf("expandQuery(\"HandleHTTPRequest\") = %v, want at least 2 sub-queries", got)
	}
	if got[0] != "HandleHTTPRequest" {
		t.Errorf("first sub-query = %q, want original", got[0])
	}
	if got[1] != "handle http request" {
		t.Errorf("second sub-query = %q, want \"handle http request\"", got[1])
	}
}

func TestExpandQuerySnakeCase(t *testing.T) {
	got := expandQuery("user_auth_token")
	if len(got) < 2 {
		t.Fatalf("expandQuery(\"user_auth_token\") = %v, want at least 2 sub-queries", got)
	}
	if got[1] != "user auth token" {
		t.Errorf("second sub-query = %q, want \"user auth token\"", got[1])
	}
}

func TestExpandQueryLongPhrase(t *testing.T) {
	got := expandQuery("HTTP request routing middleware handler")
	if len(got) < 2 {
		t.Fatalf("got %v, want at least 2", got)
	}
	// Should have short variant (first 3 words)
	found := false
	for _, q := range got {
		if q == "HTTP request routing" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected short variant \"HTTP request routing\" in %v", got)
	}
}

func TestExpandQueryCapsAt3(t *testing.T) {
	// A query that could produce 4+ expansions should be capped.
	got := expandQuery("ProcessHTTPPaymentRequest handler code logic")
	if len(got) > 3 {
		t.Errorf("expandQuery produced %d sub-queries, want <= 3", len(got))
	}
}

func TestExpandQueryAlreadyLowercase(t *testing.T) {
	// If splitting produces the same string, don't duplicate.
	got := expandQuery("payment")
	if len(got) != 1 {
		t.Errorf("expandQuery(\"payment\") = %v, want 1 sub-query (no duplicate)", got)
	}
}

func TestSplitCamelCase(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"ServeHTTP", []string{"serve", "http"}},
		{"HandleRequest", []string{"handle", "request"}},
		{"HTTPRequest", []string{"http", "request"}},
		{"simple", []string{"simple"}},
		{"URL", []string{"url"}},
		{"getURLParser", []string{"get", "url", "parser"}},
	}
	for _, tt := range tests {
		got := splitCamelCase(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCamelCase(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCamelCase(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
