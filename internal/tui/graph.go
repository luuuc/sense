package tui

import (
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ZoomLevel controls how much detail is visible.
type ZoomLevel int

const (
	ZoomFit    ZoomLevel = 0 // fit entire graph in viewport
	ZoomMedium ZoomLevel = 1 // 2× zoom
	ZoomClose  ZoomLevel = 2 // 4× zoom
	ZoomDetail ZoomLevel = 3 // 8× zoom
)

func (z ZoomLevel) scale() float64 {
	return math.Pow(2, float64(z))
}

// Viewport tracks the visible region of the graph.
type Viewport struct {
	OffsetX float64 // pan offset in normalized [0,1] space
	OffsetY float64
	Zoom    ZoomLevel
}

// GraphRenderer renders a Layout onto a Canvas with viewport controls.
type GraphRenderer struct {
	Layout   *Layout
	Viewport Viewport
	Mode     RenderMode
	Lens     Lens
	Palette  ColorPalette
	// VisibleCount controls how many nodes are visible (for animation).
	// 0 or >= len(Layout.Nodes) means all visible.
	VisibleCount int
	// SelectedID highlights a specific node (0 = none).
	SelectedID int64
	// NodeColorOverride maps node IDs to special color indices (blast hop colors, faded).
	// Nil = no overrides. Overrides take priority over lens-based coloring.
	NodeColorOverride map[int64]uint8

	// Cached state, rebuilt on each Render call.
	nodeIndex         map[int64]int // ID → index into Layout.Nodes
	colorMap          map[int64]uint8
	colorMapLens      Lens
	styles            []lipgloss.Style
	specialColorIndex map[uint8]int // color index → style array position
}

// Render draws the graph onto a canvas sized for the given terminal dimensions.
// Returns the rendered string.
func (g *GraphRenderer) Render(cols, rows int) string {
	if g.Layout == nil || len(g.Layout.Nodes) == 0 || cols <= 0 || rows <= 0 {
		return ""
	}

	g.buildNodeIndex()

	c := NewCanvas(cols, rows)
	scale := g.Viewport.Zoom.scale()

	visible := g.visibleNodes()
	visibleSet := make(map[int64]bool, len(visible))
	for _, n := range visible {
		visibleSet[n.ID] = true
	}

	for _, e := range g.Layout.Edges {
		if !visibleSet[e.SourceID] || !visibleSet[e.TargetID] {
			continue
		}
		src := g.nodeByID(e.SourceID)
		tgt := g.nodeByID(e.TargetID)
		if src == nil || tgt == nil {
			continue
		}
		x0, y0 := g.toCanvas(src.X, src.Y, c.Width, c.Height, scale)
		x1, y1 := g.toCanvas(tgt.X, tgt.Y, c.Width, c.Height, scale)
		c.DrawLine(x0, y0, x1, y1)
	}

	colorMap := g.cachedColorMap(visible)
	for _, n := range visible {
		x, y := g.toCanvas(n.X, n.Y, c.Width, c.Height, scale)
		radius := nodeRadius(n.Centrality)
		if n.ID == g.SelectedID {
			radius += 2
			c.DrawCircleColored(x, y, radius, colorSelectedIdx)
		} else if override, ok := g.NodeColorOverride[n.ID]; ok {
			c.DrawCircleColored(x, y, radius, override)
		} else {
			c.DrawCircleColored(x, y, radius, colorMap[n.ID])
		}
	}

	if g.Viewport.Zoom >= ZoomMedium {
		g.drawLabels(c, visible, scale)
	}

	if g.Mode == RenderBlock {
		return RenderBlockFallback(c)
	}
	if len(g.Palette.Colors) > 0 {
		return g.renderColored(c)
	}
	return c.Render()
}

func (g *GraphRenderer) cachedColorMap(nodes []LayoutNode) map[int64]uint8 {
	if g.colorMap != nil && g.colorMapLens == g.Lens && len(g.colorMap) >= len(nodes) {
		return g.colorMap
	}
	m := make(map[int64]uint8, len(nodes))
	if len(g.Palette.Colors) == 0 {
		g.colorMap = m
		g.colorMapLens = g.Lens
		return m
	}
	colorIndex := make(map[lipgloss.Color]uint8, len(g.Palette.Colors))
	for i, c := range g.Palette.Colors {
		colorIndex[c] = uint8(i + 1)
	}
	for _, n := range nodes {
		color := NodeColor(n, g.Lens, g.Palette)
		m[n.ID] = colorIndex[color]
	}
	g.colorMap = m
	g.colorMapLens = g.Lens
	return m
}

const (
	colorSelectedIdx uint8 = 254
	colorSentinel    uint8 = 255 // never-used value for run-length encoding init
)

func (g *GraphRenderer) renderColored(c *Canvas) string {
	if len(c.dots) == 0 {
		return ""
	}
	g.ensureStyles()

	var b strings.Builder
	lastNonEmpty := -1
	lines := make([]string, len(c.dots))
	for row, cells := range c.dots {
		var line strings.Builder
		runColor := colorSentinel
		var runBuf strings.Builder
		rowHasContent := false
		for col, bits := range cells {
			if bits != 0 {
				rowHasContent = true
			}
			cidx := c.CellColor(col, row)
			if cidx != runColor {
				if runBuf.Len() > 0 {
					line.WriteString(g.styles[g.styleIndex(runColor)].Render(runBuf.String()))
					runBuf.Reset()
				}
				runColor = cidx
			}
			runBuf.WriteRune(rune(brailleBase + int(bits)))
		}
		if runBuf.Len() > 0 {
			si := g.styleIndex(runColor)
			line.WriteString(g.styles[si].Render(runBuf.String()))
		}
		if rowHasContent {
			lastNonEmpty = row
		}
		lines[row] = line.String()
	}
	if lastNonEmpty < 0 {
		return ""
	}
	for i := 0; i <= lastNonEmpty; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(lines[i])
	}
	return b.String()
}

// ensureStyles builds the style lookup table: palette colors [1..N],
// then special indices for blast/faded/selection mapped to the end.
func (g *GraphRenderer) ensureStyles() {
	paletteLen := len(g.Palette.Colors)
	expectedLen := paletteLen + 1 + len(specialColors)
	if len(g.styles) == expectedLen {
		return
	}
	g.styles = make([]lipgloss.Style, expectedLen)
	g.styles[0] = lipgloss.NewStyle()
	for i, color := range g.Palette.Colors {
		g.styles[i+1] = lipgloss.NewStyle().Foreground(color)
	}
	dark := g.Palette.Dark
	base := paletteLen + 1
	g.specialColorIndex = make(map[uint8]int, len(specialColors))
	for i, sc := range specialColors {
		color := sc.dark
		if !dark {
			color = sc.light
		}
		g.styles[base+i] = lipgloss.NewStyle().Foreground(color)
		g.specialColorIndex[sc.idx] = base + i
	}
}

type specialColor struct {
	idx   uint8
	dark  lipgloss.Color
	light lipgloss.Color
}

// specialColors maps special color indices to their styles.
// Order matters: the offset from palette end determines the style slot.
var specialColors = []specialColor{
	{colorFaded, "#3B4048", "#C8CCD4"},
	{colorBlastSubject, "#FFFFFF", "#000000"},
	{colorBlastHop1, "#E5C07B", "#B08800"},
	{colorBlastHop2, "#D19A66", "#E36209"},
	{colorBlastHop3, "#E06C75", "#D73A49"},
	{colorSelectedIdx, "#FFFFFF", "#000000"},
}

func (g *GraphRenderer) styleIndex(colorIdx uint8) int {
	if si, ok := g.specialColorIndex[colorIdx]; ok {
		return si
	}
	si := int(colorIdx)
	if si >= len(g.styles) {
		si = 0
	}
	return si
}

func (g *GraphRenderer) buildNodeIndex() {
	if len(g.nodeIndex) == len(g.Layout.Nodes) {
		return
	}
	g.nodeIndex = make(map[int64]int, len(g.Layout.Nodes))
	for i, n := range g.Layout.Nodes {
		g.nodeIndex[n.ID] = i
	}
}

func (g *GraphRenderer) nodeByID(id int64) *LayoutNode {
	if idx, ok := g.nodeIndex[id]; ok {
		return &g.Layout.Nodes[idx]
	}
	return nil
}

func (g *GraphRenderer) visibleNodes() []LayoutNode {
	if g.VisibleCount <= 0 || g.VisibleCount >= len(g.Layout.Nodes) {
		return g.Layout.Nodes
	}
	sorted := make([]LayoutNode, len(g.Layout.Nodes))
	copy(sorted, g.Layout.Nodes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Centrality > sorted[j].Centrality
	})
	return sorted[:g.VisibleCount]
}

func (g *GraphRenderer) toCanvas(nx, ny float64, canvasW, canvasH int, scale float64) (int, int) {
	cx := 0.5 + (nx-0.5-g.Viewport.OffsetX)*scale
	cy := 0.5 + (ny-0.5-g.Viewport.OffsetY)*scale
	x := int(cx * float64(canvasW-1))
	y := int(cy * float64(canvasH-1))
	return x, y
}

// nodeRadius maps centrality to a dot-space radius.
// Range: 1 (leaf) to 5 (hub with 20+ inbound edges).
func nodeRadius(centrality int) int {
	if centrality <= 0 {
		return 1
	}
	r := 1 + int(math.Log2(float64(centrality+1)))
	if r > 5 {
		r = 5
	}
	return r
}

func (g *GraphRenderer) drawLabels(c *Canvas, nodes []LayoutNode, scale float64) {
	maxLabels := 30
	if g.Viewport.Zoom >= ZoomClose {
		maxLabels = 60
	}
	if g.Viewport.Zoom >= ZoomDetail {
		maxLabels = 120
	}

	sorted := make([]LayoutNode, len(nodes))
	copy(sorted, nodes)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Centrality > sorted[j].Centrality
	})

	count := len(sorted)
	if count > maxLabels {
		count = maxLabels
	}

	for i := 0; i < count; i++ {
		n := sorted[i]
		x, y := g.toCanvas(n.X, n.Y, c.Width, c.Height, scale)
		radius := nodeRadius(n.Centrality)
		labelX := x + radius + 2
		labelY := y - 2
		if labelY < 0 {
			labelY = 0
		}
		drawText(c, labelX, labelY, n.Name)
	}
}

// drawText renders ASCII text onto the braille canvas by setting dots to
// approximate each character. This is a simple 3×5 bitmap font.
func drawText(c *Canvas, startX, startY int, text string) {
	x := startX
	for _, ch := range text {
		bmp := charBitmap(ch)
		for dy := 0; dy < 5; dy++ {
			for dx := 0; dx < 3; dx++ {
				if bmp[dy]&(1<<(2-dx)) != 0 {
					c.Set(x+dx, startY+dy)
				}
			}
		}
		x += 4
	}
}

// charBitmap returns a 5-row, 3-column bitmap for common ASCII characters.
// Each row is a byte where bits 2,1,0 represent columns left-to-right.
func charBitmap(ch rune) [5]byte {
	if ch >= 'A' && ch <= 'Z' {
		return upperBitmaps[ch-'A']
	}
	if ch >= 'a' && ch <= 'z' {
		return upperBitmaps[ch-'a']
	}
	if ch >= '0' && ch <= '9' {
		return digitBitmaps[ch-'0']
	}
	return [5]byte{0, 0, 0, 0, 0}
}

var upperBitmaps = [26][5]byte{
	{0b010, 0b101, 0b111, 0b101, 0b101}, // A
	{0b110, 0b101, 0b110, 0b101, 0b110}, // B
	{0b011, 0b100, 0b100, 0b100, 0b011}, // C
	{0b110, 0b101, 0b101, 0b101, 0b110}, // D
	{0b111, 0b100, 0b110, 0b100, 0b111}, // E
	{0b111, 0b100, 0b110, 0b100, 0b100}, // F
	{0b011, 0b100, 0b101, 0b101, 0b011}, // G
	{0b101, 0b101, 0b111, 0b101, 0b101}, // H
	{0b111, 0b010, 0b010, 0b010, 0b111}, // I
	{0b001, 0b001, 0b001, 0b101, 0b010}, // J
	{0b101, 0b110, 0b100, 0b110, 0b101}, // K
	{0b100, 0b100, 0b100, 0b100, 0b111}, // L
	{0b101, 0b111, 0b111, 0b101, 0b101}, // M
	{0b101, 0b111, 0b111, 0b101, 0b101}, // N
	{0b010, 0b101, 0b101, 0b101, 0b010}, // O
	{0b110, 0b101, 0b110, 0b100, 0b100}, // P
	{0b010, 0b101, 0b101, 0b110, 0b011}, // Q
	{0b110, 0b101, 0b110, 0b101, 0b101}, // R
	{0b011, 0b100, 0b010, 0b001, 0b110}, // S
	{0b111, 0b010, 0b010, 0b010, 0b010}, // T
	{0b101, 0b101, 0b101, 0b101, 0b010}, // U
	{0b101, 0b101, 0b101, 0b010, 0b010}, // V
	{0b101, 0b101, 0b111, 0b111, 0b101}, // W
	{0b101, 0b101, 0b010, 0b101, 0b101}, // X
	{0b101, 0b101, 0b010, 0b010, 0b010}, // Y
	{0b111, 0b001, 0b010, 0b100, 0b111}, // Z
}

var digitBitmaps = [10][5]byte{
	{0b010, 0b101, 0b101, 0b101, 0b010}, // 0
	{0b010, 0b110, 0b010, 0b010, 0b111}, // 1
	{0b110, 0b001, 0b010, 0b100, 0b111}, // 2
	{0b110, 0b001, 0b010, 0b001, 0b110}, // 3
	{0b101, 0b101, 0b111, 0b001, 0b001}, // 4
	{0b111, 0b100, 0b110, 0b001, 0b110}, // 5
	{0b011, 0b100, 0b110, 0b101, 0b010}, // 6
	{0b111, 0b001, 0b010, 0b010, 0b010}, // 7
	{0b010, 0b101, 0b010, 0b101, 0b010}, // 8
	{0b010, 0b101, 0b011, 0b001, 0b110}, // 9
}

// Pan adjusts the viewport offset.
func (v *Viewport) Pan(dx, dy float64) {
	v.OffsetX += dx
	v.OffsetY += dy
}

// ZoomIn increases the zoom level.
func (v *Viewport) ZoomIn() {
	if v.Zoom < ZoomDetail {
		v.Zoom++
	}
}

// ZoomOut decreases the zoom level.
func (v *Viewport) ZoomOut() {
	if v.Zoom > ZoomFit {
		v.Zoom--
	}
}

// PanStep returns the appropriate pan distance for the current zoom level.
func (v *Viewport) PanStep() float64 {
	return 0.1 / v.Zoom.scale()
}

// String returns a short description of the zoom level.
func (z ZoomLevel) String() string {
	switch z {
	case ZoomFit:
		return "fit"
	case ZoomMedium:
		return "2×"
	case ZoomClose:
		return "4×"
	case ZoomDetail:
		return "8×"
	}
	return "?"
}

// RenderBlockFallback renders the canvas using block characters instead of
// braille. Each terminal cell represents a 2×2 pixel grid using half-block
// characters. Used when SENSE_RENDER=block is set.
func RenderBlockFallback(c *Canvas) string {
	if len(c.dots) == 0 {
		return ""
	}
	var b strings.Builder
	cellRows := c.Height / 2
	cellCols := c.Width / 2
	if cellRows <= 0 || cellCols <= 0 {
		return ""
	}

	for row := 0; row < cellRows; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		for col := 0; col < cellCols; col++ {
			topY := row * 2
			botY := topY + 1
			x := col * 2
			topSet := isDotSet(c, x, topY) || isDotSet(c, x+1, topY)
			botSet := isDotSet(c, x, botY) || isDotSet(c, x+1, botY)
			switch {
			case topSet && botSet:
				b.WriteRune('█')
			case topSet:
				b.WriteRune('▀')
			case botSet:
				b.WriteRune('▄')
			default:
				b.WriteByte(' ')
			}
		}
	}
	return b.String()
}

func isDotSet(c *Canvas, x, y int) bool {
	if x < 0 || y < 0 || x >= c.Width || y >= c.Height {
		return false
	}
	col := x / 2
	row := y / 4
	dx := x % 2
	dy := y % 4
	return c.dots[row][col]&(1<<brailleDotBit[dx][dy]) != 0
}
