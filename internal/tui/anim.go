package tui

import (
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	animDuration = 2 * time.Second
	animFPS      = 30
	animInterval = time.Second / animFPS
)

type animTickMsg time.Time

func animTick() tea.Cmd {
	return tea.Tick(animInterval, func(t time.Time) tea.Msg {
		return animTickMsg(t)
	})
}

// animation drives the node reveal sequence.
type animation struct {
	active    bool
	startTime time.Time
	total     int // total nodes to reveal
}

func newAnimation(totalNodes int) animation {
	return animation{
		active: totalNodes > 0,
		total:  totalNodes,
	}
}

// start returns the tea.Cmd to begin animation ticking.
func (a *animation) start() tea.Cmd {
	if !a.active {
		return nil
	}
	a.startTime = time.Now()
	return animTick()
}

// update processes a tick and returns the number of nodes that should be
// visible and whether the animation is still running.
func (a *animation) update() (visibleCount int, cmd tea.Cmd) {
	if !a.active {
		return a.total, nil
	}

	elapsed := time.Since(a.startTime)
	if elapsed >= animDuration {
		a.active = false
		return a.total, nil
	}

	t := float64(elapsed) / float64(animDuration)
	eased := easeOutCubic(t)
	count := int(math.Ceil(eased * float64(a.total)))
	if count < 1 {
		count = 1
	}
	if count > a.total {
		count = a.total
	}

	return count, animTick()
}

// easeOutCubic produces a decelerating curve — fast reveal at the start
// (hub nodes appear quickly), slow tail (leaves trickle in).
func easeOutCubic(t float64) float64 {
	t = t - 1
	return t*t*t + 1
}
