package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedGraphProject builds a .sense/index.db inside a tempdir seeded
// with a tiny graph: CheckoutService -> {PaymentGateway#charge,
// Order#finalize}, OrdersController#create -> CheckoutService, plus
// one test edge targeting CheckoutService. Returns the tempdir
// (suitable for IO.Dir) and the subject symbol id.
func seedGraphProject(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(senseDir, "index.db")

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	files := []model.File{
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()},
		{Path: "app/services/payment_gateway.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "c", IndexedAt: time.Now()},
		{Path: "app/controllers/orders_controller.rb", Language: "ruby", Hash: "d", IndexedAt: time.Now()},
		{Path: "test/services/checkout_service_test.rb", Language: "ruby", Hash: "e", IndexedAt: time.Now()},
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
		{FileID: fids[1], Name: "charge", Qualified: "PaymentGateway#charge", Kind: "method", LineStart: 5, LineEnd: 20},
		{FileID: fids[2], Name: "finalize", Qualified: "Order#finalize", Kind: "method", LineStart: 3, LineEnd: 8},
		{FileID: fids[3], Name: "create", Qualified: "OrdersController#create", Kind: "method", LineStart: 10, LineEnd: 18},
		{FileID: fids[4], Name: "test_checkout", Qualified: "CheckoutServiceTest#test_checkout", Kind: "method", LineStart: 1, LineEnd: 5},
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
		{SourceID: &sids[0], TargetID: sids[1], Kind: model.EdgeCalls, FileID: fids[0], Confidence: 1.0},
		{SourceID: &sids[0], TargetID: sids[2], Kind: model.EdgeCalls, FileID: fids[0], Confidence: 1.0},
		{SourceID: &sids[3], TargetID: sids[0], Kind: model.EdgeCalls, FileID: fids[3], Confidence: 1.0},
		{SourceID: &sids[4], TargetID: sids[0], Kind: model.EdgeTests, FileID: fids[4], Confidence: 0.8},
	}
	for i := range edges {
		if _, werr := adapter.WriteEdge(ctx, &edges[i]); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}
	return dir
}

func TestRunGraphHumanSuccess(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"CheckoutService  (class)",
		"app/services/checkout_service.rb:12-85",
		"calls     PaymentGateway#charge, Order#finalize",
		"callers   OrdersController#create",
		"tests     test/services/checkout_service_test.rb (0.8)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestRunGraphJSONMatchesSchema(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"App::Services::CheckoutService", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	var parsed struct {
		Symbol struct {
			Qualified string `json:"qualified"`
			Kind      string `json:"kind"`
		} `json:"symbol"`
		Edges struct {
			Calls    []map[string]any `json:"calls"`
			CalledBy []map[string]any `json:"called_by"`
			Tests    []map[string]any `json:"tests"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("json.Unmarshal: %v\noutput:\n%s", err, stdout.String())
	}
	if parsed.Symbol.Qualified != "App::Services::CheckoutService" {
		t.Errorf("qualified = %q", parsed.Symbol.Qualified)
	}
	if len(parsed.Edges.Calls) != 2 {
		t.Errorf("calls = %d, want 2", len(parsed.Edges.Calls))
	}
	if len(parsed.Edges.CalledBy) != 1 {
		t.Errorf("called_by = %d, want 1", len(parsed.Edges.CalledBy))
	}
	if len(parsed.Edges.Tests) != 1 {
		t.Errorf("tests = %d, want 1", len(parsed.Edges.Tests))
	}
}

func TestRunGraphDirectionCallersOnly(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"--direction", "callers", "App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "calls     ") {
		t.Errorf("callers-only output should not contain 'calls' group:\n%s", out)
	}
	if !strings.Contains(out, "callers   OrdersController#create") {
		t.Errorf("callers group missing:\n%s", out)
	}
}

func TestRunGraphNotFoundExit2(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"DoesNotExist"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSymbolIssue {
		t.Errorf("exit = %d, want %d (ExitSymbolIssue)", code, ExitSymbolIssue)
	}
	if !strings.Contains(stderr.String(), "No symbol matches") {
		t.Errorf("expected not-found message, got:\n%s", stderr.String())
	}
}

func TestRunGraphAmbiguousExit2(t *testing.T) {
	// Add a second CheckoutService in a different namespace to
	// create ambiguity on the unqualified name.
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
	f1, _ := adapter.WriteFile(ctx, &model.File{Path: "a.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()})
	f2, _ := adapter.WriteFile(ctx, &model.File{Path: "b.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()})
	_, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: f1, Name: "User", Qualified: "App::User", Kind: "class", LineStart: 1, LineEnd: 5})
	_, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: f2, Name: "User", Qualified: "Admin::User", Kind: "class", LineStart: 1, LineEnd: 5})

	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"User"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSymbolIssue {
		t.Errorf("exit = %d, want %d (ExitSymbolIssue)", code, ExitSymbolIssue)
	}
	if !strings.Contains(stderr.String(), `Multiple symbols match "User"`) {
		t.Errorf("expected disambiguation, got:\n%s", stderr.String())
	}
}

func TestRunGraphDisambiguateByLanguage(t *testing.T) {
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
	f1, _ := adapter.WriteFile(ctx, &model.File{Path: "app/models/project.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()})
	f2, _ := adapter.WriteFile(ctx, &model.File{Path: "src/project.js", Language: "javascript", Hash: "b", IndexedAt: time.Now()})
	_, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: f1, Name: "Project", Qualified: "Project", Kind: "class", LineStart: 1, LineEnd: 10})
	_, _ = adapter.WriteSymbol(ctx, &model.Symbol{FileID: f2, Name: "Project", Qualified: "Project", Kind: "function", LineStart: 1, LineEnd: 5})

	t.Run("ambiguous without filter", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"Project"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSymbolIssue {
			t.Errorf("exit = %d, want %d", code, ExitSymbolIssue)
		}
		if !strings.Contains(stderr.String(), "--language") {
			t.Errorf("hint should mention --language:\n%s", stderr.String())
		}
	})

	t.Run("resolved by --language", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"Project", "--language", "ruby"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Project  (class)") {
			t.Errorf("expected ruby class in output:\n%s", stdout.String())
		}
	})

	t.Run("resolved by --file", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"Project", "--file", "project.js"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit = %d; stderr=%s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Project  (function)") {
			t.Errorf("expected JS function in output:\n%s", stdout.String())
		}
	})
}

// TestRunGraphJSONRoundTripsCanonical proves the CLI's --json output
// IS the canonical mcpio wire shape — not just a shape that happens
// to unmarshal cleanly. A byte-by-byte equality after an unmarshal +
// MarshalGraph round trip catches two classes of regression:
//   - CLI adds extra whitespace / trailing newlines that drift from
//     the mcpio contract
//   - CLI populates a field the builder forgot to normalize
//
// This is the "end-to-end" sibling of the mcpio contract_test.go
// golden — that test pins types→JSON; this one pins CLI→JSON→types→JSON.
func TestRunGraphJSONRoundTripsCanonical(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	if code := RunGraph([]string{"App::Services::CheckoutService", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}); code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	assertOneTrailingNewline(t, stdout.Bytes())
	cliBytes := bytes.TrimRight(stdout.Bytes(), "\n")

	var parsed mcpio.GraphResponse
	if err := json.Unmarshal(cliBytes, &parsed); err != nil {
		t.Fatalf("unmarshal CLI output: %v\n%s", err, cliBytes)
	}
	canonical, err := mcpio.MarshalGraph(parsed)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	if !bytes.Equal(cliBytes, canonical) {
		t.Fatalf("CLI output is not canonical\n--- cli ---\n%s\n--- canonical ---\n%s",
			cliBytes, canonical)
	}
}

// assertOneTrailingNewline pins the CLI's --json contract: the
// output ends with exactly one '\n' so a downstream `| jq` or `|
// head` sees a well-formed line-delimited payload. Zero trailing
// newline (Marshal without Fprintln) or two (double-newline by
// accident) both break shell pipelines in different ways.
func assertOneTrailingNewline(t *testing.T, out []byte) {
	t.Helper()
	if len(out) == 0 || out[len(out)-1] != '\n' {
		t.Fatalf("expected trailing newline, got: %q", tailSnippet(out))
	}
	if bytes.HasSuffix(out, []byte("\n\n")) {
		t.Fatalf("expected exactly one trailing newline, got double: %q", tailSnippet(out))
	}
}

// tailSnippet returns the last up-to-40 bytes of out for error
// messages, so a failure log shows the actual bytes instead of
// "[]byte{...}".
func tailSnippet(out []byte) string {
	if len(out) <= 40 {
		return string(out)
	}
	return "..." + string(out[len(out)-40:])
}

// TestRunGraphFlagOrderInvariant pins that flags can appear before
// or after the positional symbol without changing behavior. Go's
// stdlib flag.Parse stops at the first non-flag arg by default; a
// regression here would silently drop --json or --direction.
func TestRunGraphFlagOrderInvariant(t *testing.T) {
	dir := seedGraphProject(t)

	runCapture := func(args []string) string {
		var stdout, stderr bytes.Buffer
		code := RunGraph(args, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
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

func TestRunGraphDirectionCallees(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"--direction", "callees", "App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "calls     PaymentGateway#charge") {
		t.Errorf("callees mode missing calls group:\n%s", out)
	}
	if strings.Contains(out, "callers   ") {
		t.Errorf("callees mode should not contain callers group:\n%s", out)
	}
	if strings.Contains(out, "tests     ") {
		t.Errorf("callees mode should not contain tests group:\n%s", out)
	}
}

func TestRunGraphDepthExceedsMax(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"--depth", "4", "App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "exceeds maximum") {
		t.Errorf("expected max depth message, got: %s", stderr.String())
	}
}

// TestRunGraphInvalidDirectionExit1 drives the non-help parse-error
// return: an unrecognized --direction value is rejected by
// parseGraphArgs and RunGraph maps it to exit 1.
func TestRunGraphInvalidDirectionExit1(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"--direction", "sideways", "Foo"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: t.TempDir()})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "--direction must be one of") {
		t.Errorf("expected --direction complaint, got: %s", stderr.String())
	}
}

// TestRunGraphDepthBelowOneClamped pins the depth floor: --depth 0 is
// clamped up to 1 rather than rejected, and the query still resolves.
func TestRunGraphDepthBelowOneClamped(t *testing.T) {
	dir := seedGraphProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"--depth", "0", "App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "CheckoutService  (class)") {
		t.Errorf("expected resolved symbol with clamped depth, got:\n%s", stdout.String())
	}
}

func TestRunGraphMissingIndexExit3(t *testing.T) {
	dir := t.TempDir() // no .sense/ inside
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"Anything"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitIndexMissing {
		t.Errorf("exit = %d, want %d (ExitIndexMissing)", code, ExitIndexMissing)
	}
}

// TestRunGraphCorruptIndexExit4 covers two corruption flavors:
// garbage bytes (file clearly not SQLite) and a valid SQLite
// header followed by truncation (file looks SQLite-ish but
// schema apply fails). Both should land on exit code 4 per the
// documented table.
func TestRunGraphCorruptIndexExit4(t *testing.T) {
	tests := []struct {
		name     string
		contents []byte
	}{
		{"garbage bytes", []byte("not a sqlite database, just some garbage bytes")},
		// SQLite files start with "SQLite format 3\x00" (16 bytes).
		// A file carrying only this header and nothing else opens
		// past the signature check and fails on the first read.
		{"valid header, truncated body", []byte("SQLite format 3\x00")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			senseDir := filepath.Join(dir, ".sense")
			if err := os.MkdirAll(senseDir, 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(senseDir, "index.db"), tc.contents, 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			var stdout, stderr bytes.Buffer
			code := RunGraph([]string{"Anything"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
			if code != ExitIndexCorrupt {
				t.Errorf("exit = %d, want %d (ExitIndexCorrupt)", code, ExitIndexCorrupt)
			}
			if !strings.Contains(stderr.String(), "corrupt") {
				t.Errorf("expected 'corrupt' hint in stderr, got: %s", stderr.String())
			}
		})
	}
}

// TestRunGraphUnreadableIndexExit1 pins the permission-vs-corrupt
// split added by the card-9 council trim: an index file that
// exists but cannot be read (mode 0000) must fall through as
// exit 1, not exit 4. Wrong fix: "rebuild the index"; right fix:
// "fix the filesystem permissions." Separate code, separate
// hint.
func TestRunGraphUnreadableIndexExit1(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root — mode bits don't block reads")
	}
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dbPath := filepath.Join(senseDir, "index.db")
	if err := os.WriteFile(dbPath, []byte("whatever"), 0o000); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o644) })

	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"Anything"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Errorf("exit = %d, want %d (ExitGeneralError — permission ≠ corrupt)",
			code, ExitGeneralError)
	}
	if strings.Contains(stderr.String(), "corrupt") {
		t.Errorf("permission error should not be labeled corrupt, got: %s", stderr.String())
	}
}

// TestRunGraphLookupQueryError drives RunGraph's lookup failure path:
// dropping sense_symbols.line_start (a non-indexed, non-FTS column the
// resolver selects) makes the lookup query fail with "no such column",
// mapped to the general-error exit and a "sense graph:" diagnostic.
// degradeIndexColumn models a partially migrated index that OpenIndex
// still opens cleanly.
func TestRunGraphLookupQueryError(t *testing.T) {
	dir := seedGraphProject(t)
	degradeIndexColumn(t, dir, "sense_symbols", "line_start")
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense graph:") {
		t.Errorf("expected 'sense graph:' diagnostic, got: %s", stderr.String())
	}
}

// TestRunGraphReadSymbolGraphError drives RunGraph's graph-read
// failure path: lookup reads only sense_symbols/files (left intact)
// and resolves the subject, but ReadSymbolGraph reads
// sense_edges.confidence — dropped here — so the graph query fails and
// RunGraph returns the general-error exit with a "sense graph:"
// diagnostic.
func TestRunGraphReadSymbolGraphError(t *testing.T) {
	dir := seedGraphProject(t)
	degradeIndexColumn(t, dir, "sense_edges", "confidence")
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"App::Services::CheckoutService"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d (ExitGeneralError)", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "sense graph:") {
		t.Errorf("expected 'sense graph:' diagnostic, got: %s", stderr.String())
	}
}

// seedGraphProjectDeep builds a 3-hop call chain:
//
//	D → C → B → A
//
// where → means "calls". Querying A with direction=callers at depth 1
// returns B, depth 2 returns B+C, depth 3 returns B+C+D.
func seedGraphProjectDeep(t *testing.T) string {
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
		{Path: "a.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()},
		{Path: "b.rb", Language: "ruby", Hash: "b", IndexedAt: time.Now()},
		{Path: "c.rb", Language: "ruby", Hash: "c", IndexedAt: time.Now()},
		{Path: "d.rb", Language: "ruby", Hash: "d", IndexedAt: time.Now()},
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
		{FileID: fids[0], Name: "A", Qualified: "A", Kind: "method", LineStart: 1, LineEnd: 5},
		{FileID: fids[1], Name: "B", Qualified: "B", Kind: "method", LineStart: 1, LineEnd: 5},
		{FileID: fids[2], Name: "C", Qualified: "C", Kind: "method", LineStart: 1, LineEnd: 5},
		{FileID: fids[3], Name: "D", Qualified: "D", Kind: "method", LineStart: 1, LineEnd: 5},
	}
	sids := make([]int64, len(syms))
	for i := range syms {
		id, werr := adapter.WriteSymbol(ctx, &syms[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		sids[i] = id
	}

	// D→C→B→A call chain
	edges := []model.Edge{
		{SourceID: &sids[1], TargetID: sids[0], Kind: model.EdgeCalls, FileID: fids[1], Confidence: 1.0},
		{SourceID: &sids[2], TargetID: sids[1], Kind: model.EdgeCalls, FileID: fids[2], Confidence: 1.0},
		{SourceID: &sids[3], TargetID: sids[2], Kind: model.EdgeCalls, FileID: fids[3], Confidence: 1.0},
	}
	for i := range edges {
		if _, werr := adapter.WriteEdge(ctx, &edges[i]); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}
	return dir
}

func TestRunGraphDepthCallers(t *testing.T) {
	dir := seedGraphProjectDeep(t)

	t.Run("depth=1 returns direct caller only", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--depth", "1", "--direction", "callers", "--json", "A"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var resp mcpio.GraphResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if len(resp.Edges.CalledBy) != 1 || resp.Edges.CalledBy[0].Symbol != "B" {
			t.Errorf("depth=1 called_by = %v, want [B]", resp.Edges.CalledBy)
		}
		if len(resp.Layers) != 0 {
			t.Errorf("depth=1 layers = %d, want 0", len(resp.Layers))
		}
	})

	t.Run("depth=2 returns direct + transitive callers", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--depth", "2", "--direction", "callers", "--json", "A"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var resp mcpio.GraphResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if len(resp.Edges.CalledBy) != 1 || resp.Edges.CalledBy[0].Symbol != "B" {
			t.Errorf("depth=2 edges.called_by = %v, want [B]", resp.Edges.CalledBy)
		}
		if len(resp.Layers) != 1 {
			t.Fatalf("depth=2 layers = %d, want 1", len(resp.Layers))
		}
		if resp.Layers[0].Depth != 2 {
			t.Errorf("layer depth = %d, want 2", resp.Layers[0].Depth)
		}
		if len(resp.Layers[0].Edges.CalledBy) != 1 || resp.Layers[0].Edges.CalledBy[0].Symbol != "C" {
			t.Errorf("depth=2 layer called_by = %v, want [C]", resp.Layers[0].Edges.CalledBy)
		}
	})

	t.Run("depth=3 returns full 3-hop chain", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--depth", "3", "--direction", "callers", "--json", "A"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var resp mcpio.GraphResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if len(resp.Edges.CalledBy) != 1 || resp.Edges.CalledBy[0].Symbol != "B" {
			t.Errorf("depth=3 edges.called_by = %v, want [B]", resp.Edges.CalledBy)
		}
		if len(resp.Layers) != 2 {
			t.Fatalf("depth=3 layers = %d, want 2", len(resp.Layers))
		}
		if resp.Layers[0].Edges.CalledBy[0].Symbol != "C" {
			t.Errorf("depth=3 layer[0] = %v, want C", resp.Layers[0].Edges.CalledBy)
		}
		if resp.Layers[1].Depth != 3 {
			t.Errorf("layer[1] depth = %d, want 3", resp.Layers[1].Depth)
		}
		if len(resp.Layers[1].Edges.CalledBy) != 1 || resp.Layers[1].Edges.CalledBy[0].Symbol != "D" {
			t.Errorf("depth=3 layer[1] = %v, want [D]", resp.Layers[1].Edges.CalledBy)
		}
	})

	t.Run("depth=2 callees direction", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--depth", "2", "--direction", "callees", "--json", "D"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var resp mcpio.GraphResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		if len(resp.Edges.Calls) != 1 || resp.Edges.Calls[0].Symbol != "C" {
			t.Errorf("depth=2 callees calls = %v, want [C]", resp.Edges.Calls)
		}
		if len(resp.Layers) != 1 {
			t.Fatalf("depth=2 callees layers = %d, want 1", len(resp.Layers))
		}
		if len(resp.Layers[0].Edges.Calls) != 1 || resp.Layers[0].Edges.Calls[0].Symbol != "B" {
			t.Errorf("depth=2 callees layer = %v, want [B]", resp.Layers[0].Edges.Calls)
		}
	})

	t.Run("deduplication across hops", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--depth", "3", "--direction", "callees", "--json", "D"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var resp mcpio.GraphResponse
		if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
			t.Fatalf("json: %v", err)
		}
		seen := map[string]bool{}
		for _, e := range resp.Edges.Calls {
			if seen[e.Symbol] {
				t.Errorf("duplicate in edges: %s", e.Symbol)
			}
			seen[e.Symbol] = true
		}
		for _, layer := range resp.Layers {
			for _, e := range layer.Edges.Calls {
				if seen[e.Symbol] {
					t.Errorf("duplicate across layers: %s", e.Symbol)
				}
				seen[e.Symbol] = true
			}
		}
	})
}
