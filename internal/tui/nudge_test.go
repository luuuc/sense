package tui

import (
	"testing"
	"time"
)

func nudgeFullText(n *activeNudge) string {
	if n.command != "" {
		return n.text + " " + n.command
	}
	return n.text
}

func TestNudgeTriggers(t *testing.T) {
	tests := []struct {
		name      string
		data      StatusData
		preShown  []nudgeID
		wantID    nudgeID
		wantFire  bool
		wantMsg   string
	}{
		{
			name:     "no index triggers nudge",
			data:     StatusData{IndexAgeSeconds: -1},
			wantID:   nudgeNoIndex,
			wantFire: true,
			wantMsg:  "No index found",
		},
		{
			name:     "no index has command",
			data:     StatusData{IndexAgeSeconds: -1},
			wantID:   nudgeNoIndex,
			wantFire: true,
			wantMsg:  "sense scan",
		},
		{
			name:     "stale index with changed files",
			data:     StatusData{IndexAgeSeconds: 7200, FilesChanged: 3, Symbols: 100},
			wantID:   nudgeStaleIndex,
			wantFire: true,
			wantMsg:  "120min stale",
		},
		{
			name:     "stale index without changed files does not fire",
			data:     StatusData{IndexAgeSeconds: 7200, FilesChanged: 0, Symbols: 100},
			wantFire: false,
		},
		{
			name:     "fresh index with changed files does not fire",
			data:     StatusData{IndexAgeSeconds: 600, FilesChanged: 3, Symbols: 100},
			wantFire: false,
		},
		{
			name:     "first MCP query",
			data:     StatusData{Queries: 1, Symbols: 100},
			wantID:   nudgeFirstQuery,
			wantFire: true,
			wantMsg:  "MCP connected",
		},
		{
			name:     "zero queries does not fire",
			data:     StatusData{Queries: 0, Symbols: 100},
			wantFire: false,
		},
		{
			name:     "fifth query triggers tip",
			data:     StatusData{Queries: 5, Symbols: 100},
			preShown: []nudgeID{nudgeFirstQuery},
			wantID:   nudgeFifthQuery,
			wantFire: true,
			wantMsg:  "sense scan --watch",
		},
		{
			name:     "four queries does not fire fifth query nudge",
			data:     StatusData{Queries: 4, Symbols: 100},
			preShown: []nudgeID{nudgeFirstQuery},
			wantFire: false,
		},
		{
			name:     "10k token milestone",
			data:     StatusData{TokensSaved: 12000, Symbols: 100},
			wantID:   nudgeMilestone10k,
			wantFire: true,
			wantMsg:  "~12k tokens saved",
		},
		{
			name:     "50k token milestone",
			data:     StatusData{TokensSaved: 55000, Symbols: 100},
			preShown: []nudgeID{nudgeMilestone10k},
			wantID:   nudgeMilestone50k,
			wantFire: true,
			wantMsg:  "$0.17",
		},
		{
			name:     "100k token milestone",
			data:     StatusData{TokensSaved: 105000, Symbols: 100},
			preShown: []nudgeID{nudgeMilestone10k, nudgeMilestone50k},
			wantID:   nudgeMilestone100k,
			wantFire: true,
			wantMsg:  "$0.32",
		},
		{
			name:     "below 10k threshold does not fire",
			data:     StatusData{TokensSaved: 8000, Symbols: 100},
			wantFire: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns := newNudgeState()
			for _, id := range tt.preShown {
				ns.shown[id] = true
			}
			now := time.Now()

			ns.evaluate(tt.data, now)

			if tt.wantFire {
				if ns.active == nil {
					t.Fatal("expected nudge to fire, got none")
				}
				if !ns.shown[tt.wantID] {
					t.Errorf("expected nudge ID %d to be marked shown", tt.wantID)
				}
				full := nudgeFullText(ns.active)
				if !containsText(full, tt.wantMsg) {
					t.Errorf("expected message containing %q, got %q", tt.wantMsg, full)
				}
			} else {
				if ns.active != nil {
					t.Errorf("expected no nudge, got %q", nudgeFullText(ns.active))
				}
			}
		})
	}
}

func TestNudgeState_OncePerSession(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now)
	if ns.active == nil {
		t.Fatal("first evaluation should fire nudge")
	}

	ns.dismiss()
	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now.Add(2*time.Second))
	if ns.active != nil {
		t.Error("same nudge should not fire twice per session")
	}
}

func TestNudgeState_AutoDismiss(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now)
	if ns.active == nil {
		t.Fatal("expected active nudge")
	}

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now.Add(nudgeDismissAfter+time.Second))
	if ns.active != nil {
		t.Error("nudge should auto-dismiss after timeout")
	}
}

func TestNudgeState_KeyDismiss(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now)
	if ns.active == nil {
		t.Fatal("expected active nudge")
	}

	ns.dismiss()
	if ns.active != nil {
		t.Error("dismiss should clear active nudge")
	}
}

func TestNudgeState_OneAtATime(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1, Queries: 1}, now)
	if ns.active == nil {
		t.Fatal("expected first nudge")
	}
	firstText := ns.active.text

	ns.evaluate(StatusData{IndexAgeSeconds: -1, Queries: 5}, now.Add(time.Second))
	if ns.active.text != firstText {
		t.Error("should not replace active nudge with a new one")
	}
}

func TestNudgeState_QueueAfterDismiss(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1, Queries: 1}, now)
	if ns.active == nil || !containsText(ns.active.text, "No index") {
		t.Fatal("expected no-index nudge first")
	}

	ns.dismiss()

	ns.evaluate(StatusData{IndexAgeSeconds: -1, Queries: 1}, now.Add(time.Second))
	if ns.active == nil {
		t.Fatal("expected queued nudge after dismiss")
	}
	if !containsText(ns.active.text, "MCP connected") {
		t.Errorf("expected first-query nudge, got %q", ns.active.text)
	}
}

func TestNudgeState_RenderAccentCommand(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()
	dim := testDimStyle()
	accent := testDimStyle()

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now)
	got := ns.render(120, dim, accent)
	if !containsText(got, "No index found") {
		t.Errorf("expected text portion, got %q", got)
	}
	if !containsText(got, "sense scan") {
		t.Errorf("expected command portion, got %q", got)
	}
}

func TestNudgeState_RenderNoCommand(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()
	dim := testDimStyle()
	accent := testDimStyle()

	ns.evaluate(StatusData{Queries: 1, Symbols: 100}, now)
	got := ns.render(120, dim, accent)
	if !containsText(got, "MCP connected") {
		t.Errorf("expected text, got %q", got)
	}
}

func TestNudgeState_RenderEmpty(t *testing.T) {
	ns := newNudgeState()
	dim := testDimStyle()
	accent := testDimStyle()

	if got := ns.render(80, dim, accent); got != "" {
		t.Errorf("no active nudge should render empty, got %q", got)
	}
}

func TestNudgeState_RenderTruncates(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()
	dim := testDimStyle()
	accent := testDimStyle()

	ns.evaluate(StatusData{IndexAgeSeconds: -1}, now)
	got := ns.render(10, dim, accent)
	clean := stripANSI(got)
	runes := []rune(clean)
	if len(runes) > 10 {
		t.Errorf("rendered nudge should truncate to width, got %d runes: %q", len(runes), clean)
	}
}

func TestNudgeTriggers_PriorityOrder(t *testing.T) {
	ns := newNudgeState()
	now := time.Now()

	ns.evaluate(StatusData{IndexAgeSeconds: -1, Queries: 5, TokensSaved: 50000}, now)
	if ns.active == nil {
		t.Fatal("expected a nudge")
	}
	if !containsText(ns.active.text, "No index") {
		t.Errorf("no-index should have highest priority, got %q", ns.active.text)
	}
}

func TestNudgeInView(t *testing.T) {
	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil)
	m.width = 120
	m.height = 24
	m.status = StatusData{IndexAgeSeconds: -1, Symbols: 10, Edges: 5}
	m.nudge.evaluate(m.status, time.Now())

	v := m.View()
	if !containsText(v, "No index found") {
		t.Error("View() should include active nudge text")
	}
	if !containsText(v, "sense scan") {
		t.Error("View() should include nudge command")
	}
}
