package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedBlastProject mirrors seedGraphProject but wires a slightly
// richer caller graph so the direct/indirect/tests sections all
// populate. Shape:
//
//	OrdersController#create → CheckoutService
//	WebhookJob#process      → OrdersController#create   (indirect → CheckoutService)
//	CheckoutServiceTest     → CheckoutService            (tests edge)
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

// degradeIndexColumn models a partially-migrated index: it reopens a
// fully-populated on-disk index and drops a single non-indexed column
// from one table, then stamps the current schema version. The column
// must be free of any index, constraint, or FTS reference so the table
// keeps its indexes and full-text triggers intact — that way
// sqlite.Open's schema re-apply (CREATE ... IF NOT EXISTS) stays a
// no-op and OpenIndex returns the adapter cleanly, while any later
// query that reads the dropped column fails for real with "no such
// column". This is the same degraded-index technique the hook/profile
// tests use (a table missing a column a query needs), seeded on disk
// so the Run* command opens it itself and the user-facing error exit
// codes get exercised. A missing column is one genuinely-possible
// shape of a half-migrated index: sqlite.Open only rebuilds on a
// user_version mismatch and otherwise heals via CREATE TABLE IF NOT
// EXISTS, which cannot restore a column on a table that still exists.
func degradeIndexColumn(t *testing.T, dir, table, column string) {
	t.Helper()
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("reopen index: %v", err)
	}
	defer func() { _ = adapter.Close() }()
	db := adapter.DB()
	if _, err := db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s", table, column)); err != nil {
		t.Fatalf("drop %s.%s: %v", table, column, err)
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", sqlite.SchemaVersion)); err != nil {
		t.Fatalf("stamp user_version: %v", err)
	}
}

// TestCollectBlastFileIDsIncludesEdgeKindGroups guards the empty-path
// regression: AffectedViaComposition is computed from the edge table
// independently of the capped caller lists, so a composer's file may be
// referenced by no caller. CollectBlastFileIDs must still gather it, or that
// dependent renders with an empty path and loses its citation.
func TestCollectBlastFileIDsIncludesEdgeKindGroups(t *testing.T) {
	r := blast.Result{
		Symbol:        model.Symbol{ID: 1, FileID: 1},
		DirectCallers: []model.Symbol{{ID: 2, FileID: 2}},
		// a composer whose file (99) appears nowhere in the caller lists
		AffectedViaComposition: []model.Symbol{{ID: 9, FileID: 99}},
		AffectedSubclasses:     []model.Symbol{{ID: 8, FileID: 88}},
		AffectedViaIncludes:    []model.Symbol{{ID: 7, FileID: 77}},
	}
	got := map[int64]bool{}
	for _, id := range CollectBlastFileIDs(r) {
		got[id] = true
	}
	for _, want := range []int64{1, 2, 99, 88, 77} {
		if !got[want] {
			t.Errorf("CollectBlastFileIDs missing file id %d (edge-kind group file not collected)", want)
		}
	}
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
		"Tests affected: 1",
		"Affected: 2 symbols across",
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
	// First commit: source files matching the indexed paths so the working
	// tree matches the paths sense already knows. The CheckoutService file is
	// padded past the indexed symbol's span (lines 12-85) so the second
	// commit's edit lands *inside* that span — diff-blast now seeds by changed
	// line range, not whole file, so the edit must overlap the symbol.
	for _, rel := range []string{
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
	checkout := filepath.Join(dir, "app/services/checkout_service.rb")
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v1 := checkoutServiceBody("original")
	if err := os.WriteFile(checkout, []byte(v1), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", ".")
	run("commit", "--quiet", "-m", "v1")

	// Second commit: edit a line within the CheckoutService span (12-85) so
	// HEAD~1..HEAD shows the symbol's body as changed.
	if err := os.WriteFile(checkout, []byte(checkoutServiceBody("edited")), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", checkout)
	run("commit", "--quiet", "-m", "v2")

	return dir, "HEAD~1"
}

// checkoutServiceBody builds a 90-line file whose body covers the indexed
// CheckoutService span (lines 12-85). The marker is placed at line 40, well
// inside the span, so editing it produces a hunk that overlaps the symbol.
func checkoutServiceBody(marker string) string {
	var b strings.Builder
	for i := 1; i <= 90; i++ {
		if i == 40 {
			fmt.Fprintf(&b, "  # body %d (%s)\n", i, marker)
			continue
		}
		fmt.Fprintf(&b, "  # body %d\n", i)
	}
	return b.String()
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
	if !strings.Contains(stdout.String(), "Affected: 0 symbols across 0 files") {
		t.Errorf("expected 'Affected: 0 symbols across 0 files', got:\n%s", stdout.String())
	}
}

// TestRunBlastDiffBadRefErrors verifies the diff form surfaces
// git's complaint when the ref doesn't resolve. Uses the tempdir
// from seedBlastProject which is not a git repo — so `git diff`
// fails at the "not a git repository" level, exercising the
// GitDiffHunks error path.
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

// TestRunBlastParseErrorExit1 drives the non-help parse-error return:
// an out-of-range --min-confidence is rejected by parseBlastArgs and
// RunBlast maps it to exit 1.
func TestRunBlastParseErrorExit1(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--min-confidence", "5", "Foo"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "min-confidence") {
		t.Errorf("expected --min-confidence complaint, got: %s", stderr.String())
	}
}

// TestRunBlastDiffMissingIndexExit3 covers the diff form's
// index-open error path: a real git repo (so GitDiffHunks succeeds)
// whose .sense index has been removed maps to exit 3.
func TestRunBlastDiffMissingIndexExit3(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	if err := os.RemoveAll(filepath.Join(dir, ".sense")); err != nil {
		t.Fatalf("rm .sense: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", ref},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("exit = %d, want %d (ExitIndexMissing)", code, ExitIndexMissing)
	}
}

// seedAmbiguousBlastProject creates a project with two symbols
// sharing the same name but in different files.
func seedAmbiguousBlastProject(t *testing.T) string {
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
		{Path: "app/services/user_service.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()},
		{Path: "app/models/user.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()},
	}
	fids := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fids[i] = id
	}

	// Two symbols both named "Handler" but with different qualified names
	syms := []model.Symbol{
		{FileID: fids[0], Name: "Handler", Qualified: "A::Handler", Kind: "class", LineStart: 1, LineEnd: 10},
		{FileID: fids[1], Name: "Handler", Qualified: "B::Handler", Kind: "class", LineStart: 1, LineEnd: 10},
	}
	for i := range syms {
		if _, werr := adapter.WriteSymbol(ctx, &syms[i]); werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
	}

	return dir
}

func TestRunBlastAmbiguousSymbol(t *testing.T) {
	dir := seedAmbiguousBlastProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"Handler"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSymbolIssue {
		t.Errorf("exit = %d, want %d", code, ExitSymbolIssue)
	}
	if !strings.Contains(stderr.String(), "Multiple symbols match") {
		t.Errorf("expected disambiguation message, got: %s", stderr.String())
	}
}

// TestRunBlastSymbolLookupQueryError drives the symbol form's lookup
// failure path: dropping sense_symbols.line_start (a non-indexed,
// non-FTS column the resolver selects) makes the lookup query fail
// with "no such column", which RunBlast maps to the general-error exit
// with a "sense blast:" diagnostic.
func TestRunBlastSymbolLookupQueryError(t *testing.T) {
	dir := seedBlastProject(t)
	degradeIndexColumn(t, dir, "sense_symbols", "line_start")
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense blast:") {
		t.Errorf("expected 'sense blast:' diagnostic, got: %s", stderr.String())
	}
}

// TestRunBlastSymbolComputeError drives the blast.Compute failure
// path: lookup and sibling resolution read only sense_symbols/files
// (left intact), but the BFS reads sense_edges.confidence — dropped
// here — so the frontier-expansion query fails inside Compute.
func TestRunBlastSymbolComputeError(t *testing.T) {
	dir := seedBlastProject(t)
	degradeIndexColumn(t, dir, "sense_edges", "confidence")
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense blast:") {
		t.Errorf("expected 'sense blast:' diagnostic, got: %s", stderr.String())
	}
}

// TestRunBlastDiffSymbolsInChangedLinesError drives the diff form's
// first index query failure: GitDiffHunks succeeds against the real
// repo, OpenIndex opens the degraded index cleanly, then
// SymbolsInChangedLines fails on the dropped sense_symbols.line_end.
func TestRunBlastDiffSymbolsInChangedLinesError(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	degradeIndexColumn(t, dir, "sense_symbols", "line_end")
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", ref},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense blast:") {
		t.Errorf("expected 'sense blast:' diagnostic, got: %s", stderr.String())
	}
}

// TestRunBlastDiffComputeError drives the diff form's per-symbol
// blast.Compute failure: SymbolsInChangedLines resolves the changed
// CheckoutService span against intact sense_symbols/files, then the
// per-symbol BFS hits the dropped sense_edges.confidence and errors.
func TestRunBlastDiffComputeError(t *testing.T) {
	dir, ref := seedBlastGitProject(t)
	degradeIndexColumn(t, dir, "sense_edges", "confidence")
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"--diff", ref},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense blast:") {
		t.Errorf("expected 'sense blast:' diagnostic, got: %s", stderr.String())
	}
}
