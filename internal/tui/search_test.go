package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luuuc/sense/internal/search"
)

func TestScoreToColor(t *testing.T) {
	tests := []struct {
		score float64
		want  uint8
	}{
		{1.0, colorSearchHigh},
		{0.7, colorSearchHigh},
		{0.69, colorSearchMed},
		{0.4, colorSearchMed},
		{0.39, colorSearchLow},
		{0.01, colorSearchLow},
		{0.0, colorFaded},
	}
	for _, tt := range tests {
		if got := scoreToColor(tt.score); got != tt.want {
			t.Errorf("scoreToColor(%v) = %d, want %d", tt.score, got, tt.want)
		}
	}
}

func TestSearchState_ApplyOverrides(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "Matched"},
			{ID: 2, Name: "Also"},
			{ID: 3, Name: "NotMatched"},
		},
	}
	ss := &searchState{
		results: []search.Result{
			{SymbolID: 1, Score: 0.9},
			{SymbolID: 2, Score: 0.3},
		},
	}
	overrides := ss.applyOverrides(layout)

	if overrides[1] != colorSearchHigh {
		t.Errorf("high-score node: got %d, want %d", overrides[1], colorSearchHigh)
	}
	if overrides[2] != colorSearchLow {
		t.Errorf("low-score node: got %d, want %d", overrides[2], colorSearchLow)
	}
	if overrides[3] != colorFaded {
		t.Errorf("unmatched node: got %d, want %d", overrides[3], colorFaded)
	}
}

func TestSearchState_ApplyOverrides_NilLayout(t *testing.T) {
	ss := &searchState{
		results: []search.Result{{SymbolID: 1, Score: 0.8}},
	}
	if overrides := ss.applyOverrides(nil); overrides != nil {
		t.Error("nil layout should return nil overrides")
	}
}

func TestSearchState_SelectedSymbolID(t *testing.T) {
	ss := &searchState{
		results: []search.Result{
			{SymbolID: 10},
			{SymbolID: 20},
			{SymbolID: 30},
		},
		cursor: 1,
	}
	if got := ss.selectedSymbolID(); got != 20 {
		t.Errorf("cursor=1: got %d, want 20", got)
	}

	ss.cursor = -1
	if got := ss.selectedSymbolID(); got != 0 {
		t.Errorf("cursor=-1: got %d, want 0", got)
	}

	ss.cursor = 5
	if got := ss.selectedSymbolID(); got != 0 {
		t.Errorf("cursor=5 (out of range): got %d, want 0", got)
	}
}

func TestSearchState_ScheduleSearch(t *testing.T) {
	ss := newSearchState()
	id1, cmd1 := ss.scheduleSearch()
	if cmd1 == nil {
		t.Error("scheduleSearch should return a cmd")
	}
	if !ss.pending {
		t.Error("pending should be true after scheduleSearch")
	}

	id2, _ := ss.scheduleSearch()
	if id2 <= id1 {
		t.Errorf("debounceID should increment: %d <= %d", id2, id1)
	}
}

func TestEnterSearchMode_NilEngine(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24

	updated, cmd := m.enterSearchMode()
	um := updated.(model)
	if um.mode != ModeNormal {
		t.Errorf("nil engine should not enter search mode, got %v", um.mode)
	}
	if cmd != nil {
		t.Error("nil engine should not return a cmd")
	}
}

func TestExitSearchMode(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.mode = ModeSearch
	m.searchState = &searchState{query: "test"}
	m.renderer.NodeColorOverride = map[int64]uint8{1: colorSearchHigh}
	m.renderer.SelectedID = 42

	m.exitSearchMode()

	if m.mode != ModeNormal {
		t.Errorf("should return to normal, got %v", m.mode)
	}
	if m.searchState != nil {
		t.Error("searchState should be nil")
	}
	if m.renderer.NodeColorOverride != nil {
		t.Error("overrides should be cleared")
	}
	if m.renderer.SelectedID != 0 {
		t.Error("SelectedID should be cleared")
	}
}

func TestHandleSearchKey_Backspace_ClearsOverrides(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{query: "a"}
	m.renderer.NodeColorOverride = map[int64]uint8{1: colorSearchHigh}

	updated, _ := m.handleSearchKey(tea.KeyMsg{Type: tea.KeyBackspace})
	um := updated.(model)

	if um.searchState.query != "" {
		t.Errorf("query should be empty, got %q", um.searchState.query)
	}
	if um.renderer.NodeColorOverride != nil {
		t.Error("overrides should be cleared when query emptied")
	}
}

func TestHandleSearchKey_CursorNavigation(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{
		results: []search.Result{
			{SymbolID: 10, Score: 0.9},
			{SymbolID: 20, Score: 0.5},
		},
		cursor: 0,
	}

	updated, _ := m.handleSearchKey(tea.KeyMsg{Type: tea.KeyDown})
	um := updated.(model)
	if um.searchState.cursor != 1 {
		t.Errorf("down: cursor should be 1, got %d", um.searchState.cursor)
	}
	if um.renderer.SelectedID != 20 {
		t.Errorf("down: SelectedID should track cursor, got %d", um.renderer.SelectedID)
	}

	updated, _ = um.handleSearchKey(tea.KeyMsg{Type: tea.KeyDown})
	um = updated.(model)
	if um.searchState.cursor != 1 {
		t.Errorf("down at end: cursor should stay 1, got %d", um.searchState.cursor)
	}

	updated, _ = um.handleSearchKey(tea.KeyMsg{Type: tea.KeyUp})
	um = updated.(model)
	if um.searchState.cursor != 0 {
		t.Errorf("up: cursor should be 0, got %d", um.searchState.cursor)
	}
}

func TestUpdate_SearchEscReturnsToNormal(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{query: "test"}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	um := updated.(model)
	if um.mode != ModeNormal {
		t.Errorf("esc in search mode should return to normal, got %v", um.mode)
	}
}

func TestUpdate_SearchResultMsg(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{debounceID: 5}

	results := []search.Result{
		{SymbolID: 1, Name: "Foo", Score: 0.8},
		{SymbolID: 2, Name: "Bar", Score: 0.3},
	}
	updated, _ := m.Update(searchResultMsg{results: results, id: 5})
	um := updated.(model)

	if len(um.searchState.results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(um.searchState.results))
	}
	if um.searchState.cursor != 0 {
		t.Error("cursor should reset to 0")
	}
	if um.searchState.pending {
		t.Error("pending should be false")
	}
	if um.renderer.SelectedID != 1 {
		t.Errorf("SelectedID should be first result, got %d", um.renderer.SelectedID)
	}
	if um.renderer.NodeColorOverride == nil {
		t.Error("overrides should be set")
	}
}

func TestUpdate_SearchResultMsg_StaleID(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{debounceID: 5}

	updated, _ := m.Update(searchResultMsg{
		results: []search.Result{{SymbolID: 1, Score: 0.9}},
		id:      3,
	})
	um := updated.(model)

	if len(um.searchState.results) != 0 {
		t.Error("stale result (id=3 vs debounceID=5) should be ignored")
	}
}

func TestStatusBar_SearchMode(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	m.mode = ModeSearch

	bar := m.statusBar()
	if !containsText(bar, "esc:back") {
		t.Errorf("search status bar should show esc hint, got %q", bar)
	}
	if !containsText(bar, "enter:blast") {
		t.Errorf("search status bar should show enter hint, got %q", bar)
	}
}

func TestStatusBar_NormalMode_ShowsSearch(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24

	bar := m.statusBar()
	if !containsText(bar, "/:search") {
		t.Errorf("normal status bar should show search hint, got %q", bar)
	}
}

func TestSelectNodeByID(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.selectNodeByID(2)

	if m.mode != ModeSelection {
		t.Errorf("should enter selection mode, got %v", m.mode)
	}
	if m.renderer.SelectedID != 2 {
		t.Errorf("SelectedID should be 2, got %d", m.renderer.SelectedID)
	}
}

func TestSearchEnter_TriggersBlastTransition_NilDB(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{
		results: []search.Result{
			{SymbolID: 1, Name: "Foo", Score: 0.9},
		},
		cursor: 0,
	}

	updated, cmd := m.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(model)

	if um.searchState != nil {
		t.Error("search state should be cleared after Enter")
	}
	if um.mode != ModeSelection {
		t.Errorf("nil DB: should fall back to selection, got %v", um.mode)
	}
	if um.renderer.SelectedID != 1 {
		t.Errorf("selected node should be 1, got %d", um.renderer.SelectedID)
	}
	if um.blast != nil {
		t.Error("nil DB should not create blast state")
	}
	if cmd != nil {
		t.Error("nil DB should not return a cmd")
	}
}

func TestSearchEnter_NoResults(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{query: "nothing"}

	updated, _ := m.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	um := updated.(model)

	if um.mode != ModeSearch {
		t.Errorf("Enter with no results should stay in search, got %v", um.mode)
	}
}

func TestSelectNodeByID_NotFound(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.mode = ModeNormal
	m.selectNodeByID(999)

	if m.mode != ModeNormal {
		t.Errorf("non-existent ID should not change mode, got %v", m.mode)
	}
}
