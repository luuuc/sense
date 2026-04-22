package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luuuc/sense/internal/blast"
	sensemodel "github.com/luuuc/sense/internal/model"
)

func TestBlastState_VisibleHops(t *testing.T) {
	bs := &blastState{}

	bs.frame = 0
	if h := bs.visibleHops(); h != 0 {
		t.Errorf("frame 0: expected 0 hops, got %d", h)
	}

	bs.frame = blastFrames / 2
	h := bs.visibleHops()
	if h < 1 || h > 3 {
		t.Errorf("mid-frame: expected 1-3 hops, got %d", h)
	}

	bs.frame = blastFrames
	if h := bs.visibleHops(); h != 3 {
		t.Errorf("final frame: expected 3 hops, got %d", h)
	}
}

func TestBlastColorForHop(t *testing.T) {
	tests := []struct {
		hop  int
		want uint8
	}{
		{0, colorBlastSubject},
		{1, colorBlastHop1},
		{2, colorBlastHop2},
		{3, colorBlastHop3},
		{5, colorBlastHop3},
	}
	for _, tt := range tests {
		if got := blastColorForHop(tt.hop); got != tt.want {
			t.Errorf("hop %d: got %d, want %d", tt.hop, got, tt.want)
		}
	}
}

func TestBlastState_StatusText(t *testing.T) {
	bs := &blastState{
		result: blast.Result{
			Symbol:        sensemodel.Symbol{Name: "Foo"},
			Risk:          "high",
			DirectCallers: []sensemodel.Symbol{{ID: 1}, {ID: 2}},
			TotalAffected: 5,
			AffectedTests: []string{"test_foo.go"},
		},
	}
	text := bs.statusText()
	if !containsText(text, "risk:high") {
		t.Errorf("expected risk in status, got %q", text)
	}
	if !containsText(text, "callers:2") {
		t.Errorf("expected callers count, got %q", text)
	}
	if !containsText(text, "affected:5") {
		t.Errorf("expected affected count, got %q", text)
	}
	if !containsText(text, "tests:1") {
		t.Errorf("expected test count, got %q", text)
	}
}

func TestApplyBlastOverrides_Normal(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "Subject"},
			{ID: 2, Name: "Caller"},
			{ID: 3, Name: "Unrelated"},
		},
	}
	m := newModel(graphStats{}, layout, nil, nil)
	m.blast = &blastState{
		hopMap: map[int64]int{1: 0, 2: 1},
		frame:  blastFrames,
	}
	m.applyBlastOverrides()

	overrides := m.renderer.NodeColorOverride
	if overrides[1] != colorBlastSubject {
		t.Errorf("subject should be bright, got %d", overrides[1])
	}
	if overrides[2] != colorBlastHop1 {
		t.Errorf("direct caller should be hop1 color, got %d", overrides[2])
	}
	if overrides[3] != colorFaded {
		t.Errorf("unrelated should be faded, got %d", overrides[3])
	}
}

func TestApplyBlastOverrides_HubNode(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "Hub"},
			{ID: 2, Name: "A"},
			{ID: 3, Name: "B"},
		},
	}
	m := newModel(graphStats{}, layout, nil, nil)
	m.blast = &blastState{
		hopMap:  map[int64]int{1: 0, 2: 1, 3: 2},
		frame:   blastFrames,
		hubNode: true,
	}
	m.applyBlastOverrides()

	overrides := m.renderer.NodeColorOverride
	if overrides[1] != colorBlastSubject {
		t.Errorf("hub subject should still be bright, got %d", overrides[1])
	}
	if overrides[2] != colorFaded {
		t.Errorf("hub callers should be faded (summary mode), got %d", overrides[2])
	}
}

func TestApplyBlastOverrides_AnimationProgression(t *testing.T) {
	layout := &Layout{
		Nodes: []LayoutNode{
			{ID: 1, Name: "Subject"},
			{ID: 2, Name: "Hop1"},
			{ID: 3, Name: "Hop2"},
			{ID: 4, Name: "Hop3"},
		},
	}
	m := newModel(graphStats{}, layout, nil, nil)
	m.blast = &blastState{
		hopMap: map[int64]int{1: 0, 2: 1, 3: 2, 4: 3},
		frame:  0,
	}

	m.applyBlastOverrides()
	if m.renderer.NodeColorOverride[2] != colorFaded {
		t.Error("frame 0: hop1 should be faded (not yet revealed)")
	}

	m.blast.frame = blastFrames
	m.applyBlastOverrides()
	if m.renderer.NodeColorOverride[2] != colorBlastHop1 {
		t.Error("final frame: hop1 should be illuminated")
	}
	if m.renderer.NodeColorOverride[4] != colorBlastHop3 {
		t.Error("final frame: hop3 should be illuminated")
	}
}

func TestExitBlastMode_ReturnsToSelection(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.mode = ModeBlast
	m.selection.selectedIdx = 0
	m.blast = &blastState{
		hopMap: map[int64]int{1: 0},
	}
	m.renderer.NodeColorOverride = map[int64]uint8{1: colorBlastSubject}

	m.exitBlastMode()

	if m.mode != ModeSelection {
		t.Errorf("should return to selection mode, got %v", m.mode)
	}
	if m.blast != nil {
		t.Error("blast state should be nil")
	}
	if m.renderer.NodeColorOverride != nil {
		t.Error("node color overrides should be cleared")
	}
	if m.selection.selectedIdx != 0 {
		t.Error("selection should be preserved")
	}
}

func TestUpdate_BlastEscReturnsToSelection(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.mode = ModeBlast
	m.selection.selectedIdx = 0
	m.blast = &blastState{
		hopMap: map[int64]int{1: 0},
		frame:  blastFrames,
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEscape})
	um := updated.(model)
	if um.mode != ModeSelection {
		t.Errorf("esc in blast mode should return to selection, got %v", um.mode)
	}
	if um.selection.selectedIdx != 0 {
		t.Error("selection should be preserved after blast exit")
	}
}

func TestTriggerBlast_NilDB(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	m.enterSelectionMode()
	origMode := m.mode
	origIdx := m.selection.selectedIdx

	updated, cmd := m.triggerBlast()
	um := updated.(model)

	if um.mode != origMode {
		t.Errorf("nil DB should not change mode, got %v", um.mode)
	}
	if um.selection.selectedIdx != origIdx {
		t.Error("nil DB should not change selection")
	}
	if cmd != nil {
		t.Error("nil DB should not return a command")
	}
	if um.blast != nil {
		t.Error("nil DB should not create blast state")
	}
}

func TestHubNodeDetection_Boundary(t *testing.T) {
	tests := []struct {
		name     string
		total    int
		affected int
		wantHub  bool
	}{
		{"below half", 10, 4, false},
		{"exactly half", 10, 5, false},
		{"above half", 10, 6, true},
		{"all affected", 4, 4, true},
		{"single node", 1, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.total > 0 && tt.affected > tt.total/2
			if got != tt.wantHub {
				t.Errorf("total=%d affected=%d: got hub=%v, want %v",
					tt.total, tt.affected, got, tt.wantHub)
			}
		})
	}
}

func TestVisibleHops_PerFrameProgression(t *testing.T) {
	bs := &blastState{}
	for frame := 0; frame < blastFrames; frame++ {
		bs.frame = frame
		got := bs.visibleHops()
		if got != frame {
			t.Errorf("frame %d: visibleHops should be %d, got %d", frame, frame, got)
		}
	}
}

func TestStatusBar_BlastMode(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	m.mode = ModeBlast

	bar := m.statusBar()
	if !containsText(bar, "esc:back") {
		t.Errorf("blast status bar should show esc hint, got %q", bar)
	}
}
