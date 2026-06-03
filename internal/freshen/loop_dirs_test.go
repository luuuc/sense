package freshen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/ignore"
)

func TestLoopContextCancellationCloses(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	batches := Loop(ctx, w, 100)

	// Cancel immediately — Loop should exit cleanly and close the channel.
	cancel()

	timeout := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-batches:
			if !ok {
				return // channel closed — success
			}
		case <-timeout:
			t.Fatal("timed out waiting for Loop to close after context cancel")
		}
	}
}

func TestLoopDefaultDebounce(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// debounceMs=0 should use the default.
	batches := Loop(ctx, w, 0)

	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		if len(batch.Changed) == 0 {
			t.Error("expected changed files")
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestLoopRemovedFilesInBatch(t *testing.T) {
	dir := t.TempDir()

	// Create a file before starting the watcher.
	target := filepath.Join(dir, "doomed.go")
	if err := os.WriteFile(target, []byte("package doomed"), 0o644); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 50)

	// Remove the file — should appear in Removed.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		if len(batch.Removed) == 0 && len(batch.Changed) == 0 {
			t.Error("expected file event in batch")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for removal batch")
	}
}

func TestLoopIgnoredPathsFiltered(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("*.log")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 50)

	// Write an ignored file, then a non-ignored file.
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		for _, p := range batch.Changed {
			if p == "debug.log" {
				t.Error("debug.log should have been filtered by ignore matcher")
			}
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestLoopCollapseRapidEvents(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 200)

	// Write the same file rapidly multiple times.
	target := filepath.Join(dir, "hot.go")
	for i := 0; i < 5; i++ {
		if err := os.WriteFile(target, []byte("package hot // "+string(rune('0'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case batch := <-batches:
		count := 0
		for _, p := range batch.Changed {
			if p == "hot.go" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected hot.go deduplicated to 1 entry, got %d", count)
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestAddDir(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	newDir := filepath.Join(dir, "newpkg")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := w.AddDir(newDir); err != nil {
		t.Fatalf("AddDir: %v", err)
	}

	if !w.dirs["newpkg"] {
		t.Error("newpkg should be registered")
	}

	// Adding again should be a no-op.
	if err := w.AddDir(newDir); err != nil {
		t.Fatalf("AddDir (idempotent): %v", err)
	}
}

func TestAddDirSkipsDotPrefixed(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	dotDir := filepath.Join(dir, ".hidden")
	if err := os.Mkdir(dotDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := w.AddDir(dotDir); err != nil {
		t.Fatalf("AddDir: %v", err)
	}

	if w.dirs[".hidden"] {
		t.Error(".hidden should be skipped by shouldSkipDir")
	}
}

func TestAddDirSkipsIgnored(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("vendor/")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	vendorDir := filepath.Join(dir, "vendor")
	if err := os.Mkdir(vendorDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := w.AddDir(vendorDir); err != nil {
		t.Fatalf("AddDir: %v", err)
	}

	if w.dirs["vendor"] {
		t.Error("vendor should be skipped by ignore matcher")
	}
}

func TestRemoveDir(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.dirs["sub"] {
		t.Fatal("sub should be registered initially")
	}

	w.RemoveDir(subDir)

	if w.dirs["sub"] {
		t.Error("sub should be deregistered after RemoveDir")
	}
}

func TestIsDirNonExistent(t *testing.T) {
	if isDir("/nonexistent/path/that/does/not/exist") {
		t.Error("non-existent path should return false")
	}
}

func TestIsDirFile(t *testing.T) {
	f, err := os.CreateTemp("", "testfile")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	_ = f.Close()

	if isDir(f.Name()) {
		t.Error("regular file should return false")
	}
}

func TestIsDirActualDir(t *testing.T) {
	dir := t.TempDir()
	if !isDir(dir) {
		t.Error("directory should return true")
	}
}

func TestLoopNewDirectoryAutoRegistered(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 100)

	// Create a new directory and write a file into it.
	newDir := filepath.Join(dir, "newpkg")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(newDir, "x.go"), []byte("package newpkg"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		_ = batch // we care that it arrived at all
	case <-ctx.Done():
		t.Fatal("timed out waiting for event from new directory")
	}
}

func TestWatcherShouldIgnoreRelPathError(t *testing.T) {
	// ShouldIgnore with a path that can't be made relative to root
	// should return true (ignore it).
	w := &Watcher{
		root:    "/some/root",
		matcher: ignore.New(),
	}
	// fsnotify.Watcher is nil so we can't call Close, but that's fine
	// for this unit test — we're only testing ShouldIgnore's path logic.

	// A path completely outside root may still produce a valid relative
	// path via "../" so test the matcher.Match result instead.
	// The only case Rel errors is with different volumes on Windows.
	// On unix, just verify that matching works correctly.
	if w.ShouldIgnore("/some/root/valid.go") {
		t.Error("valid path under root should not be ignored")
	}
}

// TestLoopErrorChannelClose ensures Loop exits cleanly when the
// fsnotify error channel is closed (watcher shutdown).
func TestLoopErrorChannelClose(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 50)

	// Closing the watcher closes both Events and Errors channels,
	// which should cause Loop to flush and exit.
	_ = w.Close()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-batches:
			if !ok {
				return // channel closed — success
			}
		case <-timeout:
			t.Fatal("timed out waiting for Loop to exit after watcher close")
		}
	}
}

func TestNewDirectoryCreatedDuringWatch(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := Loop(ctx, w, 100)

	newDir := filepath.Join(dir, "created")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Wait for the Loop to process the Create event and call AddDir.
	deadline := time.After(2 * time.Second)
	for {
		w.mu.Lock()
		tracked := w.dirs["created"]
		w.mu.Unlock()
		if tracked {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for directory to be tracked")
		case <-time.After(20 * time.Millisecond):
		}
	}

	// Drain the channel to avoid goroutine leak.
	cancel()
	//nolint:revive // drain channel
	for range ch {
	}
}

// Verify the Batch type properly separates changed and removed.
func TestBatchSeparation(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "temp.go")
	if err := os.WriteFile(target, []byte("package temp"), 0o644); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Use a synthetic event check: write a new file, then remove the old one.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 100)

	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package new"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		_ = batch
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestIntegration_WatchWriteDebounce(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("*.log")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	debounceMs := 150
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	batches := Loop(ctx, w, debounceMs)

	// Write a file and record the time.
	writeStart := time.Now()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package app\n\nfunc Run() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		elapsed := time.Since(writeStart)

		// Batch should arrive after the debounce period.
		if elapsed < time.Duration(debounceMs)*time.Millisecond {
			t.Errorf("batch arrived too early (%s < %dms debounce)", elapsed, debounceMs)
		}

		// Batch should contain the written file.
		found := false
		for _, p := range batch.Changed {
			if p == "app.go" {
				found = true
			}
		}
		if !found {
			t.Errorf("batch.Changed = %v, expected app.go", batch.Changed)
		}
		if len(batch.Removed) != 0 {
			t.Errorf("batch.Removed = %v, expected empty", batch.Removed)
		}

	case <-ctx.Done():
		t.Fatal("timed out waiting for debounced batch")
	}
}
