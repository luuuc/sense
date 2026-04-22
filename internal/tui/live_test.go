package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestLiveModel_View(t *testing.T) {
	m := newLiveModel(nil)
	m.width = 80
	m.status = StatusData{Symbols: 42, Edges: 10}

	v := m.View()
	if !containsText(v, "42 sym") {
		t.Errorf("expected symbol count, got %q", v)
	}
	if !containsText(v, "10 edges") {
		t.Errorf("expected edge count, got %q", v)
	}
}

func TestLiveModel_ViewDefaultWidth(t *testing.T) {
	m := newLiveModel(nil)
	m.status = StatusData{Symbols: 10, Edges: 5}

	v := m.View()
	if v == "" {
		t.Error("expected non-empty view at default width")
	}
}

func TestLiveModel_QuitOnQ(t *testing.T) {
	m := newLiveModel(nil)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	um := updated.(liveModel)
	if !um.quit {
		t.Error("q should set quit")
	}
	if cmd == nil {
		t.Error("q should return tea.Quit cmd")
	}
}

func TestLiveModel_WindowSize(t *testing.T) {
	m := newLiveModel(nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 1})
	um := updated.(liveModel)
	if um.width != 120 {
		t.Errorf("expected width 120, got %d", um.width)
	}
}

func TestLiveModel_PollTick(t *testing.T) {
	m := newLiveModel(nil)
	_, cmd := m.Update(livePollMsg(time.Now()))
	if cmd == nil {
		t.Error("poll tick should schedule next poll")
	}
}

func TestLiveModel_QuitView(t *testing.T) {
	m := newLiveModel(nil)
	m.quit = true
	if v := m.View(); v != "" {
		t.Errorf("quit view should be empty, got %q", v)
	}
}
