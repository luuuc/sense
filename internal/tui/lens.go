package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Lens determines how nodes are colored.
type Lens int

const (
	LensLanguage Lens = iota
	LensKind
	LensCluster
	lensCount
)

func (l Lens) String() string {
	switch l {
	case LensLanguage:
		return "language"
	case LensKind:
		return "kind"
	case LensCluster:
		return "cluster"
	}
	return "?"
}

// NextLens cycles to the next lens.
func (l Lens) Next() Lens {
	return (l + 1) % lensCount
}

// ColorPalette holds the terminal-adapted color set.
type ColorPalette struct {
	Dark   bool
	Colors []lipgloss.Color
}

// DetectPalette returns a palette adapted to the terminal background.
func DetectPalette() ColorPalette {
	dark := isDarkTerminal()
	if dark {
		return ColorPalette{
			Dark: true,
			Colors: []lipgloss.Color{
				lipgloss.Color("#61AFEF"), // blue
				lipgloss.Color("#98C379"), // green
				lipgloss.Color("#E5C07B"), // yellow
				lipgloss.Color("#C678DD"), // purple
				lipgloss.Color("#E06C75"), // red
				lipgloss.Color("#56B6C2"), // cyan
				lipgloss.Color("#D19A66"), // orange
				lipgloss.Color("#ABB2BF"), // gray
			},
		}
	}
	return ColorPalette{
		Dark: false,
		Colors: []lipgloss.Color{
			lipgloss.Color("#0366D6"), // blue
			lipgloss.Color("#22863A"), // green
			lipgloss.Color("#B08800"), // yellow
			lipgloss.Color("#6F42C1"), // purple
			lipgloss.Color("#D73A49"), // red
			lipgloss.Color("#1B7C83"), // cyan
			lipgloss.Color("#E36209"), // orange
			lipgloss.Color("#586069"), // gray
		},
	}
}

func isDarkTerminal() bool {
	ct := os.Getenv("COLORFGBG")
	if ct != "" {
		parts := strings.Split(ct, ";")
		if len(parts) >= 2 {
			bg := parts[len(parts)-1]
			if bg == "0" || bg == "1" || bg == "2" || bg == "3" || bg == "4" || bg == "5" || bg == "6" || bg == "7" {
				return true
			}
			return false
		}
	}
	// Default to dark — most developer terminals are dark.
	return true
}

// NodeColor returns the color index for a node under the given lens.
func NodeColor(n LayoutNode, lens Lens, palette ColorPalette) lipgloss.Color {
	switch lens {
	case LensLanguage:
		return LanguageColor(n.Language, palette)
	case LensKind:
		idx := hashString(n.Kind) % len(palette.Colors)
		return palette.Colors[idx]
	case LensCluster:
		idx := hashString(clusterKey(n)) % len(palette.Colors)
		return palette.Colors[idx]
	}
	return palette.Colors[0]
}

func clusterKey(n LayoutNode) string {
	parts := strings.Split(n.Qualified, ".")
	if len(parts) >= 2 {
		return parts[0]
	}
	return n.Language
}

func hashString(s string) int {
	h := 0
	for _, c := range s {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	return h
}

// languageColors maps common languages to specific palette indices for
// recognizable, stable coloring.
var languageColors = map[string]int{
	"go":         0, // blue
	"python":     1, // green
	"ruby":       4, // red
	"typescript": 2, // yellow
	"javascript": 2, // yellow
	"rust":       6, // orange
	"java":       4, // red
	"css":        5, // cyan
	"html":       6, // orange
}

// LanguageColor returns a stable color for known languages, falling back
// to hash-based assignment for unknown ones.
func LanguageColor(lang string, palette ColorPalette) lipgloss.Color {
	if idx, ok := languageColors[strings.ToLower(lang)]; ok && idx < len(palette.Colors) {
		return palette.Colors[idx]
	}
	idx := hashString(lang) % len(palette.Colors)
	return palette.Colors[idx]
}
