package tui

import "strings"

// Braille Unicode block: U+2800 to U+28FF.
// Each cell is a 2×4 dot grid. Bit positions:
//
//	0  3
//	1  4
//	2  5
//	6  7
const brailleBase = 0x2800

// brailleDotBit maps (dx, dy) within a braille cell to the Unicode bit offset.
// dx ∈ {0,1}, dy ∈ {0,1,2,3}.
var brailleDotBit = [2][4]uint{
	{0, 1, 2, 6}, // left column
	{3, 4, 5, 7}, // right column
}

// Canvas is a braille-character drawing surface. Each terminal cell maps to a
// 2×4 dot grid, giving effectively 2× horizontal and 4× vertical resolution.
// Coordinates are in dot space (not cell space).
type Canvas struct {
	// Width and Height in dot coordinates.
	Width  int
	Height int
	// dots stores the braille bit pattern for each terminal cell.
	// Indexed as dots[cellRow][cellCol].
	dots [][]uint8
	// cellColors stores a color index per terminal cell (0 = default).
	cellColors [][]uint8
}

// NewCanvas creates a canvas sized for the given terminal cell dimensions.
// The dot-space resolution is (cols*2, rows*4).
func NewCanvas(cols, rows int) *Canvas {
	if cols <= 0 || rows <= 0 {
		return &Canvas{}
	}
	dots := make([][]uint8, rows)
	colors := make([][]uint8, rows)
	for i := range dots {
		dots[i] = make([]uint8, cols)
		colors[i] = make([]uint8, cols)
	}
	return &Canvas{
		Width:      cols * 2,
		Height:     rows * 4,
		dots:       dots,
		cellColors: colors,
	}
}

// Set turns on the dot at (x, y) in dot coordinates.
// Out-of-bounds coordinates are silently ignored.
func (c *Canvas) Set(x, y int) {
	if x < 0 || y < 0 || x >= c.Width || y >= c.Height {
		return
	}
	col := x / 2
	row := y / 4
	dx := x % 2
	dy := y % 4
	c.dots[row][col] |= 1 << brailleDotBit[dx][dy]
}

// Render returns the canvas as a string of braille characters, one line per
// terminal row. Trailing blank braille cells (U+2800) are trimmed from each
// line. Trailing empty lines are trimmed from the output.
func (c *Canvas) Render() string {
	if len(c.dots) == 0 {
		return ""
	}
	var b strings.Builder
	lines := make([]string, len(c.dots))
	lastNonEmpty := -1
	for row, cells := range c.dots {
		var line strings.Builder
		trailingBlanks := 0
		for _, bits := range cells {
			r := rune(brailleBase + int(bits))
			if bits == 0 {
				trailingBlanks++
			} else {
				trailingBlanks = 0
			}
			line.WriteRune(r)
		}
		s := []rune(line.String())
		if trailingBlanks < len(s) {
			s = s[:len(s)-trailingBlanks]
			lastNonEmpty = row
		}
		lines[row] = string(s)
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

// DrawLine draws a line between two points in dot coordinates using
// Bresenham's algorithm. Both endpoints are inclusive.
func (c *Canvas) DrawLine(x0, y0, x1, y1 int) {
	dx := abs(x1 - x0)
	dy := abs(y1 - y0)
	sx := 1
	if x0 > x1 {
		sx = -1
	}
	sy := 1
	if y0 > y1 {
		sy = -1
	}
	err := dx - dy
	for {
		c.Set(x0, y0)
		if x0 == x1 && y0 == y1 {
			break
		}
		e2 := 2 * err
		if e2 > -dy {
			err -= dy
			x0 += sx
		}
		if e2 < dx {
			err += dx
			y0 += sy
		}
	}
}

// DrawCircle draws a filled circle at center (cx, cy) with the given radius
// in dot coordinates.
func (c *Canvas) DrawCircle(cx, cy, radius int) {
	c.DrawCircleColored(cx, cy, radius, 0)
}

// DrawCircleColored draws a filled circle and sets the color index for each cell.
func (c *Canvas) DrawCircleColored(cx, cy, radius int, colorIdx uint8) {
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			if dx*dx+dy*dy <= radius*radius {
				x, y := cx+dx, cy+dy
				c.Set(x, y)
				if colorIdx > 0 {
					c.SetColor(x, y, colorIdx)
				}
			}
		}
	}
}

// SetColor marks the terminal cell containing dot (x, y) with a color index.
// Index 0 means default (no color). Higher values map to palette entries.
func (c *Canvas) SetColor(x, y int, colorIdx uint8) {
	if x < 0 || y < 0 || x >= c.Width || y >= c.Height {
		return
	}
	col := x / 2
	row := y / 4
	c.cellColors[row][col] = colorIdx
}

// CellColor returns the color index for the terminal cell at (cellCol, cellRow).
func (c *Canvas) CellColor(cellCol, cellRow int) uint8 {
	if len(c.cellColors) == 0 || cellRow < 0 || cellCol < 0 || cellRow >= len(c.cellColors) || cellCol >= len(c.cellColors[cellRow]) {
		return 0
	}
	return c.cellColors[cellRow][cellCol]
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
