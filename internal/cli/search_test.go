package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestRunSearch_whenEmbedderInitFails_returnsError(t *testing.T) {
	// Embeddings are present, so BuildEngine creates the bundled embedder.
	// Pointing the ORT cache at a regular file makes library extraction fail,
	// driving RunSearch's BuildEngine error branch.
	dir := seedSearchProjectWithEmbeddings(t)
	t.Setenv("SENSE_EMBEDDINGS", "true")
	badCache := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(badCache, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed bad cache: %v", err)
	}
	t.Setenv("SENSE_CACHE_DIR", badCache)

	var stdout, stderr bytes.Buffer
	code := RunSearch([]string{"payment", "--json"}, IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitGeneralError {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitGeneralError, stderr.String())
	}
	if !strings.Contains(stderr.String(), "sense search:") {
		t.Errorf("expected error message on stderr, got: %s", stderr.String())
	}
}

func seedSearchProject(t *testing.T) string {
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

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/services/payment.rb", Language: "ruby",
		Hash: "a1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, sym := range []model.Symbol{
		{FileID: fid, Name: "ProcessPayment", Qualified: "PaymentService#process_payment",
			Kind: "method", LineStart: 10, LineEnd: 25,
			Docstring: "processes payment transactions"},
		{FileID: fid, Name: "RefundPayment", Qualified: "PaymentService#refund_payment",
			Kind: "method", LineStart: 30, LineEnd: 40,
			Docstring: "refunds a payment"},
	} {
		if _, err := adapter.WriteSymbol(ctx, &sym); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func TestParseSearchArgsMode(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		want    string
	}{
		{"default is hybrid", []string{"query"}, false, search.ModeHybrid},
		{"explicit hybrid", []string{"--mode", "hybrid", "query"}, false, search.ModeHybrid},
		{"semantic", []string{"--mode", "semantic", "query"}, false, search.ModeSemantic},
		{"keyword", []string{"--mode", "keyword", "query"}, false, search.ModeKeyword},
		{"invalid mode rejected", []string{"--mode", "fuzzy", "query"}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			opts, err := parseSearchArgs(tt.args, &stderr)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for args %v, got none", tt.args)
				}
				if !strings.Contains(stderr.String(), "invalid --mode") {
					t.Errorf("expected invalid-mode message, got: %s", stderr.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.Mode != tt.want {
				t.Errorf("Mode = %q, want %q", opts.Mode, tt.want)
			}
		})
	}
}

// TestRunSearchModeKeyword is a plumbing smoke test: it confirms --mode is
// accepted and reaches the engine without error. The actual mode→ranking
// behavior is pinned in search.TestSearchModeOverridesShape (resolved
// weights per mode); this test is its complement, not the behavioral gate.
func TestRunSearchModeKeyword(t *testing.T) {
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"--mode", "keyword", "payment"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}
	if stdout.String() == "" {
		t.Fatal("expected output, got empty")
	}
}

func TestRunSearchInvalidMode(t *testing.T) {
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"--mode", "nonsense", "payment"}, cio)
	if code != ExitGeneralError {
		t.Fatalf("exit code = %d, want %d", code, ExitGeneralError)
	}
}

func TestRunSearchKeywordFallback(t *testing.T) {
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"payment"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}

	got := stdout.String()
	if got == "" {
		t.Fatal("expected output, got empty")
	}
	if !strings.Contains(got, "process_payment") && !strings.Contains(got, "ProcessPayment") {
		t.Errorf("expected payment-related results, got:\n%s", got)
	}
}

func TestRunSearchKeywordFallbackJSON(t *testing.T) {
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"payment", "--json"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}

	var resp mcpio.SearchResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}

	if len(resp.Results) == 0 {
		t.Fatal("expected results, got none")
	}
	if strings.Contains(stdout.String(), "sense_metrics") {
		t.Error("JSON output should not contain sense_metrics")
	}
}

func TestRunSearchMissingIndex(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"payment"}, cio)
	if code != ExitIndexMissing {
		t.Fatalf("exit code = %d, want %d", code, ExitIndexMissing)
	}
}

// seedSearchProjectWithEmbeddings creates a project with two symbols whose
// embedding vectors are also written. The first symbol's vector is biased
// toward dim 0, the second toward dim 1 — enough for cosine-distance
// ranking to be deterministic without needing the real model.
func seedSearchProjectWithEmbeddings(t *testing.T) string {
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

	fid, err := adapter.WriteFile(ctx, &model.File{
		Path: "app/services/payment.rb", Language: "ruby",
		Hash: "a1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	symA := model.Symbol{
		FileID: fid, Name: "ProcessPayment", Qualified: "PaymentService#process_payment",
		Kind: "method", LineStart: 10, LineEnd: 25,
		Docstring: "processes payment transactions",
	}
	symB := model.Symbol{
		FileID: fid, Name: "RefundPayment", Qualified: "PaymentService#refund_payment",
		Kind: "method", LineStart: 30, LineEnd: 40,
		Docstring: "refunds a payment",
	}

	idA, err := adapter.WriteSymbol(ctx, &symA)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := adapter.WriteSymbol(ctx, &symB)
	if err != nil {
		t.Fatal(err)
	}

	// Two distinct unit vectors at the expected embedding dimension.
	vecA := make([]float32, embed.Dimensions)
	vecB := make([]float32, embed.Dimensions)
	vecA[0] = 1
	vecB[1] = 1

	if err := adapter.WriteEmbedding(ctx, idA, vecToBlob(vecA)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.WriteEmbedding(ctx, idB, vecToBlob(vecB)); err != nil {
		t.Fatal(err)
	}

	return dir
}

func vecToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func TestRunSearch_whenEmbeddingsAvailable_usesVectorIndex(t *testing.T) {
	// Skip if the bundled ORT / model are not present in this build.
	// Mirrors the guard in internal/embed/bundle_test.go.
	probe, err := embed.NewBundledEmbedder(0)
	if err != nil {
		t.Skipf("bundled embedder unavailable in this build: %v", err)
	}
	_ = probe.Close()

	dir := seedSearchProjectWithEmbeddings(t)
	t.Setenv("SENSE_EMBEDDINGS", "true")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"payment", "--json"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}

	var resp mcpio.SearchResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	// Vector branch should report a non-keyword-only search mode.
	if !strings.Contains(resp.SearchMode, "hybrid") && !strings.Contains(resp.SearchMode, "vector") {
		t.Errorf("expected hybrid/vector search mode, got %q", resp.SearchMode)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results, got none")
	}
}

func TestRunSearch_whenEmbeddingsEnabledButNoVectors_fallsBackToKeyword(t *testing.T) {
	// Embeddings flag stays enabled (the default) but no embeddings have
	// been written to the index. RunSearch must fall back to keyword-only
	// search without errors.
	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "true")

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"payment", "--json"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}

	var resp mcpio.SearchResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	// No vectors present → mode should be keyword (or contain "keyword"),
	// never "hybrid"/"vector".
	if strings.Contains(resp.SearchMode, "vector") {
		t.Errorf("expected keyword fallback, got mode %q", resp.SearchMode)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected keyword results, got none")
	}
}

func TestRunSearch_whenStructuralResultsBelowLimit_mergesTextFallback(t *testing.T) {
	// TestRunSearchKeywordFallback exercises the keyword path but doesn't
	// actually fire the text fallback because there are no source files
	// on disk for ripgrep to scan. Here we drop a real source file with
	// a unique token that the SQL keyword index won't surface, forcing
	// the text-fallback merge branch (lines 134-142) to fire.
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed; text fallback would no-op")
	}

	dir := seedSearchProject(t)
	t.Setenv("SENSE_EMBEDDINGS", "false")

	// Write a source file whose only match for the query is on a comment
	// line — outside the indexed symbols. The structural index returns
	// nothing for "ZebraTokenXYZ" but rg finds it.
	src := "// ZebraTokenXYZ — sentinel for text-fallback test\nfunc Noop() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{"ZebraTokenXYZ", "--json"}, cio)
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}

	var resp mcpio.SearchResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v\nraw: %s", err, stdout.String())
	}
	// SenseMetrics is marshaled with json:"-" so we verify via observable
	// state: the mode is suffixed and at least one result carries the
	// "text" source tag.
	if !strings.HasSuffix(resp.SearchMode, "+text") {
		t.Errorf("expected mode to end with +text, got %q", resp.SearchMode)
	}
	var sawText bool
	for _, r := range resp.Results {
		if r.Source == "text" {
			sawText = true
			break
		}
	}
	if !sawText {
		t.Errorf("expected at least one text-sourced result, JSON:\n%s", stdout.String())
	}
}

func TestRunSearchEmptyQuery(t *testing.T) {
	dir := seedSearchProject(t)
	var stdout, stderr bytes.Buffer
	cio := IO{Stdout: &stdout, Stderr: &stderr, Dir: dir}

	code := RunSearch([]string{}, cio)
	if code != ExitGeneralError {
		t.Fatalf("exit code = %d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "missing query") {
		t.Errorf("expected 'missing query' in stderr, got: %s", stderr.String())
	}
}
