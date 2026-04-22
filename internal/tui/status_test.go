package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func testDimStyle() lipgloss.Style {
	return lipgloss.NewStyle()
}

func TestRenderSessionStatus_ZeroState(t *testing.T) {
	s := StatusData{}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, "index: 0 sym 0 edges") {
		t.Errorf("zero state should show index vitals, got %q", got)
	}
	if containsText(got, "queries") {
		t.Errorf("zero state should not show queries, got %q", got)
	}
}

func TestRenderSessionStatus_BasicVitals(t *testing.T) {
	s := StatusData{Symbols: 100, Edges: 50}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, "index: 100 sym 50 edges") {
		t.Errorf("expected index vitals, got %q", got)
	}
}

func TestRenderSessionStatus_WithQueries(t *testing.T) {
	s := StatusData{Queries: 5, TokensSaved: 18000, Symbols: 100, Edges: 50}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, "5 queries") {
		t.Errorf("expected query count, got %q", got)
	}
	if !containsText(got, "~18k tokens saved") {
		t.Errorf("expected token savings, got %q", got)
	}
}

func TestRenderSessionStatus_TokensHiddenNarrow(t *testing.T) {
	s := StatusData{Queries: 5, TokensSaved: 18000, Symbols: 100, Edges: 50}
	got := renderSessionStatus(s, 90, testDimStyle())
	if containsText(got, "tokens saved") {
		t.Errorf("tokens should be hidden at width 90, got %q", got)
	}
	if !containsText(got, "5 queries") {
		t.Errorf("queries should still show at width 90, got %q", got)
	}
}

func TestRenderSessionStatus_VeryNarrow(t *testing.T) {
	s := StatusData{Queries: 5, TokensSaved: 18000, Symbols: 100, Edges: 50, FilesChanged: 3}
	got := renderSessionStatus(s, 70, testDimStyle())
	if !containsText(got, "index: 100 sym 50 edges") {
		t.Errorf("narrow should show only index vitals, got %q", got)
	}
	if containsText(got, "queries") {
		t.Errorf("narrow should not show queries, got %q", got)
	}
	if containsText(got, "files changed") {
		t.Errorf("narrow should not show files changed, got %q", got)
	}
}

func TestRenderSessionStatus_StalenessHint(t *testing.T) {
	s := StatusData{Symbols: 100, Edges: 50, FilesChanged: 3}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, "3 files changed") {
		t.Errorf("expected staleness hint, got %q", got)
	}
}

func TestRenderSessionStatus_ThousandSeparator(t *testing.T) {
	s := StatusData{Symbols: 1247, Edges: 50}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, "index: 1,247 sym") {
		t.Errorf("expected comma-separated count, got %q", got)
	}
}

func TestRenderSessionStatus_SeparatorStyle(t *testing.T) {
	s := StatusData{Queries: 3, Symbols: 100, Edges: 50}
	got := renderSessionStatus(s, 120, testDimStyle())
	if !containsText(got, " · ") {
		t.Errorf("expected thin separator, got %q", got)
	}
	if containsText(got, "  ·  ") {
		t.Errorf("separator should not have double spaces, got %q", got)
	}
}

func TestTokenDollars(t *testing.T) {
	tests := []struct {
		tokens int
		want   float64
	}{
		{10_000, 0.03},
		{50_000, 0.15},
		{100_000, 0.30},
		{0, 0.0},
	}
	for _, tt := range tests {
		got := tokenDollars(tt.tokens)
		if got != tt.want {
			t.Errorf("tokenDollars(%d) = %f, want %f", tt.tokens, got, tt.want)
		}
	}
}

func TestFormatCompact(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{500, "500"},
		{1000, "1k"},
		{1500, "1.5k"},
		{18000, "18k"},
		{18500, "18.5k"},
		{100000, "100k"},
		{1000000, "1M"},
		{1500000, "1.5M"},
	}
	for _, tt := range tests {
		got := formatCompact(tt.n)
		if got != tt.want {
			t.Errorf("formatCompact(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1247, "1,247"},
		{12345, "12,345"},
		{1000000, "1,000,000"},
	}
	for _, tt := range tests {
		got := formatCount(tt.n)
		if got != tt.want {
			t.Errorf("formatCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestStatusUpdateMsg_UpdatesModel(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24

	updated, _ := m.Update(statusUpdateMsg{Queries: 3, TokensSaved: 5000, Symbols: 15, Edges: 8})
	um := updated.(model)
	if um.status.Queries != 3 {
		t.Errorf("expected 3 queries, got %d", um.status.Queries)
	}
	if um.status.TokensSaved != 5000 {
		t.Errorf("expected 5000 tokens saved, got %d", um.status.TokensSaved)
	}
}

func TestStatusChannel_ListenAndUpdate(t *testing.T) {
	ch := make(chan StatusData, 1)
	ch <- StatusData{Queries: 1, Symbols: 10, Edges: 5}

	cmd := listenForStatusUpdates(ch)
	msg := cmd()
	if msg == nil {
		t.Fatal("expected statusUpdateMsg from channel")
	}
	su, ok := msg.(statusUpdateMsg)
	if !ok {
		t.Fatalf("expected statusUpdateMsg, got %T", msg)
	}
	if su.Queries != 1 {
		t.Errorf("expected 1 query, got %d", su.Queries)
	}
}
