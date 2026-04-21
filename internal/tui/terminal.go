package tui

import "os"

// RenderMode controls the character set used for graph rendering.
type RenderMode int

const (
	RenderBraille RenderMode = iota
	RenderBlock
)

// DetectRenderMode checks terminal capabilities and returns the best
// render mode. Braille characters need Unicode support; most modern
// terminals handle them, but we fall back to block characters when
// SENSE_RENDER=block is set or the terminal is known to have issues.
func DetectRenderMode() RenderMode {
	if v := os.Getenv("SENSE_RENDER"); v == "block" {
		return RenderBlock
	}
	return RenderBraille
}
