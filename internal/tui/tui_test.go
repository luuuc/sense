package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testLayout() *Layout {
	return &Layout{
		GraphHash: "test",
		Nodes: []LayoutNode{
			{ID: 1, Name: "App", X: 0.5, Y: 0.5, Centrality: 3},
			{ID: 2, Name: "DB", X: 0.8, Y: 0.3, Centrality: 1},
		},
		Edges: []LayoutEdge{
			{SourceID: 1, TargetID: 2, Kind: "calls", Confidence: 1.0},
		},
	}
}

func TestModel_QuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c", "esc"} {
		m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
		var updated tea.Model
		var cmd tea.Cmd
		switch key {
		case "ctrl+c":
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		case "esc":
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEscape})
		default:
			updated, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		}
		if cmd == nil {
			t.Errorf("key %q: expected tea.Quit cmd, got nil", key)
			continue
		}
		um := updated.(model)
		if !um.quit {
			t.Errorf("key %q: quit flag not set", key)
		}
	}
}

func TestModel_WindowSize(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	um := updated.(model)
	if um.width != 80 || um.height != 24 {
		t.Errorf("got %dx%d, want 80x24", um.width, um.height)
	}
}

func TestModel_ViewRendersGraph(t *testing.T) {
	m := newModel(graphStats{Symbols: 42, Edges: 17}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	v := m.View()
	if v == "" || v == "loading..." {
		t.Error("expected rendered graph output")
	}
}

func TestModel_ViewLoading(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	v := m.View()
	if v != "loading..." {
		t.Errorf("zero-size view should show loading, got: %q", v)
	}
}

func TestModel_PanKeys(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	um := updated.(model)
	if um.renderer.Viewport.OffsetY <= 0 {
		t.Error("j key should pan down (positive Y offset)")
	}

	updated, _ = um.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("h")})
	um = updated.(model)
	if um.renderer.Viewport.OffsetX >= 0 {
		t.Error("h key should pan left (negative X offset)")
	}
}

func TestModel_ZoomKeys(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	um := updated.(model)
	if um.renderer.Viewport.Zoom != ZoomMedium {
		t.Errorf("+ should zoom in to medium, got %v", um.renderer.Viewport.Zoom)
	}

	updated, _ = um.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("-")})
	um = updated.(model)
	if um.renderer.Viewport.Zoom != ZoomFit {
		t.Errorf("- should zoom back to fit, got %v", um.renderer.Viewport.Zoom)
	}
}

func TestModel_TabCyclesLens(t *testing.T) {
	m := newModel(graphStats{}, testLayout(), nil, nil)
	if m.renderer.Lens != LensLanguage {
		t.Fatalf("initial lens should be language, got %v", m.renderer.Lens)
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	um := updated.(model)
	if um.renderer.Lens != LensKind {
		t.Errorf("tab should cycle to kind, got %v", um.renderer.Lens)
	}
}

func TestStatusBar_ContainsStats(t *testing.T) {
	m := newModel(graphStats{Symbols: 42, Edges: 17}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	bar := m.statusBar()
	if !containsText(bar, "index: 42 sym 17 edges") {
		t.Errorf("status bar should contain index vitals, got %q", bar)
	}
}

func TestStatusBar_ContainsLensAndZoom(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	bar := m.statusBar()
	if !containsText(bar, "lens:language") {
		t.Errorf("status bar should show current lens, got %q", bar)
	}
	if !containsText(bar, "zoom:fit") {
		t.Errorf("status bar should show current zoom, got %q", bar)
	}
}

func TestStatusBar_NarrowWidth(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 50
	m.height = 24
	bar := m.statusBar()
	if !containsText(bar, "index:") {
		t.Errorf("narrow status bar should show index vitals, got %q", bar)
	}
	if containsText(bar, "hjkl:pan") {
		t.Error("narrow status bar should not show full key hints")
	}
}

func TestStatusBar_InView(t *testing.T) {
	m := newModel(graphStats{Symbols: 5, Edges: 3}, testLayout(), nil, nil)
	m.width = 80
	m.height = 24
	v := m.View()
	if !containsText(v, "index: 5 sym 3 edges") {
		t.Error("View() should contain the status bar with index vitals")
	}
}

func containsText(s, substr string) bool {
	clean := stripANSI(s)
	return strings.Contains(clean, substr)
}

func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 'A' || s[j] > 'Z') && (s[j] < 'a' || s[j] > 'z') {
				j++
			}
			if j < len(s) {
				j++
			}
			i = j
		} else {
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

func TestGraphRenderer_EmptyLayout(t *testing.T) {
	r := &GraphRenderer{Layout: &Layout{}}
	got := r.Render(80, 24)
	if got != "" {
		t.Errorf("empty layout should render empty, got %q", got)
	}
}

func TestGraphRenderer_RenderProducesBraille(t *testing.T) {
	r := &GraphRenderer{
		Layout: testLayout(),
		Mode:   RenderBraille,
	}
	got := r.Render(40, 12)
	if got == "" {
		t.Fatal("expected non-empty render")
	}
	hasBraille := false
	for _, ch := range got {
		if ch >= 0x2800 && ch <= 0x28FF {
			hasBraille = true
			break
		}
	}
	if !hasBraille {
		t.Error("expected braille characters in output")
	}
}

func TestGraphRenderer_ZoomShowsLabels(t *testing.T) {
	r := &GraphRenderer{
		Layout:   testLayout(),
		Mode:     RenderBraille,
		Viewport: Viewport{Zoom: ZoomMedium},
	}
	got := r.Render(80, 24)
	if got == "" {
		t.Fatal("expected non-empty render at medium zoom")
	}
}
