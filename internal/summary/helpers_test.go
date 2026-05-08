package summary

import (
	"testing"
)

func TestNamespacePrefixFromPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"internal/summary/summary.go", "internal/summary"},
		{"main.go", "."},
		{"cmd/sense/main.go", "cmd/sense"},
		{"a/b/c/d.go", "a/b"},
		{"root.go", "."},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := namespacePrefixFromPath(tt.input)
			if got != tt.want {
				t.Errorf("namespacePrefixFromPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCommonPrefixCoverage(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"internal/summary/a.go", "internal/summary/b.go", "internal/summary"},
		{"internal/a.go", "internal/b.go", "internal"},
		{"a.go", "b.go", ""},
		{"same.go", "same.go", "same.go"},
		{"a/b/c.go", "a/b/d.go", "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.a+"+"+tt.b, func(t *testing.T) {
			got := commonPrefix(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("commonPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "Hello"},
		{"Hello", "Hello"},
		{"", ""},
		{"a", "A"},
		{"123", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := capitalize(tt.input)
			if got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestTokenBudget(t *testing.T) {
	t.Setenv("SENSE_SUMMARY_TOKENS", "")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "5000")
	if got := TokenBudget(); got != 5000 {
		t.Errorf("TokenBudget() = %d, want 5000", got)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "invalid")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "-1")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}
}
