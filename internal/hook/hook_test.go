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

func TestPreToolUseAgentExplorationIntentAdvise(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","prompt":"explore the codebase to understand the architecture"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected advisory context for exploration-intent agent, got empty")
	}
}

func TestPreToolUseAgentDescriptionFallbackAdvise(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Agent","tool_input":{"subagent_type":"general-purpose","description":"explore the codebase structure"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected advisory context for exploration-intent description, got empty")
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

func TestPreToolUseConceptSearchAdvise(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Grep","tool_input":{"pattern":"error handling middleware"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected advisory for concept search pattern")
	}
	if !strings.Contains(resp.AdditionalContext, "sense_search") {
		t.Error("expected sense_search suggestion in advisory")
	}
}

func TestPreToolUseBashFindCodeAdvise(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"find . -name \"*.go\" -type f"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected advisory for find code files")
	}
}

func TestPreToolUseBashHeadCodeAdvise(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Bash","tool_input":{"command":"head -20 internal/hook/pre_tool_use.go"}}`
	var buf bytes.Buffer
	Run("pre-tool-use", dir, strings.NewReader(input), &buf)

	var resp hookResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.AdditionalContext == "" {
		t.Fatal("expected advisory for head on code file")
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

func TestExtractWrittenPath(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"Write", `{"tool_name":"Write","tool_input":{"file_path":"/tmp/foo.go"}}`, "/tmp/foo.go"},
		{"Edit", `{"tool_name":"Edit","tool_input":{"file_path":"/tmp/bar.go"}}`, "/tmp/bar.go"},
		{"NotebookEdit", `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/tmp/nb.ipynb"}}`, "/tmp/nb.ipynb"},
		{"Bash", `{"tool_name":"Bash","tool_input":{"command":"echo hi"}}`, ""},
		{"Grep", `{"tool_name":"Grep","tool_input":{"pattern":"foo"}}`, ""},
		{"EmptyInput", `{"tool_name":"Write","tool_input":{}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req postToolUseInput
			if err := json.Unmarshal([]byte(tc.raw), &req); err != nil {
				t.Fatal(err)
			}
			if got := extractWrittenPath(req); got != tc.want {
				t.Errorf("extractWrittenPath = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPostToolUseWriteUpdatesIndex(t *testing.T) {
	dir := indexedDir(t)

	newFile := filepath.Join(dir, "new_service.go")
	if err := os.WriteFile(newFile, []byte("package main\n\nfunc NewService() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Write","tool_input":{"file_path":"` + newFile + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {}", buf.String())
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE name = 'NewService'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("expected NewService symbol in index after PostToolUse Write")
	}
}

func TestPostToolUseEditUpdatesIndex(t *testing.T) {
	dir := indexedDir(t)

	newFile := filepath.Join(dir, "edited.go")
	if err := os.WriteFile(newFile, []byte("package main\n\nfunc EditedFunc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Edit","tool_input":{"file_path":"` + newFile + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("output = %q, want {}", buf.String())
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE name = 'EditedFunc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("expected EditedFunc symbol in index after PostToolUse Edit")
	}
}

func TestPostToolUseNonSourceFileNoOp(t *testing.T) {
	dir := indexedDir(t)

	mdFile := filepath.Join(dir, "README.md")
	if err := os.WriteFile(mdFile, []byte("# README"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Write","tool_input":{"file_path":"` + mdFile + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("expected no-op for .md file, got %q", buf.String())
	}
}

func TestPostToolUseIgnoredFileNoOp(t *testing.T) {
	dir := indexedDir(t)

	vendorDir := filepath.Join(dir, "vendor")
	if err := os.MkdirAll(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	vendorFile := filepath.Join(vendorDir, "dep.go")
	if err := os.WriteFile(vendorFile, []byte("package vendor\n\nfunc VendorFunc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Write","tool_input":{"file_path":"` + vendorFile + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("expected no-op for vendor file, got %q", buf.String())
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE name = 'VendorFunc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("vendor file should not be indexed")
	}
}

func TestPostToolUseSenseDirNoOp(t *testing.T) {
	dir := indexedDir(t)

	senseFile := filepath.Join(dir, ".sense", "stray.go")
	if err := os.WriteFile(senseFile, []byte("package stray\n\nfunc Stray() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Write","tool_input":{"file_path":"` + senseFile + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("expected no-op for .sense/ file, got %q", buf.String())
	}
}

func TestPostToolUseIdempotent(t *testing.T) {
	dir := indexedDir(t)

	newFile := filepath.Join(dir, "idempotent.go")
	if err := os.WriteFile(newFile, []byte("package main\n\nfunc IdempotentFunc() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := `{"tool_name":"Write","tool_input":{"file_path":"` + newFile + `"}}`

	var buf1 bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf1)
	var buf2 bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf2)

	if buf1.String() != "{}\n" || buf2.String() != "{}\n" {
		t.Error("expected both runs to output {}")
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE name = 'IdempotentFunc'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 IdempotentFunc symbol, got %d", count)
	}
}

func TestPostToolUseNoIndex(t *testing.T) {
	dir := t.TempDir()
	input := `{"tool_name":"Write","tool_input":{"file_path":"` + filepath.Join(dir, "foo.go") + `"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("expected no-op when no index, got %q", buf.String())
	}
}

func TestPostToolUseOutsideProjectNoOp(t *testing.T) {
	dir := indexedDir(t)
	input := `{"tool_name":"Write","tool_input":{"file_path":"/tmp/outside.go"}}`
	var buf bytes.Buffer
	Run("post-tool-use", dir, strings.NewReader(input), &buf)

	if buf.String() != "{}\n" {
		t.Errorf("expected no-op for file outside project, got %q", buf.String())
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
