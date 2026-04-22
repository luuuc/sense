package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// StatusData holds the dynamic session metrics displayed in the status bar.
// Sent over a channel by the MCP server (or any other metrics source).
type StatusData struct {
	Queries         int
	TokensSaved     int
	Symbols         int
	Edges           int
	FilesChanged    int // files modified since last index
	IndexAgeSeconds int // seconds since last scan; 0 = unknown, -1 = no index
}

type statusUpdateMsg StatusData

func listenForStatusUpdates(ch <-chan StatusData) tea.Cmd {
	return func() tea.Msg {
		data, ok := <-ch
		if !ok {
			return nil
		}
		return statusUpdateMsg(data)
	}
}

func renderSessionStatus(s StatusData, width int, dim lipgloss.Style) string {
	if width < 80 {
		return dim.Render(fmt.Sprintf("index: %s sym %d edges", formatCount(s.Symbols), s.Edges))
	}

	var parts []string
	if s.Queries > 0 {
		parts = append(parts, fmt.Sprintf("%d queries", s.Queries))
	}
	if s.TokensSaved > 0 && width >= 100 {
		parts = append(parts, fmt.Sprintf("~%s tokens saved", formatCompact(s.TokensSaved)))
	}
	parts = append(parts, fmt.Sprintf("index: %s sym %d edges", formatCount(s.Symbols), s.Edges))
	if s.FilesChanged > 0 && width >= 100 {
		parts = append(parts, fmt.Sprintf("%d files changed", s.FilesChanged))
	}
	return dim.Render(strings.Join(parts, " · "))
}

// tokenDollars estimates the dollar value of saved tokens at $3/1M input tokens.
func tokenDollars(tokens int) float64 {
	return float64(tokens) * 3.0 / 1_000_000
}

func formatCompact(n int) string {
	var s string
	switch {
	case n >= 1_000_000:
		s = fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		s = fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
	s = strings.Replace(s, ".0k", "k", 1)
	s = strings.Replace(s, ".0M", "M", 1)
	return s
}

func formatCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var out strings.Builder
	offset := len(s) % 3
	for i, ch := range s {
		if i > 0 && (i-offset)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(ch)
	}
	return out.String()
}
