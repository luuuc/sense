package versioncheck

import "testing"

func TestParseSemver(t *testing.T) {
	tests := []struct {
		input string
		want  [3]int
	}{
		{"1.2.3", [3]int{1, 2, 3}},
		{"v0.8.0", [3]int{0, 8, 0}},
		{"0.0.0-dev", [3]int{0, 0, 0}},
		{"1.0.0-beta.1", [3]int{1, 0, 0}},
	}
	for _, tt := range tests {
		got := parseSemver(tt.input)
		if got != tt.want {
			t.Errorf("parseSemver(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	tests := []struct {
		latest, current string
		want            bool
	}{
		{"0.9.0", "0.8.0", true},
		{"1.0.0", "0.9.9", true},
		{"0.8.0", "0.8.0", false},
		{"0.7.0", "0.8.0", false},
		{"0.1.0", "0.0.0-dev", true},
		{"0.0.0-dev", "0.0.0-dev", false},
	}
	for _, tt := range tests {
		got := isNewer(tt.latest, tt.current)
		if got != tt.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}
