package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedCaptureProject reproduces the gitea Issue collision (G-9): a frontend
// TypeScript type whose qualified name IS its bare name, shadowing same-named
// backend symbols. Returns a project dir with .sense/index.db.
func seedCaptureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	files := []model.File{
		{Path: "web_src/js/types.ts", Language: "typescript", Hash: "a", IndexedAt: time.Now()},
		{Path: "models/issues/issue.go", Language: "go", Hash: "b", IndexedAt: time.Now()},
		{Path: "modules/migration/issue.go", Language: "go", Hash: "c", IndexedAt: time.Now()},
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
		{FileID: fids[0], Name: "Issue", Qualified: "Issue", Kind: "type", LineStart: 38, LineEnd: 55},
		{FileID: fids[1], Name: "Issue", Qualified: "issues.Issue", Kind: "class", LineStart: 54, LineEnd: 120},
		{FileID: fids[2], Name: "Issue", Qualified: "migration.Issue", Kind: "class", LineStart: 10, LineEnd: 40},
	}
	for i := range syms {
		if _, werr := adapter.WriteSymbol(ctx, &syms[i]); werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
	}
	return dir
}

// The CLI --file flag must constrain resolution INSIDE the tier cascade (the
// MCP path), not filter after tier 1 already picked the capture. Today the
// post-hoc filter empties tier 1's TS match and answers "No symbol matches"
// for a symbol that is exactly where the flag points.
func TestRunBlastFileFlagReachesLowerTier(t *testing.T) {
	dir := seedCaptureProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"Issue", "--file", "models/issues/issue.go", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("issues.Issue")) {
		t.Errorf("stdout should name issues.Issue, got: %s", stdout.String())
	}
}

func TestRunGraphFileFlagReachesLowerTier(t *testing.T) {
	dir := seedCaptureProject(t)
	var stdout, stderr bytes.Buffer
	code := RunGraph([]string{"Issue", "--file", "models/issues/issue.go", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("issues.Issue")) {
		t.Errorf("stdout should name issues.Issue, got: %s", stdout.String())
	}
}

// The --language filter has no in-cascade variant and stays post-hoc; pin
// that it still narrows the (now cascade-resolved) match set.
func TestRunBlastLanguageFlagStillFilters(t *testing.T) {
	dir := seedCaptureProject(t)
	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"issues.Issue", "--language", "go", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}
}

// Killer for the M5 survivor: the pinned symbol is only reachable at the
// SUFFIX tier, so commit 2's bare-name union cannot mask commit 1's
// in-cascade --file wiring. Query has a separator (no merge path at all).
func TestRunBlastFileFlagReachesSuffixTier(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	files := []model.File{
		{Path: "models/issues/issue.go", Language: "go", Hash: "a", IndexedAt: time.Now()},
		{Path: "services/issue.go", Language: "go", Hash: "b", IndexedAt: time.Now()},
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
		{FileID: fids[0], Name: "Issue", Qualified: "issues.Issue", Kind: "class", LineStart: 54, LineEnd: 120},
		{FileID: fids[1], Name: "Issue", Qualified: "services.issues.Issue", Kind: "class", LineStart: 10, LineEnd: 40},
	}
	for i := range syms {
		if _, werr := adapter.WriteSymbol(ctx, &syms[i]); werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
	}
	_ = adapter.Close()

	var stdout, stderr bytes.Buffer
	code := RunBlast([]string{"issues.Issue", "--file", "services/issue.go", "--json"},
		IO{Stdout: &stdout, Stderr: &stderr, Dir: dir})
	if code != ExitSuccess {
		t.Fatalf("exit = %d, want %d; stderr: %s", code, ExitSuccess, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("services.issues.Issue")) {
		t.Errorf("stdout should name services.issues.Issue, got: %s", stdout.String())
	}
}
