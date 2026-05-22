package scan_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// gitInit creates a real git repo at root with deterministic identity.
// Centralises the env-var boilerplate that several tests would otherwise
// duplicate. Returns a runner for `git <args...>` against that repo.
func gitInit(t *testing.T, root string) func(args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")
	return run
}

// TestExtractTemporalCoupling_whenCoChangesBelowThreshold_writesNoEdges
// exercises the "drop pairs below minCoChanges" branch in
// extractTemporalCoupling: two files in different directories that
// co-change only twice (below the threshold of 3) must produce zero
// temporal edges, even though structurally they would qualify.
func TestExtractTemporalCoupling_whenCoChangesBelowThreshold_writesNoEdges(t *testing.T) {
	root := t.TempDir()
	gitCmd := gitInit(t, root)

	pathA := filepath.Join(root, "pkg", "a.go")
	pathB := filepath.Join(root, "lib", "b.go")

	// Only 2 co-changes — below minCoChanges (3).
	for i := 0; i < 2; i++ {
		writeFile(t, pathA, fmt.Sprintf("package pkg\n\n// v%d\nfunc A() {}\n", i))
		writeFile(t, pathB, fmt.Sprintf("package lib\n\n// v%d\nfunc B() {}\n", i))
		gitCmd("add", "-A")
		gitCmd("commit", "-m", fmt.Sprintf("co-change %d", i))
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var edgeCount int
	err = a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE kind = 'temporal'`).Scan(&edgeCount)
	if err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("expected 0 temporal edges below threshold, got %d", edgeCount)
	}
}

// TestExtractTemporalCoupling_whenNoIndexedFiles_returnsEarly
// covers the len(indexedFiles)==0 branch: a git repo with commits whose
// only files are unindexed (e.g. plain text) means temporal coupling
// has nothing to anchor against and must short-circuit cleanly.
func TestExtractTemporalCoupling_whenNoIndexedFiles_returnsEarly(t *testing.T) {
	root := t.TempDir()
	gitCmd := gitInit(t, root)

	// Co-changes between two .txt files — git sees them but the scanner
	// won't extract symbols, so the indexed-file map will be empty.
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(root, "docs", "a.txt"), fmt.Sprintf("v%d\n", i))
		writeFile(t, filepath.Join(root, "notes", "b.txt"), fmt.Sprintf("v%d\n", i))
		gitCmd("add", "-A")
		gitCmd("commit", "-m", fmt.Sprintf("co-change %d", i))
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var edgeCount int
	if err := a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE kind = 'temporal'`).Scan(&edgeCount); err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("expected 0 temporal edges when no files are indexed, got %d", edgeCount)
	}
}

// TestExtractTemporalCoupling_whenRepoHasNoHistory_writesNoEdges
// exercises the early return when parseGitLog returns empty commits
// (a fresh git repo with no commits yet).
func TestExtractTemporalCoupling_whenRepoHasNoHistory_writesNoEdges(t *testing.T) {
	root := t.TempDir()
	gitCmd := gitInit(t, root)
	_ = gitCmd // init only; no commits

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\nfunc A() {}\n")

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var edgeCount int
	if err := a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE kind = 'temporal'`).Scan(&edgeCount); err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if edgeCount != 0 {
		t.Errorf("expected 0 temporal edges with no commits, got %d", edgeCount)
	}
}
