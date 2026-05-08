package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
)

func TestRunBenchmarkSuccess(t *testing.T) {
	dir := t.TempDir()

	// Write a small Go file so scan can build an index
	goFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(goFile, []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Build the index
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     dir,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	t.Run("human output", func(t *testing.T) {
		cio, stdout, stderr := newTestIO()
		cio.Dir = dir
		code := RunBenchmark([]string{"--iterations", "1"}, cio)
		if code != ExitSuccess {
			t.Errorf("benchmark success: exit=%d, want %d\nstderr: %s", code, ExitSuccess, stderr.String())
		}
		// Human output should contain some benchmark metrics
		out := stdout.String()
		if !strings.Contains(out, "Symbol") && !strings.Contains(out, "Query") {
			t.Errorf("expected human benchmark output, got: %s", out)
		}
	})

	t.Run("json output", func(t *testing.T) {
		cio, stdout, stderr := newTestIO()
		cio.Dir = dir
		code := RunBenchmark([]string{"--iterations", "1", "--json"}, cio)
		if code != ExitSuccess {
			t.Errorf("benchmark JSON: exit=%d, want %d\nstderr: %s", code, ExitSuccess, stderr.String())
		}
		var report map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
			t.Errorf("invalid JSON output: %v\noutput: %s", err, stdout.String())
		}
	})
}

func TestRunBenchmarkRunError(t *testing.T) {
	dir := t.TempDir()

	// Create an invalid index file (not a valid SQLite DB)
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "index.db"), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	cio, _, stderr := newTestIO()
	cio.Dir = dir
	code := RunBenchmark(nil, cio)
	if code != ExitGeneralError {
		t.Errorf("benchmark with bad index: exit=%d, want %d", code, ExitGeneralError)
	}
	if !strings.Contains(stderr.String(), "benchmark:") {
		t.Errorf("expected benchmark error in stderr, got: %s", stderr.String())
	}
}
