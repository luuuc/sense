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
	if resp.SenseMetrics.SymbolsSearched < 2 {
		t.Errorf("expected at least 2 symbols searched, got %d", resp.SenseMetrics.SymbolsSearched)
	}
	if resp.SenseMetrics.EstimatedFileReadsAvoided == 0 {
		t.Error("expected non-zero estimated_file_reads_avoided")
	}
	if resp.SenseMetrics.EstimatedTokensSaved != resp.SenseMetrics.EstimatedFileReadsAvoided*mcpio.AvgTokensPerFile {
		t.Errorf("expected estimated_tokens_saved = files * %d, got %d",
			mcpio.AvgTokensPerFile, resp.SenseMetrics.EstimatedTokensSaved)
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

