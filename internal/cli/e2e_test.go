package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
)

// TestE2EGraphAndBlastOnSenseRepo is the pitch's acceptance gate:
// after a full scan of the Sense repo itself, `sense graph`,
// `sense graph --direction callers`, and `sense blast --json`
// all produce non-empty output for a known-real symbol.
//
// The pitch text uses "Scanner" as a placeholder; no such symbol
// exists in the Sense codebase. `extract.Register` stands in
// because every language extractor's init() function calls it —
// the Tier-Basic extractors (ruby, python, tsjs, golang, rust)
// guarantee the symbol has multiple direct callers even on a
// repo with no test suite. Same pattern as
// internal/blast/e2e_test.go.
//
// The test scans into a tempdir so the repo's working tree stays
// clean. repoRoot is resolved relative to this test file's
// location: internal/cli/... → `../..` is the module root.
func TestE2EGraphAndBlastOnSenseRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E: scan-and-query takes ~200ms; run without -short")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	// Scan into <projectDir>/.sense/ directly so OpenIndex
	// (<projectDir>/.sense/index.db) finds it without symlinks.
	projectDir := t.TempDir()
	senseSubdir := filepath.Join(projectDir, ".sense")
	ctx := context.Background()
	res, err := scan.Run(ctx, scan.Options{
		Root:     repoRoot,
		Sense:    senseSubdir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("scan.Run: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatal("no symbols indexed — scan didn't walk the repo")
	}
	t.Logf("scanned: %d files, %d indexed, %d symbols, %d edges",
		res.Files, res.Indexed, res.Symbols, res.Edges)

	t.Run("graph human output", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"extract.Register"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: projectDir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		out := stdout.String()
		if out == "" {
			t.Fatal("graph output empty")
		}
		// Human render uses Symbol.Name not Qualified (matching the
		// pitch example "CheckoutService  (class)"), so check for
		// the bare name plus the expected file path.
		for _, want := range []string{"Register  (function)", "internal/extract"} {
			if !strings.Contains(out, want) {
				t.Errorf("graph output missing %q\ngot:\n%s", want, out)
			}
		}
	})

	t.Run("graph --direction callers", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunGraph([]string{"--direction", "callers", "extract.Register"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: projectDir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		out := stdout.String()
		if !strings.Contains(out, "callers") {
			t.Errorf("--direction callers output missing 'callers' section:\n%s", out)
		}
		if strings.Contains(out, "calls     ") {
			t.Errorf("--direction callers output should not contain 'calls' section:\n%s", out)
		}
	})

	t.Run("blast --json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := RunBlast([]string{"extract.Register", "--json"},
			IO{Stdout: &stdout, Stderr: &stderr, Dir: projectDir})
		if code != ExitSuccess {
			t.Fatalf("exit=%d stderr=%s", code, stderr.String())
		}
		var parsed struct {
			Symbol        string           `json:"symbol"`
			Risk          string           `json:"risk"`
			DirectCallers []map[string]any `json:"direct_callers"`
			TotalAffected int              `json:"total_affected"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &parsed); err != nil {
			t.Fatalf("unmarshal: %v\n%s", err, stdout.String())
		}
		if parsed.Symbol == "" {
			t.Error("symbol field empty")
		}
		switch parsed.Risk {
		case "low", "medium", "high":
		default:
			t.Errorf("risk = %q, want low/medium/high", parsed.Risk)
		}
		// Floor is 5, not 1: the five Tier-Basic extractors
		// (ruby, python, tsjs, golang, rust) each call Register
		// from their init(). A regression that drops one init()
		// wiring would leave direct_callers non-empty but short,
		// and the test would miss it with a "> 0" check.
		const minDirectCallers = 5
		if len(parsed.DirectCallers) < minDirectCallers {
			t.Errorf("direct_callers = %d, want >= %d (one per registered extractor)",
				len(parsed.DirectCallers), minDirectCallers)
		}
		if parsed.TotalAffected == 0 {
			t.Error("total_affected == 0")
		}
		t.Logf("blast extract.Register: risk=%s direct=%d total=%d",
			parsed.Risk, len(parsed.DirectCallers), parsed.TotalAffected)
	})
}
