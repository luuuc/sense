package tui

import (
	"fmt"
	"math"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	pulseChar     = "◉"
	pulseInterval = 500 * time.Millisecond
	breathePeriod = 3 * time.Second
	eventFlash    = 200 * time.Millisecond
	eventDecay    = 300 * time.Millisecond
)

// Color endpoints: muted (low energy) and vivid (high energy).
// In dark mode vivid is brighter; in light mode vivid is more saturated/darker.
var (
	darkMuted  = [3]float64{0x30, 0x50, 0x55}
	darkVivid  = [3]float64{0x56, 0xB6, 0xC2}
	lightMuted = [3]float64{0x80, 0xA0, 0xA5}
	lightVivid = [3]float64{0x1B, 0x7C, 0x83}
)

type pulseTickMsg time.Time

func pulseTick() tea.Cmd {
	return tea.Tick(pulseInterval, func(t time.Time) tea.Msg {
		return pulseTickMsg(t)
	})
}

type pulseState struct {
	startTime  time.Time
	eventTime  time.Time
	dark       bool
	cachedHex  string
	cachedStyle lipgloss.Style
}

func newPulseState(dark bool) pulseState {
	return pulseState{
		startTime: time.Now(),
		dark:      dark,
	}
}

func (p *pulseState) event() {
	p.eventTime = time.Now()
}

func (p *pulseState) render(now time.Time) string {
	brightness := p.breatheBrightness(now)

	if !p.eventTime.IsZero() {
		elapsed := now.Sub(p.eventTime)
		if elapsed < eventFlash {
			brightness = 1.0
		} else if elapsed < eventFlash+eventDecay {
			t := float64(elapsed-eventFlash) / float64(eventDecay)
			brightness = 1.0 + t*(brightness-1.0)
		}
	}

	hex := interpolateColor(brightness, p.dark)
	if hex != p.cachedHex {
		p.cachedHex = hex
		p.cachedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(hex))
	}
	return p.cachedStyle.Render(pulseChar)
}

func (p pulseState) breatheBrightness(now time.Time) float64 {
	elapsed := now.Sub(p.startTime)
	phase := float64(elapsed) / float64(breathePeriod) * 2 * math.Pi
	return 0.3 + 0.7*(0.5+0.5*math.Sin(phase))
}

func interpolateColor(brightness float64, dark bool) string {
	if brightness < 0 {
		brightness = 0
	}
	if brightness > 1 {
		brightness = 1
	}

	muted, vivid := lightMuted, lightVivid
	if dark {
		muted, vivid = darkMuted, darkVivid
	}

	r := int(muted[0] + brightness*(vivid[0]-muted[0]))
	g := int(muted[1] + brightness*(vivid[1]-muted[1]))
	b := int(muted[2] + brightness*(vivid[2]-muted[2]))
	return fmt.Sprintf("#%02X%02X%02X", r, g, b)
}
