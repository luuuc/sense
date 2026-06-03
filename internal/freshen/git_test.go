package freshen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runGit runs a git command in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initGitRepo creates a git repo at dir with deterministic identity.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
}

func TestGitHeadNonRepo(t *testing.T) {
	if gitHead(t.TempDir()) != "" {
		t.Error("gitHead should be empty for a non-git directory")
	}
}

func TestNewGitHeadWatcherNonRepo(t *testing.T) {
	if _, err := newGitHeadWatcher(t.TempDir()); err == nil {
		t.Error("newGitHeadWatcher should fail for a non-git directory")
	}
}

// TestNewGitHeadWatcherAddFails drives the fsnotify-Add failure branch: .git
// exists as a directory (so the stat passes) but cannot be watched because it
// is unreadable. Root-guarded — root ignores the permission bits.
func TestNewGitHeadWatcherAddFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission bits are ignored when running as root")
	}
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gitDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(gitDir, 0o755) })

	w, err := newGitHeadWatcher(root)
	if w != nil {
		_ = w.Close()
	}
	if err == nil {
		t.Skip("this platform allowed a watch on an unreadable .git; nothing to assert")
	}
}

func TestGitDiffNamesSameCommit(t *testing.T) {
	c, r := gitDiffNames(t.TempDir(), "abc", "abc")
	if c != nil || r != nil {
		t.Errorf("same-commit diff should be empty, got changed=%v removed=%v", c, r)
	}
	c, r = gitDiffNames(t.TempDir(), "", "abc")
	if c != nil || r != nil {
		t.Errorf("empty-from diff should be empty, got changed=%v removed=%v", c, r)
	}
}

func TestGitDiffNamesModifyDeleteAdd(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep.go", "package p\n\nfunc Keep() {}\n")
	write("gone.go", "package p\n\nfunc Gone() {}\n")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	base := gitHead(dir)

	write("keep.go", "package p\n\nfunc Keep() {}\n\nfunc Added() {}\n")
	if err := os.Remove(filepath.Join(dir, "gone.go")); err != nil {
		t.Fatal(err)
	}
	write("new.go", "package p\n\nfunc New() {}\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "change")
	head := gitHead(dir)

	changed, removed := gitDiffNames(dir, base, head)
	has := func(list []string, want string) bool {
		for _, p := range list {
			if p == want {
				return true
			}
		}
		return false
	}
	if !has(changed, "keep.go") || !has(changed, "new.go") {
		t.Errorf("changed should include keep.go and new.go, got %v", changed)
	}
	if !has(removed, "gone.go") {
		t.Errorf("removed should include gone.go, got %v", removed)
	}
}

func TestGitDiffNamesRename(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "old.go"),
		[]byte("package p\n\nfunc Stable() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	base := gitHead(dir)

	runGit(t, dir, "mv", "old.go", "new.go")
	runGit(t, dir, "commit", "-q", "-m", "rename")
	head := gitHead(dir)

	changed, removed := gitDiffNames(dir, base, head)
	hasOld, hasNew := false, false
	for _, p := range removed {
		if p == "old.go" {
			hasOld = true
		}
	}
	for _, p := range changed {
		if p == "new.go" {
			hasNew = true
		}
	}
	if !hasOld {
		t.Errorf("rename should mark old.go removed, got removed=%v", removed)
	}
	if !hasNew {
		t.Errorf("rename should mark new.go changed, got changed=%v", changed)
	}
}

func TestParseDiffNameStatus(t *testing.T) {
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	// Modify + add + delete + rename, NUL-separated.
	out := []byte("M\x00keep.go\x00A\x00new.go\x00D\x00gone.go\x00R100\x00old.go\x00moved.go\x00")
	changed, removed := parseDiffNameStatus(out)
	if !eq(changed, "keep.go", "new.go", "moved.go") {
		t.Errorf("changed = %v", changed)
	}
	if !eq(removed, "gone.go", "old.go") {
		t.Errorf("removed = %v", removed)
	}

	// Truncated entries must not panic and are ignored.
	if c, r := parseDiffNameStatus([]byte("M")); c != nil || r != nil {
		t.Errorf("truncated modify: changed=%v removed=%v", c, r)
	}
	if c, r := parseDiffNameStatus([]byte("D")); c != nil || r != nil {
		t.Errorf("truncated delete: changed=%v removed=%v", c, r)
	}
	if c, r := parseDiffNameStatus([]byte("R100\x00old.go")); c != nil || r != nil {
		t.Errorf("truncated rename: changed=%v removed=%v", c, r)
	}
	if c, r := parseDiffNameStatus(nil); c != nil || r != nil {
		t.Errorf("empty: changed=%v removed=%v", c, r)
	}
}

func TestGitDiffNamesBadRefs(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")

	// Two refs that do not exist: git diff fails, so we return no changes.
	c, r := gitDiffNames(dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "cafebabecafebabecafebabecafebabecafebabe")
	if c != nil || r != nil {
		t.Errorf("bad refs should yield no changes, got changed=%v removed=%v", c, r)
	}
}

// TestReconcileGitHeadEmptyHead covers the non-git branch: reconcile is a
// no-op when HEAD cannot be resolved.
func TestReconcileGitHeadEmptyHead(t *testing.T) {
	dir := t.TempDir() // not a git repo
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	svc, err := NewService(Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// gitHead is "" for a non-git dir, so reconcile returns immediately.
	svc.reconcileGitHead(context.Background())
}

// TestReconcileGitHeadNoop covers the unchanged-HEAD branch: when HEAD has
// not moved since the last reconcile, nothing is re-indexed.
func TestReconcileGitHeadNoop(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	scanRepo(t, dir, false)

	svc, err := NewService(Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// Start recorded lastHead = current HEAD; an unchanged HEAD is a no-op.
	svc.reconcileGitHead(context.Background())
	if !svc.Writing() {
		t.Error("service should still be the writer")
	}
}

// TestReconcileGitHeadEmptyDiff covers the HEAD-moved-but-no-files-changed
// case: an empty commit moves HEAD, but the diff is empty, so nothing is
// re-indexed.
func TestReconcileGitHeadEmptyDiff(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	base := gitHead(dir)

	// Empty commit: HEAD moves, working tree unchanged.
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "empty")

	// Drive reconcile directly (no watcher goroutine, no race): lastHead is
	// the base commit, HEAD is the empty commit, so the diff is empty.
	svc := &Service{root: dir, lastHead: base}
	svc.reconcileGitHead(context.Background())
	if svc.lastHead == base {
		t.Error("reconcile should advance lastHead even when the diff is empty")
	}
}

// TestRunGitHeadExitsOnWatcherClose covers the closed-Events exit path:
// closing the underlying watcher ends the git goroutine.
func TestRunGitHeadExitsOnWatcherClose(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	scanRepo(t, dir, false)

	svc, err := NewService(Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// Closing the watcher closes its channels; the git goroutine must exit
	// via the closed-Events branch.
	if svc.gitFsw != nil {
		_ = svc.gitFsw.Close()
	}
	time.Sleep(150 * time.Millisecond)
}

// TestGitFastPathReindexesOnBranchSwitch is the card's outcome: with the
// general watcher idle, a branch switch is re-indexed solely from the git
// diff on .git/HEAD.
func TestGitFastPathReindexesOnBranchSwitch(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	mainFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainFile, []byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "base")
	mainBranch := runGit(t, dir, "rev-parse", "--abbrev-ref", "HEAD")

	// feature branch adds Goodbye.
	runGit(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(mainFile, []byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "add goodbye")

	// Back to main, then scan: index has Hello, not Goodbye.
	runGit(t, dir, "checkout", "-q", mainBranch)
	scanRepo(t, dir, false)

	// Idle general watcher → only the git fast path can re-index.
	svc, err := NewService(Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()
	if !svc.Writing() {
		t.Fatal("service should own the writer role")
	}

	if symbolExists(t, dir, "Goodbye") {
		t.Fatal("Goodbye should be absent before the branch switch")
	}

	// Switch branches: rewrites .git/HEAD and the working tree.
	runGit(t, dir, "checkout", "-q", "feature")

	deadline := time.After(8 * time.Second)
	for {
		if symbolExists(t, dir, "Goodbye") {
			return // git fast path re-indexed from the diff
		}
		select {
		case <-deadline:
			t.Fatal("timed out: git fast path did not re-index after branch switch")
		case <-time.After(150 * time.Millisecond):
		}
	}
}
