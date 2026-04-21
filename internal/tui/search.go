package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luuuc/sense/internal/search"
)

const (
	searchDebounce    = 300 * time.Millisecond
	searchMaxVisible  = 5
)

type searchState struct {
	query      string
	results    []search.Result
	cursor     int
	debounceID int
	pending    bool
}

type searchDebounceMsg struct {
	id int
}

type searchResultMsg struct {
	results []search.Result
	id      int
}

func newSearchState() *searchState {
	return &searchState{}
}

func (s *searchState) scheduleSearch() (int, tea.Cmd) {
	s.debounceID++
	s.pending = true
	id := s.debounceID
	return id, tea.Tick(searchDebounce, func(_ time.Time) tea.Msg {
		return searchDebounceMsg{id: id}
	})
}

func executeSearch(engine *search.Engine, query string, id int) tea.Cmd {
	return func() tea.Msg {
		results, _, err := engine.Search(context.Background(), search.Options{
			Query: query,
			Limit: 50,
		})
		if err != nil {
			return searchResultMsg{id: id}
		}
		return searchResultMsg{results: results, id: id}
	}
}

// scoreToColor maps a normalized [0,1] search score to a color index.
// High = bright (blast subject), medium = hop2 color, low = hop3, zero = faded.
func scoreToColor(score float64) uint8 {
	switch {
	case score >= 0.7:
		return colorSearchHigh
	case score >= 0.4:
		return colorSearchMed
	case score > 0:
		return colorSearchLow
	default:
		return colorFaded
	}
}

const (
	colorSearchHigh uint8 = 246
	colorSearchMed  uint8 = 247
	colorSearchLow  uint8 = 248
)

func init() {
	specialColors = append(specialColors,
		specialColor{colorSearchHigh, "#61AFEF", "#0366D6"},
		specialColor{colorSearchMed, "#98C379", "#22863A"},
		specialColor{colorSearchLow, "#586069", "#959DA5"},
	)
}

func (s *searchState) applyOverrides(layout *Layout) map[int64]uint8 {
	if layout == nil {
		return nil
	}
	overrides := make(map[int64]uint8, len(layout.Nodes))
	scoreMap := make(map[int64]float64, len(s.results))
	for _, r := range s.results {
		scoreMap[r.SymbolID] = r.Score
	}
	for _, n := range layout.Nodes {
		if score, ok := scoreMap[n.ID]; ok {
			overrides[n.ID] = scoreToColor(score)
		} else {
			overrides[n.ID] = colorFaded
		}
	}
	return overrides
}

func (s *searchState) selectedSymbolID() int64 {
	if s.cursor >= 0 && s.cursor < len(s.results) {
		return s.results[s.cursor].SymbolID
	}
	return 0
}

func renderSearchInput(ss *searchState, dimStyle, accentStyle lipgloss.Style) string {
	prompt := accentStyle.Render("/")
	text := ss.query
	cursor := accentStyle.Render("█")
	count := dimStyle.Render(fmt.Sprintf(" (%d)", len(ss.results)))
	return prompt + text + cursor + count
}

func renderSearchResults(ss *searchState, maxLines, width int, dimStyle, accentStyle lipgloss.Style) string {
	if len(ss.results) == 0 {
		return ""
	}
	var b strings.Builder
	count := len(ss.results)
	if count > maxLines {
		count = maxLines
	}
	for i := 0; i < count; i++ {
		r := ss.results[i]
		prefix := "  "
		if i == ss.cursor {
			prefix = accentStyle.Render("> ")
		}
		name := r.Name
		if r.Qualified != "" {
			name = r.Qualified
		}
		suffix := " " + r.Kind + " " + fmt.Sprintf("%.0f%%", r.Score*100)
		maxName := width - 2 - len(suffix)
		if maxName > 0 && len(name) > maxName {
			name = name[:maxName-1] + "…"
		}
		score := dimStyle.Render(fmt.Sprintf("%.0f%%", r.Score*100))
		kind := dimStyle.Render(r.Kind)
		b.WriteString(prefix + name + " " + kind + " " + score)
		if i < count-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
