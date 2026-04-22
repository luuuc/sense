package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

type promptID string

const (
	promptBrain     promptID = "brain"
	promptClaude    promptID = "claude"
	promptPreCommit promptID = "pre-commit"
	promptCI        promptID = "ci"
	promptLargeRepo promptID = "large-repo"
)

type ecosystemPrompt struct {
	id        promptID
	condition func(ctx promptContext) bool
	text      string
	command   string
}

type promptContext struct {
	ProjectRoot string
	Symbols     int
}

var (
	brainInPath     bool
	brainInPathOnce sync.Once
)

func hasBrainInPath() bool {
	brainInPathOnce.Do(func() {
		if p, _ := lookPath("brain"); p != "" {
			brainInPath = true
		}
	})
	return brainInPath
}

// lookPath is a variable so tests can override it.
var lookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

var ecosystemPrompts = []ecosystemPrompt{
	{
		id:        promptBrain,
		condition: func(_ promptContext) bool { return hasBrainInPath() },
		text:      "brAIn detected",
		command:   "sense export --facts pipes symbols into your Facts layer",
	},
	{
		id: promptClaude,
		condition: func(ctx promptContext) bool {
			_, err := os.Stat(filepath.Join(ctx.ProjectRoot, ".claude"))
			return err == nil
		},
		text:    "Claude Code detected",
		command: "add sense to .mcp.json for structural queries",
	},
	{
		id: promptPreCommit,
		condition: func(ctx promptContext) bool {
			_, err := os.Stat(filepath.Join(ctx.ProjectRoot, ".pre-commit-config.yaml"))
			return err == nil
		},
		text:    "pre-commit detected",
		command: "sense blast --diff in hooks catches high-risk changes",
	},
	{
		id: promptCI,
		condition: func(ctx promptContext) bool {
			paths := []string{
				filepath.Join(ctx.ProjectRoot, ".github", "workflows"),
				filepath.Join(ctx.ProjectRoot, ".gitlab-ci.yml"),
			}
			for _, p := range paths {
				if _, err := os.Stat(p); err == nil {
					return true
				}
			}
			return false
		},
		text:    "CI detected",
		command: "sense check in CI validates no unresolved cross-module refs",
	},
	{
		id:        promptLargeRepo,
		condition: func(ctx promptContext) bool { return ctx.Symbols > 5000 },
		text:      "Large codebase?",
		command:   "sense scan --watch avoids waiting for full re-scans",
	},
}

type seenPromptsFile struct {
	MajorVersion int      `json:"major_version"`
	Seen         []string `json:"seen"`
}

type promptState struct {
	pending  []ecosystemPrompt
	active   *ecosystemPrompt
	seenPath string
	seen     map[promptID]bool
}

func newPromptState(senseDir string, majorVersion int, ctx promptContext) promptState {
	ps := promptState{
		seenPath: filepath.Join(senseDir, "seen_prompts"),
		seen:     make(map[promptID]bool),
	}

	ps.loadSeen(majorVersion)
	for _, ep := range ecosystemPrompts {
		if ps.seen[ep.id] {
			continue
		}
		if ep.condition(ctx) {
			p := ep
			ps.pending = append(ps.pending, p)
		}
	}
	if len(ps.pending) > 0 {
		ps.active = &ps.pending[0]
		ps.pending = ps.pending[1:]
	}
	return ps
}

func (ps *promptState) loadSeen(majorVersion int) {
	data, err := os.ReadFile(ps.seenPath)
	if err != nil {
		return
	}
	var f seenPromptsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	if f.MajorVersion != majorVersion {
		return
	}
	for _, id := range f.Seen {
		ps.seen[promptID(id)] = true
	}
}

func (ps *promptState) dismiss(majorVersion int) {
	if ps.active == nil {
		return
	}
	ps.seen[ps.active.id] = true
	ps.saveSeen(majorVersion)

	if len(ps.pending) > 0 {
		ps.active = &ps.pending[0]
		ps.pending = ps.pending[1:]
	} else {
		ps.active = nil
	}
}

func (ps *promptState) saveSeen(majorVersion int) {
	var ids []string
	for id := range ps.seen {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	f := seenPromptsFile{
		MajorVersion: majorVersion,
		Seen:         ids,
	}
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	_ = os.WriteFile(ps.seenPath, data, 0644)
}

func (ps *promptState) render(width int, dim, accent lipgloss.Style) string {
	if ps.active == nil {
		return ""
	}

	dismissHint := dim.Render("[x]")
	body := dim.Render(ps.active.text+" ") + accent.Render(ps.active.command)
	result := body + "  " + dismissHint

	if width > 0 && lipgloss.Width(result) > width {
		plain := ps.active.text + " " + ps.active.command + "  [x]"
		runes := []rune(plain)
		if len(runes) > width {
			runes = append(runes[:width-1], '…')
		}
		return dim.Render(string(runes))
	}
	return result
}

func (ps *promptState) hasActive() bool {
	return ps.active != nil
}

func parseMajorVersion(v string) int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 2)
	if len(parts) == 0 {
		return 0
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return n
}
