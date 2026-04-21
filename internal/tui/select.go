package tui

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// selectionState tracks which node is selected and fuzzy-find state.
type selectionState struct {
	selectedIdx int
	filterMode  bool
	filterText  string
	matches     []int
	matchCursor int
}

func newSelectionState() selectionState {
	return selectionState{selectedIdx: -1}
}

// selectedNode returns the currently selected layout node, or nil.
func (s *selectionState) selectedNode(layout *Layout) *LayoutNode {
	if layout == nil || s.selectedIdx < 0 || s.selectedIdx >= len(layout.Nodes) {
		return nil
	}
	return &layout.Nodes[s.selectedIdx]
}

// selectNearest picks the node closest to the viewport center.
func (s *selectionState) selectNearest(layout *Layout, vp Viewport) {
	if layout == nil || len(layout.Nodes) == 0 {
		return
	}
	cx := 0.5 + vp.OffsetX
	cy := 0.5 + vp.OffsetY
	best := -1
	bestDist := math.MaxFloat64
	for i, n := range layout.Nodes {
		dx := n.X - cx
		dy := n.Y - cy
		d := dx*dx + dy*dy
		if d < bestDist {
			bestDist = d
			best = i
		}
	}
	s.selectedIdx = best
}

// moveSelection navigates to the nearest node in the given direction
// from the current selection. dirX/dirY are -1, 0, or 1.
func (s *selectionState) moveSelection(layout *Layout, dirX, dirY float64) {
	if layout == nil || len(layout.Nodes) == 0 || s.selectedIdx < 0 {
		return
	}
	cur := layout.Nodes[s.selectedIdx]
	best := -1
	bestScore := math.MaxFloat64

	for i, n := range layout.Nodes {
		if i == s.selectedIdx {
			continue
		}
		dx := n.X - cur.X
		dy := n.Y - cur.Y

		dot := dx*dirX + dy*dirY
		if dot <= 0 {
			continue
		}

		dist := math.Sqrt(dx*dx + dy*dy)
		if dist < 1e-9 {
			continue
		}
		alignment := dot / dist
		score := dist / (alignment * alignment)

		if score < bestScore {
			bestScore = score
			best = i
		}
	}
	if best >= 0 {
		s.selectedIdx = best
	}
}

// startFilter enters fuzzy-find mode.
func (s *selectionState) startFilter() {
	s.filterMode = true
	s.filterText = ""
	s.matches = s.matches[:0]
	s.matchCursor = 0
}

// updateFilter re-computes matches for the current filter text.
func (s *selectionState) updateFilter(layout *Layout) {
	if layout == nil {
		return
	}
	query := strings.ToLower(s.filterText)
	s.matches = s.matches[:0]
	for i, n := range layout.Nodes {
		if strings.Contains(strings.ToLower(n.Name), query) ||
			strings.Contains(strings.ToLower(n.Qualified), query) {
			s.matches = append(s.matches, i)
		}
	}
	if s.matchCursor >= len(s.matches) {
		s.matchCursor = 0
	}
	if len(s.matches) > 0 {
		s.selectedIdx = s.matches[s.matchCursor]
	}
}

// confirmFilter exits fuzzy-find, keeping the current selection.
func (s *selectionState) confirmFilter() {
	if len(s.matches) > 0 {
		s.selectedIdx = s.matches[s.matchCursor]
	}
	s.filterMode = false
	s.filterText = ""
	s.matches = nil
}

// cancelFilter exits fuzzy-find without changing selection.
func (s *selectionState) cancelFilter() {
	s.filterMode = false
	s.filterText = ""
	s.matches = nil
}

// NodeInfo holds data for the selection info panel.
type NodeInfo struct {
	Name      string
	Qualified string
	Kind      string
	FilePath  string
	LineStart int
	Callers   int
	Callees   int
}

// renderInfoPanel formats the info panel for the selected node.
func renderInfoPanel(info NodeInfo, dimStyle, accentStyle lipgloss.Style) string {
	loc := info.FilePath
	if info.LineStart > 0 {
		loc = fmt.Sprintf("%s:%d", loc, info.LineStart)
	}

	left := accentStyle.Render(info.Name)
	kind := dimStyle.Render(info.Kind)
	file := dimStyle.Render(loc)
	edges := dimStyle.Render(fmt.Sprintf("↑%d ↓%d", info.Callers, info.Callees))

	parts := []string{left, kind, file, edges}
	sep := "  "
	return strings.Join(parts, sep)
}

// renderFilterOverlay shows the fuzzy-find input line.
func renderFilterOverlay(sel selectionState, dimStyle, accentStyle lipgloss.Style) string {
	prompt := accentStyle.Render("find: ")
	text := sel.filterText
	cursor := accentStyle.Render("█")
	count := dimStyle.Render(fmt.Sprintf(" (%d)", len(sel.matches)))
	return prompt + text + cursor + count
}
