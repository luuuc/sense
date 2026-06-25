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
	"testing/iotest"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
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

func TestRunSenseBenchSuppressesAllHooks(t *testing.T) {
	dir := indexedDir(t)
	t.Setenv("SENSE_BENCH", "1")

	hooks := []string{"session-start", "pre-tool-use", "subagent-start", "pre-compact"}
	for _, hook := range hooks {
		var buf bytes.Buffer
		code := Run(hook, dir, strings.NewReader(`{"tool_name":"Grep","tool_input":{"pattern":"UserService"}}`), &buf)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0", hook, code)
		}
		if buf.String() != "{}\n" {
			t.Errorf("%s: output = %q, want {} (SENSE_BENCH should suppress)", hook, buf.String())
		}
	}
}

func TestSilentRunStdinReadError(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("session-start", dir, iotest.ErrReader(io.ErrUnexpectedEOF), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {} (stdin read error)", buf.String())
	}
}

func TestSilentRunOpenError(t *testing.T) {
	dir := t.TempDir()
	// Create .sense/index.db as a directory: os.Stat succeeds (so we get
	// past the existence check) but sqlite.Open fails to open it.
	dbPath := filepath.Join(dir, ".sense", "index.db")
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {} (open error)", buf.String())
	}
}

func TestSilentRunMarshalError(t *testing.T) {
	dir := indexedDir(t)
	// A handler returning a non-nil but unmarshalable value (a channel)
	// drives the json.Marshal error branch.
	fn := func(_ context.Context, _ json.RawMessage, _ *sqlite.Adapter, _ string) (any, error) {
		return make(chan int), nil
	}
	var buf bytes.Buffer
	silentRun(dir, strings.NewReader("{}"), &buf, fn)
	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {} (marshal error)", buf.String())
	}
}

func TestIndexPathHonorsSENSEDIR(t *testing.T) {
	t.Setenv("SENSE_DIR", "/tmp/sense-override")
	got := indexPath("/some/project")
	want := "/tmp/sense-override/index.db"
	if got != want {
		t.Errorf("indexPath = %q, want %q", got, want)
	}
}

func TestIndexPathDefault(t *testing.T) {
	t.Setenv("SENSE_DIR", "")
	got := indexPath("/some/project")
	want := filepath.Join("/some/project", ".sense", "index.db")
	if got != want {
		t.Errorf("indexPath = %q, want %q", got, want)
	}
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
	// The tools are pre-loaded (alwaysLoad), so the message must NOT revive the
	// retired ToolSearch gate; it should point at the loaded tools directly.
	if strings.Contains(resp.Message, "ToolSearch") {
		t.Errorf("message = %q, should not instruct ToolSearch (tools are pre-loaded)", resp.Message)
	}
	if !strings.Contains(resp.Message, "sense_graph") {
		t.Errorf("message = %q, expected the loaded-tools hint", resp.Message)
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

func TestSubagentStartEmptyIndex(t *testing.T) {
	root := t.TempDir()
	// Create DB with schema but no symbols.
	goFile := filepath.Join(root, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	var buf bytes.Buffer
	Run("subagent-start", root, strings.NewReader("{}"), &buf)
	if buf.String() != "{}\n" {
		t.Errorf("expected empty response for empty index, got %q", buf.String())
	}
}

func TestSubagentStartEdgeCountFallback(t *testing.T) {
	root := indexedDir(t)
	dbPath := filepath.Join(root, ".sense", "index.db")

	// Open db directly, drop edges, and keep connection open
	// so we can call handleSubagentStart bypassing schema recreation.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec("DROP TABLE IF EXISTS sense_edges"); err != nil {
		t.Fatalf("drop table: %v", err)
	}

	adapter, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	// Drop edges again after adapter reopened (schema recreates it).
	if _, err := adapter.DB().Exec("DROP TABLE sense_edges"); err != nil {
		t.Fatalf("drop table after adapter open: %v", err)
	}

	result, err := handleSubagentStart(context.Background(), nil, adapter, root)
	if err != nil {
		t.Fatalf("handleSubagentStart: %v", err)
	}
	resp := result.(*hookResponse)
	if !strings.Contains(resp.AdditionalContext, "0 edges") {
		t.Errorf("expected 0 edges fallback, got %q", resp.AdditionalContext)
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
	ctx := resp.AdditionalContext
	if !strings.Contains(ctx, "Sense index") {
		t.Errorf("context = %q, expected sense guidance", ctx)
	}
	if strings.Contains(ctx, "ToolSearch") {
		t.Error("guidance should not revive the ToolSearch gate — tools are pre-loaded")
	}
	if !strings.Contains(ctx, "loaded and callable") {
		t.Error("expected the pre-loaded-tools hint in guidance")
	}
	if !strings.Contains(ctx, "sense_graph") {
		t.Error("expected sense_graph in guidance")
	}
	if !strings.Contains(ctx, "sense_search") {
		t.Error("expected sense_search in guidance")
	}
	if !strings.Contains(ctx, "sense_blast") {
		t.Error("expected sense_blast in guidance")
	}
	if !strings.Contains(ctx, "edges") {
		t.Error("expected edge count in guidance")
	}
	if strings.Contains(ctx, "parent agent") {
		t.Error("should not contain old 'parent agent' delegation message")
	}
}

func TestPreToolUseSymbolMatchNudges(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for known symbol grep")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for known symbol grep")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph suggestion in additionalContext")
	}
}

func TestPreToolUseFilePathNoOp(t *testing.T) {
	dir := indexedDir(t)
	cases := []string{
		`{"tool_name":"Grep","tool_input":{"pattern":"internal/hook/pre_tool_use.go"}}`,
		`{"tool_name":"Grep","tool_input":{"pattern":"main.go"}}`,
	}
	for _, input := range cases {
		var buf bytes.Buffer
		Run("pre-tool-use", dir, strings.NewReader(input), &buf)
		if buf.String() != "{}\n" {
			t.Errorf("file-path pattern should be no-op, got %q for input %s", buf.String(), input)
		}
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

func TestPreToolUseAgentNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"deep-explore","prompt":"find all callers"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for known explorer")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for known explorer")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph in additionalContext")
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

func TestPreToolUseGlobFilePatternNoOp(t *testing.T) {
	dir := indexedDir(t)
	cases := []string{
		`{"tool_name":"Glob","tool_input":{"pattern":"src/controllers/**/*.rb"}}`,
		`{"tool_name":"Glob","tool_input":{"pattern":"internal/hook/*.go"}}`,
	}
	for _, input := range cases {
		var buf bytes.Buffer
		Run("pre-tool-use", dir, strings.NewReader(input), &buf)
		if buf.String() != "{}\n" {
			t.Errorf("Glob with file pattern should be no-op, got %q for input %s", buf.String(), input)
		}
	}
}

func TestPreToolUseGlobSymbolNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Glob","tool_input":{"pattern":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for Glob with symbol pattern")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for Glob with symbol pattern")
	}
}

func TestPreToolUseBashGrepNudges(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"grep -rn UserService ."}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for bash grep of known symbol")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for bash grep of known symbol")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Error("expected sense_graph suggestion in additionalContext")
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

func TestPreToolUseExploreAgentNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"Explore","prompt":"find implementations"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for Explore agent")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for Explore agent")
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
		// A search behind a cd prefix (the dominant real-world shape) is found.
		{"cd /repo && grep -rn UserService .", "UserService"},
		{"cd /repo ; grep -rn UserService .", "UserService"},
		{"cd /a/b || true && grep UserService", "UserService"},
		// Quoted multi-word patterns survive intact.
		{`grep -rn "func Open" internal`, "func Open"},
		{"cd /x && grep -rn 'func Open' internal", "func Open"},
		// A grep that only filters piped output is not a code search.
		{"cat foo.go | grep UserService", ""},
		{"git status | grep '^??'", ""},
		{"ls | grep -i foo", ""},
		// Path-qualified search binaries are recognised by basename.
		{"/usr/bin/grep -rn UserService .", "UserService"},
		{"egrep -rn UserService .", "UserService"},
		{"fgrep UserService .", "UserService"},
		// A value-taking flag with no following value must not over-consume.
		{"grep --include", ""},
		// A single & (background) acts as a statement boundary.
		{"echo a & grep UserService", "UserService"},
	}
	for _, tc := range cases {
		if got := extractBashPattern(tc.cmd); got != tc.want {
			t.Errorf("extractBashPattern(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

func TestSymbolFromPattern(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"UserService", "UserService"},
		{"Spree::Order", "Spree::Order"},
		{"func Open", "Open"},
		{"def perform", "perform"},
		{"class User", "User"},
		{"type Adapter", "Adapter"},
		{"module Billing", "Billing"},
		{"func Open(", "Open"},
		{"the model file", ""}, // multi-word literal, not a definition form
		{"a b c", ""},
		{"", ""},
		{"   ", ""},
	}
	for _, tc := range cases {
		if got := symbolFromPattern(tc.pattern); got != tc.want {
			t.Errorf("symbolFromPattern(%q) = %q, want %q", tc.pattern, got, tc.want)
		}
	}
}

func TestShellTokens(t *testing.T) {
	// Quoted multi-word stays one word; operators are split out and flagged.
	toks := shellTokens(`cd /x && grep -rn "func Open" . | head`)
	var got []string
	for _, tk := range toks {
		if tk.op {
			got = append(got, "op:"+tk.val)
		} else {
			got = append(got, tk.val)
		}
	}
	want := []string{"cd", "/x", "op:&&", "grep", "-rn", "func Open", ".", "op:|", "head"}
	if len(got) != len(want) {
		t.Fatalf("shellTokens tokens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("token[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// Single quotes are literal; double-quote escapes are unwrapped.
	if tk := shellTokens(`grep 'a;b'`); len(tk) != 2 || tk[1].val != "a;b" {
		t.Errorf("single-quoted literal not preserved: %+v", tk)
	}
	if tk := shellTokens(`echo "a\"b"`); len(tk) != 2 || tk[1].val != `a"b` {
		t.Errorf("escaped double-quote not unwrapped: %+v", tk)
	}
	// An unterminated quote extends to end-of-input instead of panicking.
	if tk := shellTokens(`grep "foo`); len(tk) != 2 || tk[1].val != "foo" {
		t.Errorf("unterminated quote not tolerated: %+v", tk)
	}
}

func TestPreToolUseBashMalformedCommand(t *testing.T) {
	dir := indexedDir(t)
	// Malformed or adversarial shell must degrade to valid JSON output, never
	// panic and never block the command. This hook runs on every Bash call, so
	// "be invisible when unsure" is the contract.
	cmds := []string{
		`grep -rn "func Open`, // unterminated double quote
		`grep -rn 'sym`,       // unterminated single quote
		`grep foo\`,           // lone trailing backslash
		`cd /x && grep "a\`,   // trailing backslash inside a quote
		`;; && | grep`,        // operator soup, no real command
		`grep`,                // search binary, no arguments
	}
	for _, cmd := range cmds {
		payload, err := json.Marshal(map[string]any{
			"tool_name":  "Bash",
			"tool_input": map[string]any{"command": cmd},
		})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		var buf bytes.Buffer
		Run("pre-tool-use", dir, bytes.NewReader(payload), &buf)
		if buf.Len() == 0 {
			t.Errorf("command %q produced no output", cmd)
			continue
		}
		var v map[string]any
		if err := json.Unmarshal(buf.Bytes(), &v); err != nil {
			t.Errorf("command %q produced invalid JSON %q: %v", cmd, buf.String(), err)
		}
	}
}

func TestPreToolUseBashCdPrefixNudges(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"cd /tmp && grep -rn UserService ."}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" || !strings.Contains(resp.AdditionalContext, "sense_graph") {
		t.Errorf("cd-prefixed grep of known symbol should nudge, got %q", buf.String())
	}
}

func TestPreToolUseBashQuotedDefNudges(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"grep -rn \"func UserService\" ."}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" || !strings.Contains(resp.AdditionalContext, "UserService") {
		t.Errorf("quoted def-form grep should nudge for the declared symbol, got %q", buf.String())
	}
}

func TestPreToolUseBashPipeFilterNoOp(t *testing.T) {
	dir := indexedDir(t)
	// grep here filters git's output; it is not a code search, so no nudge.
	input := `{"tool_name":"Bash","tool_input":{"command":"git status | grep UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("pipe-filter grep should be no-op, got %q", buf.String())
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
		{"src/controllers/user.rb", false},
		{"internal/hook/pre_tool_use.go", false},
		{"main.go", false},
		{"app.py", false},
		{"models/**/*.rb", false},
	}
	for _, tc := range cases {
		if got := isSymbolShaped(tc.pattern); got != tc.want {
			t.Errorf("isSymbolShaped(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestPreToolUseAgentExplorationIntentNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","prompt":"explore the codebase to understand the architecture"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for exploration-intent agent")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for exploration-intent agent")
	}
}

func TestPreToolUseAgentDescriptionFallbackNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","description":"explore the codebase structure"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for exploration-intent description")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for exploration-intent description")
	}
}

func TestPreToolUseAgentNoExplorationIntentPass(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","prompt":"format the markdown tables in README"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("non-exploration agent should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseConceptSearchNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"error handling middleware"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for concept search")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for concept search")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_search") {
		t.Error("expected sense_search suggestion in additionalContext")
	}
}

func TestPreToolUseBashFindCodeNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"find . -name \"*.go\" -type f"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for find code files")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for find code files")
	}
}

func TestPreToolUseBashHeadCodeNudge(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"head -20 internal/hook/pre_tool_use.go"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected non-empty systemMessage for head on code file")
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected non-empty additionalContext for head on code file")
	}
}

func TestPreToolUseUnknownToolNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Read","tool_input":{"file_path":"/tmp/foo.go"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("unknown tool should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseGlobNoMatchNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Glob","tool_input":{"pattern":"NonexistentSymbol"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("Glob with no matching symbol should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseMalformedInput(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(`not json`), &buf)
	if buf.String() != "{}\n" {
		t.Errorf("malformed input should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseGrepRegexField(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"regex":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected nudge for symbol via regex field")
	}
}

func TestPreToolUseGrepCommandField(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"command":"UserService"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp nudgeResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.SystemMessage == "" {
		t.Fatal("expected nudge for symbol via command field")
	}
}

func TestPreToolUseGrepEmptyPatternNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)
	if buf.String() != "{}\n" {
		t.Errorf("empty grep pattern should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseBashHeadNonCodePass(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"head -20 README.md"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("head on non-code file should be no-op, got %q", buf.String())
	}
}

func TestPreToolUseBashFindNonCodePass(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"find . -name \"*.md\" -type f"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("find non-code files should be no-op, got %q", buf.String())
	}
}

func TestHasExplorationKeyword(t *testing.T) {
	cases := []struct {
		prompt string
		want   bool
	}{
		{"explore the codebase to find auth flow", true},
		{"find implementations of the handler interface", true},
		{"who calls this function", true},
		{"understand the code structure", true},
		{"what would break if I change this", true},
		{"Deep codebase exploration using semantic search", true},
		{"CODEBASE EXPLORATION using semantic search", true},
		{"format the markdown tables in README", false},
		{"fix the typo in the error message", false},
		{"run the test suite", false},
		{"do something", false},
		{"update the codebase documentation", false},
		{"run linting on the codebase", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := hasExplorationKeyword(tc.prompt); got != tc.want {
			t.Errorf("hasExplorationKeyword(%q) = %v, want %v", tc.prompt, got, tc.want)
		}
	}
}

func TestIsMultiWordPattern(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"error handling middleware", true},
		{"review engine flow", true},
		{"a bc", true},
		{"auth", false},
		{"UserService", false},
		{"a b", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isMultiWordPattern(tc.pattern); got != tc.want {
			t.Errorf("isMultiWordPattern(%q) = %v, want %v", tc.pattern, got, tc.want)
		}
	}
}

func TestIsExplorationCommand(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{`find . -name "*.go" -type f`, true},
		{`find . -name "*.md"`, false},
		{`head -20 main.go`, true},
		{`head -20 README.md`, false},
		{`cat internal/hook/hook.go`, true},
		{`cat package.json`, false},
		{`tail -f app.log`, false},
		{`wc -l *.go`, true},
		{`wc -l *.md`, false},
		{`ls -la`, false},
		{`git status`, false},
		{`make build`, false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isExplorationCommand(tc.cmd); got != tc.want {
			t.Errorf("isExplorationCommand(%q) = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

func TestHasCodeExtension(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"main.go", true},
		{"app.py", true},
		{"index.ts", true},
		{"component.tsx", true},
		{"script.js", true},
		{"README.md", false},
		{"package.json", false},
		{"go.mod", false},
		{"Makefile", false},
		{"*.go", true},
	}
	for _, tc := range cases {
		if got := hasCodeExtension(tc.path); got != tc.want {
			t.Errorf("hasCodeExtension(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestHandleSessionStartEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	result, err := handleSessionStart(ctx, nil, adapter, dir)
	if err != nil {
		t.Fatalf("handleSessionStart error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for empty index, got %v", result)
	}
}

func TestHandleSessionStartWithLanguages(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	db := adapter.DB()
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'main.go', 'go', 'abc', 1, ?)`, time.Now().Format(time.RFC3339Nano))
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (2, 'app.py', 'python', 'def', 1, ?)`, time.Now().Format(time.RFC3339Nano))
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols (id, file_id, name, qualified, kind, visibility, parent_id, line_start, line_end, docstring, complexity, snippet) VALUES (1, 1, 'main', 'main', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols (id, file_id, name, qualified, kind, visibility, parent_id, line_start, line_end, docstring, complexity, snippet) VALUES (2, 2, 'app', 'app', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`)
	_, _ = db.ExecContext(ctx, `INSERT INTO sense_edges (id, source_id, target_id, kind, file_id, line, confidence) VALUES (1, 1, 2, 'calls', 1, 1, 1.0)`)

	result, err := handleSessionStart(ctx, nil, adapter, dir)
	if err != nil {
		t.Fatalf("handleSessionStart error: %v", err)
	}
	resp, ok := result.(*messageResponse)
	if !ok {
		t.Fatalf("expected *messageResponse, got %T", result)
	}
	if !strings.Contains(resp.Message, "go, python") {
		t.Errorf("expected languages in message, got: %s", resp.Message)
	}
	if !strings.Contains(resp.Message, "2 symbols, 1 edges, 2 languages") {
		t.Errorf("expected stats in message, got: %s", resp.Message)
	}
}

func TestHandleSessionStartLastScanFormats(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	db := adapter.DB()

	cases := []struct {
		name      string
		indexedAt string
		wantAge   string
	}{
		{
			name:      "just_now",
			indexedAt: time.Now().Add(-30 * time.Second).Format(time.RFC3339Nano),
			wantAge:   "just now",
		},
		{
			name:      "minutes_ago",
			indexedAt: time.Now().Add(-5 * time.Minute).Format(time.RFC3339Nano),
			wantAge:   "5m0s ago",
		},
		{
			name:      "invalid_format",
			indexedAt: "not-a-valid-time",
			wantAge:   "unknown",
		},
		{
			name:      "empty",
			indexedAt: "",
			wantAge:   "unknown",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Clear tables
			_, _ = db.ExecContext(ctx, `DELETE FROM sense_files`)
			_, _ = db.ExecContext(ctx, `DELETE FROM sense_symbols`)
			_, _ = db.ExecContext(ctx, `DELETE FROM sense_edges`)

			_, _ = db.ExecContext(ctx, `INSERT INTO sense_files (id, path, language, hash, symbols, indexed_at) VALUES (1, 'main.go', 'go', 'abc', 1, ?)`, tc.indexedAt)
			_, _ = db.ExecContext(ctx, `INSERT INTO sense_symbols (id, file_id, name, qualified, kind, visibility, parent_id, line_start, line_end, docstring, complexity, snippet) VALUES (1, 1, 'main', 'main', 'function', 'public', NULL, 1, 1, NULL, NULL, NULL)`)

			result, err := handleSessionStart(ctx, nil, adapter, dir)
			if err != nil {
				t.Fatalf("handleSessionStart error: %v", err)
			}
			resp, ok := result.(*messageResponse)
			if !ok {
				t.Fatalf("expected *messageResponse, got %T", result)
			}
			if !strings.Contains(resp.Message, tc.wantAge) {
				t.Errorf("expected %q in message, got: %s", tc.wantAge, resp.Message)
			}
		})
	}
}
