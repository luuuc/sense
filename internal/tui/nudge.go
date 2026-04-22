package tui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	nudgeCheckInterval = 1 * time.Second
	nudgeDismissAfter  = 8 * time.Second
)

type nudgeID int

const (
	nudgeNoIndex nudgeID = iota
	nudgeStaleIndex
	nudgeFirstQuery
	nudgeFifthQuery
	nudgeMilestone10k
	nudgeMilestone50k
	nudgeMilestone100k
)

type nudgeTrigger struct {
	id        nudgeID
	condition func(StatusData) bool
	text      func(StatusData) string // descriptive portion
	command   func(StatusData) string // actionable command (rendered in accent style); empty = no command
}

var nudgeTriggers = []nudgeTrigger{
	{
		id:        nudgeNoIndex,
		condition: func(s StatusData) bool { return s.IndexAgeSeconds == -1 },
		text:      func(_ StatusData) string { return "No index found. Run:" },
		command:   func(_ StatusData) string { return "sense scan" },
	},
	{
		id: nudgeStaleIndex,
		condition: func(s StatusData) bool {
			return s.IndexAgeSeconds > 3600 && s.FilesChanged > 0
		},
		text: func(s StatusData) string {
			return fmt.Sprintf("Index is %dmin stale ·", s.IndexAgeSeconds/60)
		},
		command: func(_ StatusData) string { return "sense scan --watch" },
	},
	{
		id:        nudgeFirstQuery,
		condition: func(s StatusData) bool { return s.Queries >= 1 },
		text:      func(_ StatusData) string { return "MCP connected · serving queries from your AI tool" },
		command:   func(_ StatusData) string { return "" },
	},
	{
		id:        nudgeFifthQuery,
		condition: func(s StatusData) bool { return s.Queries >= 5 },
		text:      func(_ StatusData) string { return "Tip:" },
		command:   func(_ StatusData) string { return "sense scan --watch" },
	},
	{
		id:        nudgeMilestone10k,
		condition: func(s StatusData) bool { return s.TokensSaved >= 10_000 },
		text: func(s StatusData) string {
			return fmt.Sprintf("~%s tokens saved this session · that's ~$%.2f of Claude input", formatCompact(s.TokensSaved), tokenDollars(s.TokensSaved))
		},
		command: func(_ StatusData) string { return "" },
	},
	{
		id:        nudgeMilestone50k,
		condition: func(s StatusData) bool { return s.TokensSaved >= 50_000 },
		text: func(s StatusData) string {
			return fmt.Sprintf("~%s tokens saved this session · that's ~$%.2f of Claude input", formatCompact(s.TokensSaved), tokenDollars(s.TokensSaved))
		},
		command: func(_ StatusData) string { return "" },
	},
	{
		id:        nudgeMilestone100k,
		condition: func(s StatusData) bool { return s.TokensSaved >= 100_000 },
		text: func(s StatusData) string {
			return fmt.Sprintf("~%s tokens saved this session · that's ~$%.2f of Claude input", formatCompact(s.TokensSaved), tokenDollars(s.TokensSaved))
		},
		command: func(_ StatusData) string { return "" },
	},
}

type nudgeCheckMsg time.Time

func nudgeCheck() tea.Cmd {
	return tea.Tick(nudgeCheckInterval, func(t time.Time) tea.Msg {
		return nudgeCheckMsg(t)
	})
}

type nudgeState struct {
	active *activeNudge
	shown  map[nudgeID]bool
}

type activeNudge struct {
	text    string // descriptive text (dim style)
	command string // actionable command (accent style); may be empty
	shownAt time.Time
}

func newNudgeState() nudgeState {
	return nudgeState{
		shown: make(map[nudgeID]bool),
	}
}

func (n *nudgeState) evaluate(cur StatusData, now time.Time) {
	if n.active != nil {
		if now.Sub(n.active.shownAt) >= nudgeDismissAfter {
			n.active = nil
		} else {
			return
		}
	}

	for _, trigger := range nudgeTriggers {
		if n.shown[trigger.id] {
			continue
		}
		if trigger.condition(cur) {
			n.shown[trigger.id] = true
			n.active = &activeNudge{
				text:    trigger.text(cur),
				command: trigger.command(cur),
				shownAt: now,
			}
			break
		}
	}
}

func (n *nudgeState) dismiss() {
	n.active = nil
}

func (n *nudgeState) render(width int, dim, accent lipgloss.Style) string {
	if n.active == nil {
		return ""
	}

	var result string
	if n.active.command != "" {
		result = dim.Render(n.active.text) + " " + accent.Render(n.active.command)
	} else {
		result = dim.Render(n.active.text)
	}

	if width > 0 && lipgloss.Width(result) > width {
		plain := n.active.text
		if n.active.command != "" {
			plain += " " + n.active.command
		}
		runes := []rune(plain)
		if len(runes) > width {
			runes = append(runes[:width-1], '…')
		}
		return dim.Render(string(runes))
	}
	return result
}
