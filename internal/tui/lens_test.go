package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestLens_String(t *testing.T) {
	tests := []struct {
		lens Lens
		want string
	}{
		{LensLanguage, "language"},
		{LensKind, "kind"},
		{LensCluster, "cluster"},
		{Lens(99), "?"},
	}
	for _, tt := range tests {
		if got := tt.lens.String(); got != tt.want {
			t.Errorf("Lens(%d).String() = %q, want %q", tt.lens, got, tt.want)
		}
	}
}

func TestLens_Next(t *testing.T) {
	l := LensLanguage
	l = l.Next()
	if l != LensKind {
		t.Errorf("LensLanguage.Next() = %v, want LensKind", l)
	}
	l = l.Next()
	if l != LensCluster {
		t.Errorf("LensKind.Next() = %v, want LensCluster", l)
	}
	l = l.Next()
	if l != LensLanguage {
		t.Errorf("LensCluster.Next() = %v, want LensLanguage (wrap)", l)
	}
}

func testPalette() ColorPalette {
	return ColorPalette{
		Dark: true,
		Colors: []lipgloss.Color{
			lipgloss.Color("#61AFEF"),
			lipgloss.Color("#98C379"),
			lipgloss.Color("#E5C07B"),
			lipgloss.Color("#C678DD"),
			lipgloss.Color("#E06C75"),
			lipgloss.Color("#56B6C2"),
			lipgloss.Color("#D19A66"),
			lipgloss.Color("#ABB2BF"),
		},
	}
}

func TestNodeColor_Language(t *testing.T) {
	p := testPalette()
	goNode := LayoutNode{Language: "go", Kind: "function"}
	pyNode := LayoutNode{Language: "python", Kind: "function"}

	goColor := NodeColor(goNode, LensLanguage, p)
	pyColor := NodeColor(pyNode, LensLanguage, p)

	if goColor == pyColor {
		t.Error("go and python should have different colors under language lens")
	}
}

func TestNodeColor_Kind(t *testing.T) {
	p := testPalette()
	funcNode := LayoutNode{Language: "go", Kind: "function"}
	typeNode := LayoutNode{Language: "go", Kind: "type"}

	funcColor := NodeColor(funcNode, LensKind, p)
	typeColor := NodeColor(typeNode, LensKind, p)

	if funcColor == typeColor {
		t.Error("function and type should have different colors under kind lens")
	}
}

func TestNodeColor_SameInputStableOutput(t *testing.T) {
	p := testPalette()
	n := LayoutNode{Language: "go", Kind: "function"}
	c1 := NodeColor(n, LensLanguage, p)
	c2 := NodeColor(n, LensLanguage, p)
	if c1 != c2 {
		t.Error("same node should produce same color")
	}
}

func TestLanguageColor_KnownLanguages(t *testing.T) {
	p := testPalette()
	goColor := LanguageColor("go", p)
	if goColor != p.Colors[0] {
		t.Errorf("go should map to blue (index 0), got %v", goColor)
	}
	pyColor := LanguageColor("python", p)
	if pyColor != p.Colors[1] {
		t.Errorf("python should map to green (index 1), got %v", pyColor)
	}
}

func TestLanguageColor_UnknownFallsBackToHash(t *testing.T) {
	p := testPalette()
	color := LanguageColor("brainfuck", p)
	found := false
	for _, c := range p.Colors {
		if c == color {
			found = true
			break
		}
	}
	if !found {
		t.Error("unknown language color should be from the palette")
	}
}

func TestDetectPalette_HasColors(t *testing.T) {
	p := DetectPalette()
	if len(p.Colors) == 0 {
		t.Error("palette should have colors")
	}
	if len(p.Colors) != 8 {
		t.Errorf("palette should have 8 colors, got %d", len(p.Colors))
	}
}

func TestClusterKey_Qualified(t *testing.T) {
	n := LayoutNode{Qualified: "models.User", Language: "go"}
	key := clusterKey(n)
	if key != "models" {
		t.Errorf("clusterKey(%q) = %q, want %q", n.Qualified, key, "models")
	}
}

func TestClusterKey_NoPackage(t *testing.T) {
	n := LayoutNode{Qualified: "main", Language: "go"}
	key := clusterKey(n)
	if key != "go" {
		t.Errorf("clusterKey for unqualified should fall back to language, got %q", key)
	}
}

func TestRenderColored_ProducesColoredOutput(t *testing.T) {
	layout := rendererTestLayout()
	for i := range layout.Nodes {
		layout.Nodes[i].Language = []string{"go", "python", "ruby"}[i%3]
	}
	r := &GraphRenderer{
		Layout:  layout,
		Mode:    RenderBraille,
		Palette: testPalette(),
		Lens:    LensLanguage,
	}
	colored := r.Render(40, 15)

	rPlain := &GraphRenderer{
		Layout: layout,
		Mode:   RenderBraille,
	}
	plain := rPlain.Render(40, 15)

	if colored == plain {
		t.Error("colored render should differ from plain render (should contain ANSI escapes)")
	}
	if len(colored) <= len(plain) {
		t.Error("colored render should be longer than plain (ANSI escape overhead)")
	}
}

func TestRenderColored_LensSwitchInvalidatesCache(t *testing.T) {
	layout := rendererTestLayout()
	langs := []string{"go", "python", "ruby", "typescript", "rust"}
	kinds := []string{"function", "type", "method", "constant", "interface"}
	for i := range layout.Nodes {
		layout.Nodes[i].Language = langs[i%len(langs)]
		layout.Nodes[i].Kind = kinds[i%len(kinds)]
	}
	r := &GraphRenderer{
		Layout:  layout,
		Mode:    RenderBraille,
		Palette: testPalette(),
		Lens:    LensLanguage,
	}
	_ = r.Render(40, 15)
	langMap := make(map[int64]uint8)
	for k, v := range r.colorMap {
		langMap[k] = v
	}

	r.Lens = LensKind
	_ = r.Render(40, 15)

	differ := false
	for k, v := range r.colorMap {
		if langMap[k] != v {
			differ = true
			break
		}
	}
	if !differ {
		t.Error("switching lens should produce different color assignments")
	}
}
