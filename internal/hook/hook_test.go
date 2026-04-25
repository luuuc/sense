package hook

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/scan"
	_ "modernc.org/sqlite"
)

func indexedDir(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	// Write a Go file with enough structure for the index.
	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc UserService() {}\n\nfunc main() { UserService() }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	return root
}

func staleDir(t *testing.T) string {
	t.Helper()
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	old := time.Now().Add(-48 * time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE sense_files SET indexed_at = ?`, old); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunUnknownHook(t *testing.T) {
	var buf bytes.Buffer
	code := Run("nonexistent", ".", strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {}", buf.String())
	}
}

func TestRunNoIndex(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {} (no index)", buf.String())
	}
}

func TestSessionStartReturnsStats(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("session-start", dir, strings.NewReader("{}"), &buf)

	var resp messageResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Message == "" {
		t.Fatal("expected non-empty message")
	}
	if !strings.Contains(resp.Message, "Sense index:") {
		t.Errorf("message = %q, expected 'Sense index:' prefix", resp.Message)
	}
	if !strings.Contains(resp.Message, "ToolSearch") {
		t.Errorf("message = %q, expected ToolSearch directive", resp.Message)
	}
}

func TestPreCompactReturnsHubs(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("pre-compact", dir, strings.NewReader("{}"), &buf)

	var resp messageResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if !strings.Contains(resp.Message, "Sense Index Summary") {
		t.Errorf("message = %q, expected summary", resp.Message)
	}
}

func TestSubagentStartReturnsGuidance(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("subagent-start", dir, strings.NewReader("{}"), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if !strings.Contains(resp.AdditionalContext, "Sense index") {
		t.Errorf("context = %q, expected sense guidance", resp.AdditionalContext)
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph in guidance")
	}
	if !strings.Contains(resp.AdditionalContext, "parent agent") {
		t.Error("expected parent agent delegation guidance")
	}
}

func TestPreToolUseSymbolMatch(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp denyResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Output.Decision != "deny" {
		t.Fatalf("expected deny decision, got %q", resp.Output.Decision)
	}
	if !strings.Contains(resp.Output.Reason, "sense_graph") {
		t.Error("expected sense_graph suggestion in deny reason")
	}
}

func TestPreToolUseRegexNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"func.*Service"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("regex pattern should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseNoMatch(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"NonexistentSymbol"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("unknown symbol should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseAgentDeny(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"deep-explore","prompt":"find all callers"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp denyResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Output.Decision != "deny" {
		t.Fatalf("expected deny decision for deep-explore, got %q", resp.Output.Decision)
	}
	if !strings.Contains(resp.Output.Reason, "sense_graph") {
		t.Error("expected sense_graph in deny reason")
	}
}

func TestPreToolUseAgentAllowNonExplore(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","prompt":"do something"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("non-exploration agent should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseBashGrepDeny(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"grep -rn UserService ."}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp denyResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Output.Decision != "deny" {
		t.Fatalf("expected deny for bash grep of known symbol, got %q", resp.Output.Decision)
	}
}

func TestPreToolUseBashNonGrepNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("non-grep bash should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseExploreAgentDeny(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"Explore","prompt":"find implementations"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp denyResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Output.Decision != "deny" {
		t.Fatalf("expected deny for Explore agent, got %q", resp.Output.Decision)
	}
}

func TestPreToolUseStaleIndexAdvisesInsteadOfDeny(t *testing.T) {
	dir := staleDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var deny denyResponse
	if err := json.Unmarshal(buf.Bytes(), &deny); err == nil && deny.Output.Decision == "deny" {
		t.Fatal("stale index should advise, not deny")
	}

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected additionalContext for stale index")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph suggestion in advisory")
	}
}

func TestPreToolUseBashStaleIndexAdvises(t *testing.T) {
	dir := staleDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"grep -rn UserService ."}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var deny denyResponse
	if err := json.Unmarshal(buf.Bytes(), &deny); err == nil && deny.Output.Decision == "deny" {
		t.Fatal("stale index should advise, not deny")
	}

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected additionalContext for stale bash grep")
	}
}

func TestExtractBashPattern(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
	}{
		{"grep -rn UserService .", "UserService"},
		{"grep UserService", "UserService"},
		{"grep -rn 'UserService' .", "UserService"},
		{"rg UserService", "UserService"},
		{"ag UserService", "UserService"},
		{"grep -e pattern .", ""},
		{"ls -la", ""},
		{"git status", ""},
		{"", ""},
		{"grep -rn --include '*.go' UserService .", "UserService"},
		{"grep -rn UserService . | head -20", "UserService"},
		{"grep -rn UserService . && echo done", "UserService"},
		{"grep -rn UserService ; echo done", "UserService"},
		{"grep -rn UserService . || true", "UserService"},
	}
	for _, tc := range cases {
		if got := extractBashPattern(tc.cmd); got != tc.want {
			t.Errorf("extractBashPattern(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestIsSymbolShaped(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"UserService", true},
		{"user_service", true},
		{"User.find", true},
		{"User::Service", true},
		{"User#method", true},
		{"func.*Adapter", false},
		{"TODO|FIXME", false},
		{"(test)", false},
		{"[a-z]+", false},
		{"a", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isSymbolShaped(tc.pattern); got != tc.want {
			t.Errorf("isSymbolShaped(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}
