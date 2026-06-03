package freshen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestWriterLockExclusive(t *testing.T) {
	dir := t.TempDir()

	l1, ok1, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if !ok1 {
		t.Fatal("first acquire should succeed")
	}

	// A second acquire while the first is held must fail (live owner).
	_, ok2, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if ok2 {
		t.Fatal("second acquire should fail while the lock is held")
	}

	// After release, the lock is free again.
	l1.release()
	l3, ok3, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("third acquire: %v", err)
	}
	if !ok3 {
		t.Fatal("acquire after release should succeed")
	}
	l3.release()
}

func TestWriterLockReclaimsDeadPid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)

	// A live-but-unrelated process gives us a real pid; once it exits and
	// is reaped, that pid is dead, so the lock is reclaimable.
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	deadPid := cmd.Process.Pid
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()

	if err := os.WriteFile(path, []byte(strconv.Itoa(deadPid)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, ok, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("acquire over dead-pid lock: %v", err)
	}
	if !ok {
		t.Fatal("should reclaim a lock owned by a dead pid")
	}
	if l.pid != os.Getpid() {
		t.Errorf("reclaimed lock pid = %d, want %d", l.pid, os.Getpid())
	}
	l.release()
}

func TestWriterLockReclaimsStaleByAge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)

	// Own pid is alive, but the heartbeat (mtime) is ancient: this is the
	// pid-reuse backstop, reclaimable by age alone.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStaleAfter)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	l, ok, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("acquire over stale lock: %v", err)
	}
	if !ok {
		t.Fatal("should reclaim a lock whose heartbeat is stale")
	}
	l.release()
}

func TestWriterLockReclaimsCorruptContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)

	// Fresh mtime but unparseable content (e.g. a half-written lock).
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o644); err != nil {
		t.Fatal(err)
	}

	l, ok, err := acquireWriterLock(dir)
	if err != nil {
		t.Fatalf("acquire over corrupt lock: %v", err)
	}
	if !ok {
		t.Fatal("should reclaim a lock with corrupt content")
	}
	l.release()
}

func TestWriterLockReleaseIdempotent(t *testing.T) {
	dir := t.TempDir()
	l, ok, err := acquireWriterLock(dir)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	l.release()
	l.release() // second release must not panic
	var nilLock *writerLock
	nilLock.release() // nil receiver must not panic
}

func TestWriterLockReleaseLeavesReclaimersLock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lockFileName)

	l, ok, err := acquireWriterLock(dir)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}

	// Simulate a reclaimer overwriting the lock with a different owner.
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid()+1)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.release()

	// release must NOT delete a lock it no longer owns.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("release deleted a lock owned by someone else: %v", err)
	}
	_ = os.Remove(path)
}

func TestWriterLockHeartbeatRefreshesMtime(t *testing.T) {
	// Shrink the heartbeat so the test does not wait seconds.
	origInterval := lockHeartbeatInterval
	lockHeartbeatInterval = 20 * time.Millisecond
	defer func() { lockHeartbeatInterval = origInterval }()

	dir := t.TempDir()
	l, ok, err := acquireWriterLock(dir)
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	defer l.release()

	info0, err := os.Stat(l.path)
	if err != nil {
		t.Fatal(err)
	}
	// Wait for several heartbeat ticks.
	time.Sleep(120 * time.Millisecond)
	info1, err := os.Stat(l.path)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().After(info0.ModTime()) {
		t.Error("heartbeat should have advanced the lock file mtime")
	}
}

func TestWriterLockReclaimRemoveFails(t *testing.T) {
	dir := t.TempDir()
	// Make the lock path a non-empty directory: it looks like an existing
	// lock (O_EXCL create fails), reads as corrupt (stale), but os.Remove
	// fails because the directory is not empty. The bounded loop exhausts
	// and surfaces the error rather than spinning.
	lockDir := filepath.Join(dir, lockFileName)
	if err := os.MkdirAll(filepath.Join(lockDir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	l, ok, err := acquireWriterLock(dir)
	if ok {
		l.release()
		t.Fatal("should not acquire when the lock path is an undeletable directory")
	}
	if err == nil {
		t.Fatal("expected an error when the stale lock cannot be removed")
	}
}

// TestLockIsStaleMissingFile covers the stat-error branch: a lock path that
// does not exist is treated as reclaimable (it vanished between a create
// attempt and the stat).
func TestLockIsStaleMissingFile(t *testing.T) {
	if !lockIsStale(filepath.Join(t.TempDir(), "does-not-exist.lock")) {
		t.Error("a missing lock file should be reclaimable (stale)")
	}
}

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("current process should be alive")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot spawn helper process: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Kill()
	_, _ = cmd.Process.Wait()
	if pidAlive(pid) {
		t.Errorf("pid %d should be dead after kill+reap", pid)
	}
}

// TestServiceReadOnlyWhenLockHeld is the card's outcome: a second process
// on the same repo detects the lock and runs read-only — it serves
// queries but starts no writer, so an out-of-band edit is not indexed by
// it.
func TestServiceReadOnlyWhenLockHeld(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	// Pre-hold the lock with a live owner (this test process), fresh mtime.
	lockPath := filepath.Join(root, ".sense", lockFileName)
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	if svc.Writing() {
		t.Fatal("service should be read-only when another process holds the lock")
	}

	// Edit out of band; the read-only service must not index it.
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Duration(DefaultDebounceMs)*time.Millisecond + 500*time.Millisecond)
	if symbolExists(t, root, "Goodbye") {
		t.Error("read-only service should not have indexed the edit")
	}
}

// TestServiceContentionExactlyOneWriter starts two Services on one repo
// and asserts exactly one wins the writer role.
func TestServiceContentionExactlyOneWriter(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	svc1, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService 1: %v", err)
	}
	if err := svc1.Start(context.Background()); err != nil {
		t.Fatalf("Start 1: %v", err)
	}
	defer svc1.Stop()

	svc2, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService 2: %v", err)
	}
	if err := svc2.Start(context.Background()); err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	defer svc2.Stop()

	if !svc1.Writing() {
		t.Error("first service should own the writer role")
	}
	if svc2.Writing() {
		t.Error("second service should be read-only")
	}
}

func TestIsWriterLocked(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No lock file → not locked.
	if IsWriterLocked(dir) {
		t.Error("should not report locked when no lock file exists")
	}
	// Held by a live process → locked.
	l, ok, err := acquireWriterLock(filepath.Join(dir, ".sense"))
	if err != nil || !ok {
		t.Fatalf("acquire: ok=%v err=%v", ok, err)
	}
	if !IsWriterLocked(dir) {
		t.Error("should report locked while a live process holds it")
	}
	l.release()
	if IsWriterLocked(dir) {
		t.Error("should not report locked after release")
	}
}
