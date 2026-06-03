package freshen

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// scanRepo runs an initial full scan so a Service has an index to keep
// fresh. embeddings controls whether scan records embedding debt.
func scanRepo(t *testing.T, root string, embeddings bool) {
	t.Helper()
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: embeddings,
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

// symbolExists opens a fresh read connection and reports whether a symbol
// with the given name is in the index. A fresh connection sees the
// Service's committed WAL writes.
func symbolExists(t *testing.T, root, name string) bool {
	t.Helper()
	a, err := sqlite.Open(context.Background(), filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open read adapter: %v", err)
	}
	defer func() { _ = a.Close() }()
	var n int
	if err := a.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sense_symbols WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n > 0
}

func TestServiceReindexesOnEdit(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Embeddings enabled so the embed controller's checkDebt/runEmbed
	// closures execute (debt is deferred by the scan, then picked up).
	scanRepo(t, root, true)

	ws := &mcpio.WatchState{}
	svc, err := NewService(Config{Root: root, EmbeddingsEnabled: true, WatchState: ws})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	// Start publishes watching state for status reporting.
	if watching, _ := ws.Get(); !watching {
		t.Error("expected WatchState to report watching after Start")
	}

	if symbolExists(t, root, "Goodbye") {
		t.Fatal("Goodbye should not exist before the edit")
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(8 * time.Second)
	for {
		if symbolExists(t, root, "Goodbye") {
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out: Service did not re-index the edit")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// TestServiceAfterEmbedRefreshesPending verifies that when a background
// embed finishes, afterEmbed updates the watch state's pending count to the
// freshly drained debt (not the startup snapshot) and forwards to the
// external OnEmbedded callback. Without this, status would keep reporting
// the pre-embed backlog for the life of the session.
func TestServiceAfterEmbedRefreshesPending(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, true) // records embedding debt

	ws := &mcpio.WatchState{}
	svc, err := NewService(Config{Root: root, EmbeddingsEnabled: true, WatchState: ws})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer func() { _ = svc.adapter.Close() }()

	var forwardedN int
	var forwarded bool
	svc.onEmbedded = func(_ context.Context, n int) {
		forwarded = true
		forwardedN = n
	}

	// Drain the debt as a real background embed would, then signal completion.
	if _, err := scan.EmbedPending(context.Background(), svc.adapter, root); err != nil {
		t.Fatalf("EmbedPending: %v", err)
	}
	svc.afterEmbed(context.Background(), 7)

	if !forwarded || forwardedN != 7 {
		t.Errorf("expected onEmbedded forwarded with n=7, got forwarded=%v n=%d", forwarded, forwardedN)
	}
	_, _, lastIndexed, pending := ws.Snapshot()
	if pending != 0 {
		t.Errorf("expected pending refreshed to 0 after embed, got %d", pending)
	}
	if lastIndexed.IsZero() {
		t.Error("expected lastIndexed to be set after embed completion")
	}
}

func TestServiceRepairFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	// Idle debounce so only RepairFiles can refresh, not the watcher.
	svc, err := NewService(Config{Root: root, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()

	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := svc.RepairFiles(context.Background(), []string{"main.go"}); err != nil {
		t.Fatalf("RepairFiles: %v", err)
	}
	if !symbolExists(t, root, "Goodbye") {
		t.Error("RepairFiles should have re-indexed the edited file")
	}

	// Empty path set is a no-op.
	if err := svc.RepairFiles(context.Background(), nil); err != nil {
		t.Errorf("RepairFiles(nil) should be a no-op, got %v", err)
	}
}

func TestServiceRepairFilesReadOnlyNoop(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	// Pre-hold the lock with this live process → read-only service.
	if err := os.WriteFile(filepath.Join(root, ".sense", lockFileName),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
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
		t.Fatal("service should be read-only")
	}

	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.RepairFiles(context.Background(), []string{"main.go"}); err != nil {
		t.Fatalf("RepairFiles: %v", err)
	}
	if symbolExists(t, root, "Goodbye") {
		t.Error("read-only service must not repair")
	}
}

func TestServiceRepairAfterStopIsSafe(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	svc, err := NewService(Config{Root: root, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	svc.Stop()

	// A repair that races (or follows) shutdown must not write through the
	// closed adapter — it returns nil instead of panicking.
	if err := svc.RepairFiles(context.Background(), []string{"main.go"}); err != nil {
		t.Errorf("RepairFiles after Stop should be a safe no-op, got %v", err)
	}
}

func TestServiceStartIdempotent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	svc, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Stop()

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	// Second Start is a no-op and must not error or start a second watcher.
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
}

func TestServiceStopWithoutStart(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	svc, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	// Stop before Start must release the adapter without panicking, and be
	// safe to call again.
	svc.Stop()
	svc.Stop()
}

func TestServiceNewMissingIndex(t *testing.T) {
	// A root with no .sense directory: opening the index db fails because
	// its parent directory does not exist.
	root := t.TempDir()
	_, err := NewService(Config{Root: root})
	if err == nil {
		t.Fatal("expected NewService to fail when .sense/index.db cannot be opened")
	}
}

func TestServiceNewEmptyRootDefaults(t *testing.T) {
	// Empty Root defaults to the working directory. The package test
	// directory has no .sense, so the open fails — but the default-root
	// branch is exercised.
	if _, err := NewService(Config{Root: ""}); err == nil {
		t.Fatal("expected NewService to fail (no .sense in working dir)")
	}
}

func TestServiceNewBadConfig(t *testing.T) {
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(senseDir, "config.yml"),
		[]byte("ignore: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(Config{Root: root}); err == nil {
		t.Fatal("expected NewService to fail on malformed config.yml")
	}
}

// TestServiceRunExitsOnClosedBatches covers run's closed-channel branch: when
// the debounce loop closes the batch channel, the consumer goroutine returns.
func TestServiceRunExitsOnClosedBatches(t *testing.T) {
	s := &Service{}
	s.wg.Add(1)

	batches := make(chan Batch)
	close(batches)

	done := make(chan struct{})
	go func() {
		s.run(context.Background(), batches)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run did not exit when the batch channel closed")
	}
}

// TestRepairFilesNilAdapterAfterClose covers RepairFiles' raced-shutdown
// guard: a repair that runs (on the query goroutine) after Stop closed the
// adapter sees a nil adapter under the write mutex and bails instead of
// writing through a closed handle.
func TestRepairFilesNilAdapterAfterClose(t *testing.T) {
	s := &Service{}
	s.mu.Lock()
	s.started = true
	s.writing = true // pass the Writing() gate so we reach the adapter check
	s.mu.Unlock()
	s.adapter = nil

	if err := s.RepairFiles(context.Background(), []string{"main.go"}); err != nil {
		t.Errorf("RepairFiles with a nil adapter should be a safe no-op, got %v", err)
	}
}

// lockDir creates an unreadable (chmod 000) subdirectory and restores its
// permissions on cleanup so the temp tree can be removed. It is the harness
// for the permission-error branches; callers must root-guard.
func lockDir(t *testing.T, parent string) {
	t.Helper()
	locked := filepath.Join(parent, "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })
}

// TestServiceNewIgnoreBuildError covers NewService's ignore-matcher failure
// branch: an unreadable directory present before construction makes the
// gitignore walk fail to open a nested .gitignore, so NewService returns the
// error. Root-guarded.
func TestServiceNewIgnoreBuildError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission bits are ignored when running as root")
	}
	root := t.TempDir()
	lockDir(t, root)

	if _, err := NewService(Config{Root: root}); err == nil {
		t.Skip("this platform let the ignore walk read an unreadable dir; nothing to assert")
	}
}

// TestServiceStartWatcherCreateError covers Start's watcher-creation failure
// branch: the writer lock is acquired, but building the watcher fails because
// an unreadable directory appeared after construction (so the ignore matcher
// built on the clean tree), so Start releases the lock, closes the adapter,
// and returns the error. Root-guarded.
func TestServiceStartWatcherCreateError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission bits are ignored when running as root")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	// NewService walks the clean tree, so the ignore matcher builds fine.
	svc, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Stop()

	// Now lock a subdirectory: Start acquires the writer lock, then the
	// watcher's registerAll surfaces the unreadable dir as a New error.
	lockDir(t, root)

	if err := svc.Start(context.Background()); err == nil {
		t.Skip("this platform let the watcher register an unreadable dir; nothing to assert")
	}
	if svc.Writing() {
		t.Error("Start should not report writing after a watcher-creation failure")
	}
}

func TestServiceStartDegradesWhenLockCheckFails(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"),
		[]byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, root, false)

	svc, err := NewService(Config{Root: root})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Stop()

	// Remove the repo so the lock cannot be created. Start must not crash
	// the host: it degrades to read-only (serves queries, no writer).
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start should degrade, not error: %v", err)
	}
	if svc.Writing() {
		t.Error("service should be read-only when the lock cannot be acquired")
	}
}
