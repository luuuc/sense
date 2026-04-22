package tui

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const livePollInterval = 1 * time.Second

type livePollMsg time.Time

func livePoll() tea.Cmd {
	return tea.Tick(livePollInterval, func(t time.Time) tea.Msg {
		return livePollMsg(t)
	})
}

type liveModel struct {
	db     *sql.DB
	status StatusData
	pulse  pulseState
	dim    lipgloss.Style
	width  int
	quit   bool
}

func newLiveModel(db *sql.DB) liveModel {
	palette := DetectPalette()
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#586069"))
	if !palette.Dark {
		dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#959DA5"))
	}
	return liveModel{
		db:    db,
		pulse: newPulseState(palette.Dark),
		dim:   dim,
	}
}

func (m liveModel) Init() tea.Cmd {
	return tea.Batch(livePoll(), pulseTick())
}

func (m liveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quit = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case livePollMsg:
		m.pollDB()
		return m, livePoll()
	case pulseTickMsg:
		return m, pulseTick()
	}
	return m, nil
}

func (m *liveModel) pollDB() {
	if m.db == nil {
		return
	}
	_ = m.db.QueryRow("SELECT COUNT(*) FROM sense_symbols").Scan(&m.status.Symbols)
	_ = m.db.QueryRow("SELECT COUNT(*) FROM sense_edges").Scan(&m.status.Edges)
}

func (m liveModel) View() string {
	if m.quit {
		return ""
	}
	w := m.width
	if w == 0 {
		w = 80
	}
	return m.pulse.render(time.Now()) + " " + renderSessionStatus(m.status, w, m.dim)
}

// RunLive launches a single-line live status bar suitable for tmux or editor embedding.
func RunLive(ctx context.Context, db *sql.DB) error {
	m := newLiveModel(db)
	m.pollDB()
	p := tea.NewProgram(m, tea.WithContext(ctx))
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("live status: %w", err)
	}
	return nil
}
