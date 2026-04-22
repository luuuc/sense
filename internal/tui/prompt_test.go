package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMajorVersion(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"0.0.0-dev", 0},
		{"1.2.3", 1},
		{"v2.0.0", 2},
		{"10.1.0", 10},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseMajorVersion(tt.input)
		if got != tt.want {
			t.Errorf("parseMajorVersion(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestPromptState_ConditionDetectsClaudeDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps.active == nil {
		t.Fatal("expected prompt to fire for .claude/ directory")
	}
	if ps.active.id != promptClaude {
		t.Errorf("expected claude prompt, got %s", ps.active.id)
	}
}

func TestPromptState_ConditionDetectsCI(t *testing.T) {
	dir := t.TempDir()
	ghDir := filepath.Join(dir, ".github", "workflows")
	if err := os.MkdirAll(ghDir, 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps.active == nil {
		t.Fatal("expected prompt to fire for CI config")
	}
	if ps.active.id != promptCI {
		t.Errorf("expected CI prompt, got %s", ps.active.id)
	}
}

func TestPromptState_ConditionDetectsLargeRepo(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 6000})
	if ps.active == nil {
		t.Fatal("expected prompt to fire for large repo")
	}
	if ps.active.id != promptLargeRepo {
		t.Errorf("expected large-repo prompt, got %s", ps.active.id)
	}
}

func TestPromptState_NoPromptWhenNoCondition(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps.active != nil {
		t.Errorf("expected no prompt, got %s", ps.active.id)
	}
}

func TestPromptState_PersistsSeen(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps.active == nil {
		t.Fatal("expected prompt")
	}
	ps.dismiss(1)

	data, err := os.ReadFile(filepath.Join(senseDir, "seen_prompts"))
	if err != nil {
		t.Fatalf("seen_prompts should exist: %v", err)
	}
	var f seenPromptsFile
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if f.MajorVersion != 1 {
		t.Errorf("expected major version 1, got %d", f.MajorVersion)
	}
	found := false
	for _, id := range f.Seen {
		if id == string(promptClaude) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected claude in seen list, got %v", f.Seen)
	}
}

func TestPromptState_SeenNotShownAgain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps1 := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	ps1.dismiss(1)

	ps2 := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps2.active != nil {
		t.Errorf("dismissed prompt should not reappear, got %s", ps2.active.id)
	}
}

func TestPromptState_VersionResetShowsAgain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps1 := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	ps1.dismiss(1)

	ps2 := newPromptState(senseDir, 2, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps2.active == nil {
		t.Fatal("new major version should reset seen prompts")
	}
	if ps2.active.id != promptClaude {
		t.Errorf("expected claude prompt after version reset, got %s", ps2.active.id)
	}
}

func TestPromptState_Render(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	dim := testDimStyle()
	accent := testDimStyle()

	got := ps.render(120, dim, accent)
	if !containsText(got, "Claude Code detected") {
		t.Errorf("expected Claude Code text, got %q", got)
	}
	if !containsText(got, "[x]") {
		t.Errorf("expected dismiss hint, got %q", got)
	}
}

func TestPromptState_RenderEmpty(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	dim := testDimStyle()
	accent := testDimStyle()

	if got := ps.render(120, dim, accent); got != "" {
		t.Errorf("no active prompt should render empty, got %q", got)
	}
}

func TestPromptState_DismissAdvancesToNext(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".github", "workflows"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	ps := newPromptState(senseDir, 1, promptContext{ProjectRoot: dir, Symbols: 100})
	if ps.active == nil {
		t.Fatal("expected first prompt")
	}
	firstID := ps.active.id

	ps.dismiss(1)
	if ps.active == nil {
		t.Fatal("expected second prompt after dismissing first")
	}
	if ps.active.id == firstID {
		t.Error("second prompt should be different from first")
	}
}

func TestPrompt_XKeyDismissesInNormalMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil,
		WithEcosystemPrompts(senseDir, dir, 1))
	m.width = 120
	m.height = 24
	if !m.prompt.hasActive() {
		t.Fatal("expected active prompt")
	}

	updated, _ := m.Update(runeKey("x"))
	um := updated.(model)
	if um.prompt.hasActive() {
		t.Error("x in normal mode should dismiss the prompt")
	}
}

func TestPrompt_XKeyPassesThroughInSearchMode(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil,
		WithEcosystemPrompts(senseDir, dir, 1))
	m.width = 120
	m.height = 24
	m.mode = ModeSearch
	m.searchState = &searchState{query: "test"}
	if !m.prompt.hasActive() {
		t.Fatal("expected active prompt")
	}

	updated, _ := m.Update(runeKey("x"))
	um := updated.(model)
	if !um.prompt.hasActive() {
		t.Error("x in search mode should not dismiss the prompt")
	}
	if um.searchState.query != "testx" {
		t.Errorf("x should append to search query, got %q", um.searchState.query)
	}
}

func TestPromptInView(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".claude"), 0755); err != nil {
		t.Fatal(err)
	}
	senseDir := filepath.Join(dir, ".sense")
	if err := os.Mkdir(senseDir, 0755); err != nil {
		t.Fatal(err)
	}

	m := newModel(graphStats{Symbols: 10, Edges: 5}, testLayout(), nil, nil,
		WithEcosystemPrompts(senseDir, dir, 1))
	m.width = 120
	m.height = 24

	v := m.View()
	if !containsText(v, "Claude Code detected") {
		t.Error("View() should include ecosystem prompt text")
	}
	if !containsText(v, "[x]") {
		t.Error("View() should include dismiss hint")
	}
}
