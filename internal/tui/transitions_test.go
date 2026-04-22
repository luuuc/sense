package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luuuc/sense/internal/search"
)

type transition struct {
	name     string
	from     Mode
	key      tea.KeyMsg
	setup    func(m *model)
	wantMode Mode
	wantQuit bool
}

func escKey() tea.KeyMsg   { return tea.KeyMsg{Type: tea.KeyEscape} }
func enterKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} }
func runeKey(r string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}
}

func TestTransitionTable(t *testing.T) {
	transitions := []transition{
		{
			name:     "Normal + Enter → Selection",
			from:     ModeNormal,
			key:      enterKey(),
			wantMode: ModeSelection,
		},
		{
			name:     "Normal + s → Selection",
			from:     ModeNormal,
			key:      runeKey("s"),
			wantMode: ModeSelection,
		},
		{
			name:     "Normal + / → Search",
			from:     ModeNormal,
			key:      runeKey("/"),
			setup:    func(m *model) { m.searchEngine = &search.Engine{} },
			wantMode: ModeSearch,
		},
		{
			name:     "Normal + Esc → quit",
			from:     ModeNormal,
			key:      escKey(),
			wantMode: ModeNormal,
			wantQuit: true,
		},
		{
			name:     "Normal + / (nil engine) → stays Normal",
			from:     ModeNormal,
			key:      runeKey("/"),
			wantMode: ModeNormal,
		},
		{
			name:     "Selection + Esc → Normal",
			from:     ModeSelection,
			key:      escKey(),
			wantMode: ModeNormal,
		},
		{
			name:     "Selection + / → Search",
			from:     ModeSelection,
			key:      runeKey("/"),
			setup:    func(m *model) { m.searchEngine = &search.Engine{} },
			wantMode: ModeSearch,
		},
		{
			name: "Blast + Esc → Selection",
			from: ModeBlast,
			key:  escKey(),
			setup: func(m *model) {
				m.blast = &blastState{hopMap: map[int64]int{1: 0}, frame: blastFrames}
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject}
			},
			wantMode: ModeSelection,
		},
		{
			name: "Blast + / → Search (clears blast)",
			from: ModeBlast,
			key:  runeKey("/"),
			setup: func(m *model) {
				m.searchEngine = &search.Engine{}
				m.blast = &blastState{hopMap: map[int64]int{1: 0}, frame: blastFrames}
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject}
			},
			wantMode: ModeSearch,
		},
		{
			name: "Search + Esc → Normal",
			from: ModeSearch,
			key:  escKey(),
			setup: func(m *model) {
				m.searchState = newSearchState()
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorSearchHigh}
			},
			wantMode: ModeNormal,
		},
	}

	for _, tt := range transitions {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(graphStats{}, testLayout(), nil, nil)
			m.width = 80
			m.height = 24
			m.mode = tt.from
			if tt.from == ModeSelection {
				m.selection.selectedIdx = 0
			}
			if tt.setup != nil {
				tt.setup(&m)
			}

			updated, _ := m.Update(tt.key)
			um := updated.(model)

			if tt.wantQuit {
				if !um.quit {
					t.Error("expected quit")
				}
				return
			}

			if um.mode != tt.wantMode {
				t.Errorf("got mode %v, want %v", um.mode, tt.wantMode)
			}
		})
	}
}

func TestTransition_NoOrphanedOverrides(t *testing.T) {
	tests := []struct {
		name  string
		setup func(m *model)
		key   tea.KeyMsg
	}{
		{
			name: "Blast → Selection clears overrides",
			setup: func(m *model) {
				m.mode = ModeBlast
				m.blast = &blastState{hopMap: map[int64]int{1: 0}, frame: blastFrames}
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject}
			},
			key: escKey(),
		},
		{
			name: "Blast → Search clears blast overrides",
			setup: func(m *model) {
				m.mode = ModeBlast
				m.searchEngine = &search.Engine{}
				m.blast = &blastState{hopMap: map[int64]int{1: 0}, frame: blastFrames}
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject}
			},
			key: runeKey("/"),
		},
		{
			name: "Search → Normal clears overrides",
			setup: func(m *model) {
				m.mode = ModeSearch
				m.searchState = &searchState{query: "test", results: []search.Result{{SymbolID: 1, Score: 0.9}}}
				m.renderer.NodeColorOverride = map[int64]uint8{1: colorSearchHigh}
				m.renderer.SelectedID = 1
			},
			key: escKey(),
		},
		{
			name: "Selection → Normal clears SelectedID",
			setup: func(m *model) {
				m.mode = ModeSelection
				m.selection.selectedIdx = 0
				m.renderer.SelectedID = 1
			},
			key: escKey(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newModel(graphStats{}, testLayout(), nil, nil)
			m.width = 80
			m.height = 24
			tt.setup(&m)

			updated, _ := m.Update(tt.key)
			um := updated.(model)

			if um.renderer.NodeColorOverride != nil {
				t.Errorf("NodeColorOverride should be nil, got %v", um.renderer.NodeColorOverride)
			}
			if um.renderer.SelectedID != 0 {
				t.Errorf("SelectedID should be 0, got %d", um.renderer.SelectedID)
			}
		})
	}
}

func TestTransition_BlastToSearch_ClearsBlastState(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeBlast
	m.searchEngine = &search.Engine{}
	m.blast = &blastState{
		hopMap: map[int64]int{1: 0, 2: 1},
		frame:  blastFrames,
	}
	m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject, 2: colorBlastHop1}

	updated, _ := m.Update(runeKey("/"))
	um := updated.(model)

	if um.mode != ModeSearch {
		t.Errorf("expected ModeSearch, got %v", um.mode)
	}
	if um.blast != nil {
		t.Error("blast state should be nil after transitioning to search")
	}
	if um.searchState == nil {
		t.Error("searchState should be initialized")
	}
}

func TestTransition_EscAlwaysGoesBack(t *testing.T) {
	tests := []struct {
		from Mode
		want Mode
	}{
		{ModeBlast, ModeSelection},
		{ModeSearch, ModeNormal},
		{ModeSelection, ModeNormal},
	}
	for _, tt := range tests {
		t.Run(tt.from.String()+"→"+tt.want.String(), func(t *testing.T) {
			m := newModel(graphStats{}, testLayout(), nil, nil)
			m.width = 80
			m.height = 24
			m.mode = tt.from
			if tt.from == ModeSelection {
				m.selection.selectedIdx = 0
			}
			if tt.from == ModeBlast {
				m.blast = &blastState{hopMap: map[int64]int{1: 0}, frame: blastFrames}
			}
			if tt.from == ModeSearch {
				m.searchState = newSearchState()
			}

			updated, _ := m.Update(escKey())
			um := updated.(model)
			if um.mode != tt.want {
				t.Errorf("Esc from %v: got %v, want %v", tt.from, um.mode, tt.want)
			}
		})
	}
}
