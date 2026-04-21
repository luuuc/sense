package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luuuc/sense/internal/sqlite"
)

// graphStats holds the summary counts displayed in the initial view.
type graphStats struct {
	Symbols int
	Edges   int
}

// Run launches the TUI using the given adapter. The caller must ensure
// stdout is a TTY before calling this. The caller owns the adapter lifetime.
func Run(ctx context.Context, adapter *sqlite.Adapter, senseDir string) error {
	stats, err := loadStats(ctx, adapter)
	if err != nil {
		return fmt.Errorf("load graph stats: %w", err)
	}

	layout, err := LoadLayout(senseDir)
	if err != nil {
		return fmt.Errorf("load layout: %w", err)
	}
	if layout == nil {
		layout, err = ComputeAndCacheLayout(ctx, adapter, senseDir)
		if err != nil {
			return fmt.Errorf("compute layout: %w", err)
		}
	}

	m := newModel(stats, layout)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = p.Run()
	return err
}

func loadStats(ctx context.Context, adapter *sqlite.Adapter) (graphStats, error) {
	symbols, err := adapter.SymbolCount(ctx)
	if err != nil {
		return graphStats{}, err
	}
	db := adapter.DB()
	var edges int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_edges").Scan(&edges); err != nil {
		return graphStats{}, err
	}
	return graphStats{Symbols: symbols, Edges: edges}, nil
}

// SenseDir resolves the .sense directory for a given project root.
func SenseDir(root string) string {
	return filepath.Join(root, ".sense")
}

type model struct {
	stats      graphStats
	renderer   *GraphRenderer
	anim       animation
	width      int
	height     int
	quit       bool
	dimStyle   lipgloss.Style
	accentStyle lipgloss.Style
}

func newModel(stats graphStats, layout *Layout) model {
	nodeCount := 0
	if layout != nil {
		nodeCount = len(layout.Nodes)
	}
	palette := DetectPalette()
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#586069"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#ABB2BF"))
	if !palette.Dark {
		dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#959DA5"))
		accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#24292E"))
	}
	return model{
		stats: stats,
		renderer: &GraphRenderer{
			Layout:  layout,
			Mode:    DetectRenderMode(),
			Palette: palette,
		},
		anim:        newAnimation(nodeCount),
		dimStyle:    dim,
		accentStyle: accent,
	}
}

func (m model) Init() tea.Cmd {
	return m.anim.start()
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quit = true
			return m, tea.Quit
		case "up", "k":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(0, -step)
		case "down", "j":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(0, step)
		case "left", "h":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(-step, 0)
		case "right", "l":
			step := m.renderer.Viewport.PanStep()
			m.renderer.Viewport.Pan(step, 0)
		case "+", "=":
			m.renderer.Viewport.ZoomIn()
		case "-", "_":
			m.renderer.Viewport.ZoomOut()
		case "tab":
			m.renderer.Lens = m.renderer.Lens.Next()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case animTickMsg:
		count, cmd := m.anim.update()
		m.renderer.VisibleCount = count
		return m, cmd
	}
	return m, nil
}

func (m model) View() string {
	if m.quit {
		return ""
	}
	if m.width == 0 || m.height == 0 {
		return "loading..."
	}

	graphRows := m.height - 1
	if graphRows < 1 {
		graphRows = 1
	}

	graph := m.renderer.Render(m.width, graphRows)
	status := m.statusBar()
	return graph + "\n" + status
}

func (m model) statusBar() string {
	left := fmt.Sprintf("%d symbols  %d edges", m.stats.Symbols, m.stats.Edges)
	center := fmt.Sprintf("lens:%s  zoom:%s", m.renderer.Lens, m.renderer.Viewport.Zoom)
	hints := "hjkl:pan  +/-:zoom  tab:lens  q:quit"

	if m.width < 60 {
		hints = "q:quit"
	} else if m.width < 90 {
		hints = "tab:lens  +/-:zoom  q:quit"
	}

	gap := m.width - lipgloss.Width(left) - lipgloss.Width(center) - lipgloss.Width(hints)
	if gap < 2 {
		return m.dimStyle.Render(left) + "  " + m.accentStyle.Render(center)
	}

	leftPad := gap / 2
	rightPad := gap - leftPad
	return m.dimStyle.Render(left) + strings.Repeat(" ", leftPad) + m.accentStyle.Render(center) + strings.Repeat(" ", rightPad) + m.dimStyle.Render(hints)
}
