package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "."},
		{"commit", "-m", "initial"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s (%v)", args, out, err)
		}
	}
	return dir
}

func TestGitDiffFilesValidRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)
	paths, err := GitDiffFiles(context.Background(), dir, "HEAD~0")
	if err != nil {
		t.Fatalf("GitDiffFiles: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("expected no diff for HEAD~0, got %v", paths)
	}
}

func TestGitDiffFilesBadRefReturnsError(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)
	_, err := GitDiffFiles(context.Background(), dir, "nonexistent-ref-abc123")
	if err == nil {
		t.Fatal("expected error for bad ref")
	}
}

func TestGitDiffFilesFlagInjectionBlocked(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := initGitRepo(t)

	// These payloads would be dangerous without --end-of-options.
	// With it, git interprets them as literal ref names (which don't
	// exist) and returns an error — not a flag-injection side effect.
	payloads := []string{
		"--upload-pack=evil",
		"--output=/tmp/evil",
		"-p",
		"--stat",
		"--no-index",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			_, err := GitDiffFiles(context.Background(), dir, p)
			if err == nil {
				t.Errorf("flag-like ref %q should error (bad revision), not be treated as a flag", p)
			}
		})
	}
}
