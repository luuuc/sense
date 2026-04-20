package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedBlastProject mirrors seedGraphProject but wires a slightly
// richer caller graph so the direct/indirect/tests sections all
// populate. Shape:
//
//   OrdersController#create → CheckoutService
//   WebhookJob#process      → OrdersController#create   (indirect → CheckoutService)
//   CheckoutServiceTest     → CheckoutService            (tests edge)
func seedBlastProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	files := []model.File{
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()},
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()},
		{Path: "app/jobs/webhook_job.rb", Language: "ruby", Hash: "c", IndexedAt: time.Now()},
		{Path: "test/services/checkout_service_test.rb", Language: "ruby", Hash: "d", IndexedAt: time.Now()},
	}
	fids := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fids[i] = id
	}

	syms := []model.Symbol{
		{FileID: fids[0], Name: "CheckoutService", Qualified: "App::Services::CheckoutService", Kind: "class", LineStart: 12, LineEnd: 85},
		{FileID: fids[1], Name: "create", Qualified: "OrdersController#create", Kind: "method", LineStart: 10, LineEnd: 18},
		{FileID: fids[2], Name: "process", Qualified: "WebhookJob#process", Kind: "method", LineStart: 5, LineEnd: 12},
		{FileID: fids[3], Name: "test_checkout", Qualified: "CheckoutServiceTest#test_checkout", Kind: "method", LineStart: 1, LineEnd: 5},
	}
	sids := make([]int64, len(syms))
	for i := range syms {
		id, werr := adapter.WriteSymbol(ctx, &syms[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		sids[i] = id
	}

	edges := []model.Edge{
		{SourceID: &sids[1], TargetID: sids[0], Kind: model.EdgeCalls, FileID: fids[1], Confidence: 1.0},
		{SourceID: &sids[2], TargetID: sids[1], Kind: model.EdgeCalls, FileID: fids[2], Confidence: 1.0},
		{SourceID: &sids[3], TargetID: sids[0], Kind: model.EdgeTests, FileID: fids[3], Confidence: 0.8},
	}
	for i := range edges {
		if _, werr := adapter.WriteEdge(ctx, &edges[i]); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}
	return dir
}

func TestRunBlastHumanSuccess(t *testing.T) {
	dir := seedBlastProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"App::Services::CheckoutService  risk: low  (1 direct caller)",
		"Direct callers (1):",
		"OrdersController#create  app/controllers/orders_controller.rb",
		"Indirect callers (1):",
		"WebhookJob#process  via OrdersController#create (2 hops)",
		"Affected tests (1):",
		"test/services/checkout_service_test.rb",
		"Total affected: 2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRunBlastJSONMatchesSchema(t *testing.T) {
	dir := seedBlastProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"App::Services::CheckoutService", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var parsed struct {
		Symbol          string           `json:"symbol"`
		Risk            string           `json:"risk"`
		DirectCallers   []map[string]any `json:"direct_callers"`
		IndirectCallers []map[string]any `json:"indirect_callers"`
		AffectedTests   []string         `json:"affected_tests"`
		TotalAffected   int              `json:"total_affected"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, stdout.String())
	}
	if parsed.Symbol != "App::Services::CheckoutService" {
		t.Errorf("symbol = %q", parsed.Symbol)
	}
	if len(parsed.DirectCallers) != 1 {
		t.Errorf("direct_callers = %d, want 1", len(parsed.DirectCallers))
	}
	if len(parsed.IndirectCallers) != 1 {
		t.Errorf("indirect_callers = %d, want 1", len(parsed.IndirectCallers))
	}
	if parsed.TotalAffected != 2 {
		t.Errorf("total_affected = %d, want 2", parsed.TotalAffected)
	}
}

func TestRunBlastNotFoundExit2(t *testing.T) {
	dir := seedBlastProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"NoSuchSymbol"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSymbolIssue {
		t.Errorf("exit = %d, want %d", code, ExitSymbolIssue)
	}
}

func TestRunBlastMissingIndexExit3(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"Anything"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("exit = %d, want %d", code, ExitIndexMissing)
	}
}

// TestRunBlastCorruptIndexExit4 mirrors the graph-side test:
// garbage bytes in .sense/index.db trigger ErrIndexCorrupt →
// exit code 4.
func TestRunBlastCorruptIndexExit4(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "index.db"),
		[]byte("garbage"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"Anything"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexCorrupt {
		t.Errorf("exit = %d, want %d (ExitIndexCorrupt)", code, ExitIndexCorrupt)
	}
}

// TestRunBlastJSONRoundTripsCanonical sibling to
// TestRunGraphJSONRoundTripsCanonical — CLI output must be the
// canonical mcpio wire shape byte-for-byte, not merely parseable.
func TestRunBlastJSONRoundTripsCanonical(t *testing.T) {
	dir := seedBlastProject(t)
	var stdout, stderr bytes.Buffer
	if code := RunBlast([]string{"App::Services::CheckoutService", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}); code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	assertOneTrailingNewline(t, stdout.Bytes())
	cliBytes := bytes.TrimRight(stdout.Bytes(), "\n")

	var parsed mcpio.BlastResponse
	if err := json.Unmarshal(cliBytes, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, cliBytes)
	}
	canonical, err := mcpio.MarshalBlast(parsed)
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	if !bytes.Equal(cliBytes, canonical) {
		t.Fatalf("CLI output is not canonical\n--- cli ---\n%s\n--- canonical ---\n%s",
			cliBytes, canonical)
	}
}

// TestRunBlastDiffJSONRoundTripsCanonical pins the same invariant
// for the diff form — the synthesized "diff:<ref>" subject must
// round-trip byte-for-byte through the canonical marshaller.
func TestRunBlastDiffJSONRoundTripsCanonical(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	var stdout, stderr bytes.Buffer
	if code := RunBlast([]string{"--diff", ref, "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}); code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	assertOneTrailingNewline(t, stdout.Bytes())
	cliBytes := bytes.TrimRight(stdout.Bytes(), "\n")

	var parsed mcpio.BlastResponse
	if err := json.Unmarshal(cliBytes, &parsed); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, cliBytes)
	}
	canonical, err := mcpio.MarshalBlast(parsed)
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	if !bytes.Equal(cliBytes, canonical) {
		t.Fatalf("CLI output is not canonical\n--- cli ---\n%s\n--- canonical ---\n%s",
			cliBytes, canonical)
	}
}

// TestRunBlastFlagOrderInvariant mirrors the graph-side pin: flags
// can appear before or after the positional symbol without changing
// behavior. Regression gate against stdlib flag.Parse's default
// "stop at first non-flag" posture.
func TestRunBlastFlagOrderInvariant(t *testing.T) {
	dir := seedBlastProject(t)

	runCapture := func(args []string) string {
		var stdout, stderr bytes.Buffer
		code := RunBlast(args, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("%v: exit=%d stderr=%s", args, code, stderr.String())
		}
		return stdout.String()
	}

	a := runCapture([]string{"App::Services::CheckoutService", "--json"})
	b := runCapture([]string{"--json", "App::Services::CheckoutService"})
	if a != b {
		t.Fatalf("flag order changed output\n--- positional-first ---\n%s\n--- flag-first ---\n%s", a, b)
	}
}

// seedBlastGitProject extends seedBlastProject with a real .git
// so `git diff` runs inside the tempdir. The helper returns the
// tempdir along with a "past" ref (HEAD~1) whose diff against HEAD
// covers the CheckoutService file — i.e. running blast --diff
// HEAD~1 here should produce the same union as blasting
// App::Services::CheckoutService directly.
func seedBlastGitProject(t *testing.T) (dir, pastRef string) {
	t.Helper()
	dir = seedBlastProject(t)

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=sense", "GIT_AUTHOR_EMAIL=sense@example.com",
			"GIT_COMMITTER_NAME=sense", "GIT_COMMITTER_EMAIL=sense@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--quiet", "-b", "main")
	// First commit: empty source file matching the indexed path so
	// the working tree matches the file paths sense already knows.
	for _, rel := range []string{
		"app/services/checkout_service.rb",
		"app/controllers/orders_controller.rb",
		"app/jobs/webhook_job.rb",
		"test/services/checkout_service_test.rb",
	} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("# v1\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "v1")

	// Second commit: edit the CheckoutService file so HEAD~1..HEAD
	// shows it as changed.
	changed := filepath.Join(dir, "app/services/checkout_service.rb")
	if err := os.WriteFile(changed, []byte("# v2\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", changed)
	run("commit", "--quiet", "-m", "v2")

	return dir, "HEAD~1"
}

func TestRunBlastDiffHuman(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", ref},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"diff:HEAD~1",
		"Direct callers (1):",
		"OrdersController#create",
		"Indirect callers (1):",
		"WebhookJob#process",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRunBlastDiffJSONSchema(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", ref, "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	var parsed struct {
		Symbol          string           `json:"symbol"`
		DirectCallers   []map[string]any `json:"direct_callers"`
		IndirectCallers []map[string]any `json:"indirect_callers"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("Unmarshal: %v\n%s", err, stdout.String())
	}
	if parsed.Symbol != "diff:HEAD~1" {
		t.Errorf("symbol = %q, want diff:HEAD~1", parsed.Symbol)
	}
	if len(parsed.DirectCallers) != 1 || len(parsed.IndirectCallers) != 1 {
		t.Errorf("callers direct=%d indirect=%d, want 1/1",
			len(parsed.DirectCallers), len(parsed.IndirectCallers))
	}
}

// TestRunBlastDiffEmptyWhenNoIndexedFiles verifies that a diff
// touching only unindexed paths produces an empty-but-successful
// response — documented Cycle-1 behavior: docs changes have no
// blast radius.
func TestRunBlastDiffEmptyWhenNoIndexedFiles(t *testing.T) {
	dir, _ := seedBlastGitProject(t)
	// Add and commit a README; HEAD~0..HEAD (empty) does nothing, so
	// we diff the README commit alone.
	readmePath := filepath.Join(dir, "README.md")
	if err := os.WriteFile(readmePath, []byte("# Docs\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=sense", "GIT_AUTHOR_EMAIL=sense@example.com",
			"GIT_COMMITTER_NAME=sense", "GIT_COMMITTER_EMAIL=sense@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("add", readmePath)
	run("commit", "--quiet", "-m", "docs")

	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", "HEAD~1"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Total affected: 0") {
		t.Errorf("expected Total affected: 0, got:\n%s", stdout.String())
	}
}

// TestRunBlastDiffBadRefErrors verifies the diff form surfaces
// git's complaint when the ref doesn't resolve. Uses the tempdir
// from seedBlastProject which is not a git repo — so `git diff`
// fails at the "not a git repository" level, exercising the
// gitDiffFiles error path.
func TestRunBlastDiffBadRefErrors(t *testing.T) {
	dir := seedBlastProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", "nonexistent-ref"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "git diff") {
		t.Errorf("expected git diff error, got: %s", stderr.String())
	}
}
