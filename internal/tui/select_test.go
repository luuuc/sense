package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSelectionState_SelectNearest(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "A", X: 0.2, Y: 0.2},
			{ID: 2, Name: "B", X: 0.5, Y: 0.5},
			{ID: 3, Name: "C", X: 0.8, Y: 0.8},
		},
	}
	s := newSelectionState()
	s.selectNearest(layout, Viewport{})
	if s.selectedIdx != 1 {
		t.Errorf("expected node at center (idx 1), got %d", s.selectedIdx)
	}
}

func TestSelectionState_MoveSelection(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "A", X: 0.2, Y: 0.2},
			{ID: 2, Name: "B", X: 0.5, Y: 0.5},
			{ID: 3, Name: "C", X: 0.8, Y: 0.8},
		},
	}
	s := newSelectionState()
	s.selectedIdx = 0

	s.moveSelection(layout, 1, 1)
	if s.selectedIdx != 1 {
		t.Errorf("move right+down from A should reach B, got idx %d", s.selectedIdx)
	}

	s.moveSelection(layout, 1, 1)
	if s.selectedIdx != 2 {
		t.Errorf("move right+down from B should reach C, got idx %d", s.selectedIdx)
	}
}

func TestSelectionState_MoveNowhereWhenAlone(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "A", X: 0.5, Y: 0.5},
		},
	}
	s := newSelectionState()
	s.selectedIdx = 0
	s.moveSelection(layout, 1, 0)
	if s.selectedIdx != 0 {
		t.Errorf("single node should stay selected, got %d", s.selectedIdx)
	}
}

func TestSelectionState_FilterMatches(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "App", Qualified: "main.App"},
			{ID: 2, Name: "DB", Qualified: "db.DB"},
			{ID: 3, Name: "AppConfig", Qualified: "config.AppConfig"},
		},
	}
	s := newSelectionState()
	s.startFilter()
	s.filterText = "app"
	s.updateFilter(layout)

	if len(s.matches) != 2 {
		t.Errorf("expected 2 matches for 'app', got %d", len(s.matches))
	}
}

func TestSelectionState_FilterEmptyMatchesAll(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "App"},
			{ID: 2, Name: "DB"},
			{ID: 3, Name: "Config"},
		},
	}
	s := newSelectionState()
	s.startFilter()
	s.updateFilter(layout)

	if len(s.matches) != 3 {
		t.Errorf("empty filter should match all nodes, got %d", len(s.matches))
	}
}

func TestSelectionState_FilterConfirm(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "App"},
			{ID: 2, Name: "DB"},
		},
	}
	s := newSelectionState()
	s.startFilter()
	s.filterText = "db"
	s.updateFilter(layout)
	s.confirmFilter()

	if s.filterMode {
		t.Error("filter mode should be off after confirm")
	}
	if s.selectedIdx != 1 {
		t.Errorf("expected DB (idx 1) selected, got %d", s.selectedIdx)
	}
}

func TestSelectionState_FilterCancel(t *testing.T) {
	s := newSelectionState()
	s.selectedIdx = 0
	s.startFilter()
	s.filterText = "something"
	s.cancelFilter()

	if s.filterMode {
		t.Error("filter mode should be off after cancel")
	}
	if s.selectedIdx != 0 {
		t.Error("cancel should preserve original selection")
	}
}

func TestSelectionState_SelectedNode(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "A"},
			{ID: 2, Name: "B"},
		},
	}
	s := newSelectionState()
	if s.selectedNode(layout) != nil {
		t.Error("no selection should return nil")
	}
	s.selectedIdx = 1
	n := s.selectedNode(layout)
	if n == nil || n.ID != 2 {
		t.Errorf("expected node B, got %v", n)
	}
}

func TestMode_EnterAndExit(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24

	if m.mode != ModeNormal {
		t.Fatal("should start in normal mode")
	}

	m.enterSelectionMode()
	if m.mode != ModeSelection {
		t.Error("should be in selection mode")
	}
	if m.selection.selectedIdx < 0 {
		t.Error("should have auto-selected a node")
	}
	if m.renderer.SelectedID == 0 {
		t.Error("renderer should have selected ID set")
	}

	m.exitSelectionMode()
	if m.mode != ModeNormal {
		t.Error("should be back in normal mode")
	}
	if m.renderer.SelectedID != 0 {
		t.Error("renderer selected ID should be cleared")
	}
}

func TestModel_SelectionKeyNavigation(t *testing.T) {
	layout := &Layout{
		GraphHash: "test",
		Nodes: []LayoutNode{
			{ID: 1, Name: "Left", X: 0.2, Y: 0.5, Centrality: 1},
			{ID: 2, Name: "Center", X: 0.5, Y: 0.5, Centrality: 3},
			{ID: 3, Name: "Right", X: 0.8, Y: 0.5, Centrality: 1},
		},
		Edges: []LayoutEdge{
			{SourceID: 1, TargetID: 2, Kind: "calls", Confidence: 1.0},
		},
	}
	m := newModel(graphStats{Symbols: 3, Edges: 1}, layout, nil, nil)
	m.width = 80
	m.height = 24

	m.enterSelectionMode()
	if m.selection.selectedIdx != 1 {
		t.Fatalf("should select center node, got idx %d", m.selection.selectedIdx)
	}

	m.selection.moveSelection(layout, 1, 0)
	if m.selection.selectedIdx != 2 {
		t.Errorf("moving right should reach Right (idx 2), got %d", m.selection.selectedIdx)
	}
}

func TestStatusBar_SelectionMode(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	m.enterSelectionMode()

	bar := m.statusBar()
	if !containsText(bar, "f:find") {
		t.Errorf("selection status bar should show find hint, got %q", bar)
	}
	if !containsText(bar, "esc:back") {
		t.Errorf("selection status bar should show esc hint, got %q", bar)
	}
}

func TestRefreshNodeInfo_NilDB(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.enterSelectionMode()

	if m.nodeInfo == nil {
		t.Fatal("nodeInfo should be set after entering selection mode")
	}
	if m.nodeInfo.Name == "" {
		t.Error("nodeInfo.Name should be populated from layout node")
	}
	if m.nodeInfo.FilePath != "" {
		t.Error("filePath should be empty with nil DB")
	}
}

func TestUpdate_EndToEnd_ModeDispatch(t *testing.T) {
	m := newModel(graphStats{Symbols: 2, Edges: 1}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24

	if m.mode != ModeNormal {
		t.Fatal("should start in normal mode")
	}

	// "s" in normal mode → selection mode
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = updated.(model)
	if m.mode != ModeSelection {
		t.Errorf("'s' should enter selection mode, got %v", m.mode)
	}
	if m.selection.selectedIdx < 0 {
		t.Error("should have a selected node")
	}

	// arrow key in selection mode → moves selection (doesn't pan)
	origOffset := m.renderer.Viewport.OffsetY
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.renderer.Viewport.OffsetY != origOffset {
		t.Error("arrow key in selection mode should not pan viewport")
	}

	// "f" in selection mode → filter mode
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = updated.(model)
	if !m.selection.filterMode {
		t.Error("'f' should enter filter mode")
	}

	// Esc in filter mode → exits filter, stays in selection
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if m.selection.filterMode {
		t.Error("esc should exit filter mode")
	}
	if m.mode != ModeSelection {
		t.Error("should still be in selection mode after filter cancel")
	}

	// Esc in selection mode → back to normal
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	m = updated.(model)
	if m.mode != ModeNormal {
		t.Errorf("esc should return to normal mode, got %v", m.mode)
	}
}
