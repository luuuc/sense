package mcpserver

import (
	"testing"

	"github.com/luuuc/sense/internal/mcpio"
)

func TestExtractConcepts(t *testing.T) {
	tests := []struct {
		name     string
		question string
		want     []string
	}{
		{
			name:     "routing question",
			question: "How does routing work?",
			want:     []string{"routing"},
		},
		{
			name:     "multiple concepts",
			question: "How does the authentication middleware handle JWT tokens?",
			want:     []string{"authentication", "middleware", "handle", "jwt", "tokens"},
		},
		{
			name:     "all stop words",
			question: "How is the project structured?",
			want:     nil,
		},
		{
			name:     "empty input",
			question: "",
			want:     nil,
		},
		{
			name:     "more than 5 concepts",
			question: "authentication middleware routing database caching logging monitoring tracing",
			want:     []string{"authentication", "middleware", "routing", "database", "caching"},
		},
		{
			name:     "short words filtered",
			question: "How do we go to it?",
			want:     nil,
		},
		{
			name:     "punctuation stripped",
			question: "What's the error-handling strategy?",
			want:     []string{"error", "handling", "strategy"},
		},
		{
			name:     "dedup",
			question: "auth auth auth middleware middleware",
			want:     []string{"auth", "middleware"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractConcepts(tt.question)
			if len(got) != len(tt.want) {
				t.Fatalf("extractConcepts(%q) = %v (len %d), want %v (len %d)",
					tt.question, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("extractConcepts(%q)[%d] = %q, want %q",
						tt.question, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestOrientHints(t *testing.T) {
	tests := []struct {
		name     string
		resp     mcpio.OrientResponse
		question string
		wantLen  int
		wantTool string
	}{
		{
			name: "with search hits suggests graph",
			resp: mcpio.OrientResponse{
				SearchHits: []mcpio.SearchResultEntry{
					{Symbol: "Router", Kind: "struct"},
				},
				Conventions: []mcpio.ConventionEntry{
					{Category: "naming"},
				},
			},
			question: "routing",
			wantLen:  2,
			wantTool: "sense.graph",
		},
		{
			name: "no search hits with conventions",
			resp: mcpio.OrientResponse{
				Conventions: []mcpio.ConventionEntry{
					{Category: "naming"},
				},
			},
			question: "",
			wantLen:  2,
			wantTool: "sense.conventions",
		},
		{
			name:     "empty response no question suggests search",
			resp:     mcpio.OrientResponse{},
			question: "",
			wantLen:  1,
			wantTool: "sense.search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := orientHints(tt.resp, tt.question)
			if len(hints) != tt.wantLen {
				t.Fatalf("orientHints: got %d hints, want %d: %+v", len(hints), tt.wantLen, hints)
			}
			if hints[0].Tool != tt.wantTool {
				t.Errorf("orientHints[0].Tool = %q, want %q", hints[0].Tool, tt.wantTool)
			}
		})
	}
}
