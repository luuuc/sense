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
		name      string
		resp      mcpio.OrientResponse
		wantLen   int
		wantTool  string
		wantTool2 string
	}{
		{
			name: "hub symbol and search hit",
			resp: mcpio.OrientResponse{
				Structure: &mcpio.StatusStructure{
					HubSymbols: []mcpio.StatusHub{{Name: "Run", Callers: 42, Kind: "function"}},
				},
				SearchHits: []mcpio.SearchResultEntry{
					{Symbol: "Router", Kind: "struct"},
				},
			},
			wantLen:   2,
			wantTool:  "sense.graph",
			wantTool2: "sense.blast",
		},
		{
			name: "hub symbol and key symbol interface",
			resp: mcpio.OrientResponse{
				Structure: &mcpio.StatusStructure{
					HubSymbols: []mcpio.StatusHub{{Name: "Close", Callers: 10, Kind: "method"}},
				},
				Conventions: []mcpio.ConventionEntry{
					{Category: "framework", KeySymbol: "IRoutes"},
				},
			},
			wantLen:   2,
			wantTool:  "sense.graph",
			wantTool2: "sense.graph",
		},
		{
			name: "hub symbol only",
			resp: mcpio.OrientResponse{
				Structure: &mcpio.StatusStructure{
					HubSymbols: []mcpio.StatusHub{{Name: "Open", Callers: 5, Kind: "function"}},
				},
			},
			wantLen:  1,
			wantTool: "sense.graph",
		},
		{
			name:     "empty response suggests search",
			resp:     mcpio.OrientResponse{},
			wantLen:  1,
			wantTool: "sense.search",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hints := orientHints(tt.resp)
			if len(hints) != tt.wantLen {
				t.Fatalf("orientHints: got %d hints, want %d: %+v", len(hints), tt.wantLen, hints)
			}
			if hints[0].Tool != tt.wantTool {
				t.Errorf("orientHints[0].Tool = %q, want %q", hints[0].Tool, tt.wantTool)
			}
			if tt.wantTool2 != "" && len(hints) > 1 {
				if hints[1].Tool != tt.wantTool2 {
					t.Errorf("orientHints[1].Tool = %q, want %q", hints[1].Tool, tt.wantTool2)
				}
			}
		})
	}
}
