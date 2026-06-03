package freshen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/ignore"
)

func TestWatcherAddDirThenWrite(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	batches := Loop(ctx, w, 50)

	// Create subdirectory
	sub := filepath.Join(dir, "subpkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond) // let fsnotify register

	// Write a file into the new subdirectory
	if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		if len(batch.Changed) == 0 {
			t.Error("expected changed files from new subdirectory")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for event from new subdirectory")
	}
}

func TestWatcherRemoveDirAndReAdd(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "ephemeral")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.dirs["ephemeral"] {
		t.Fatal("ephemeral should be registered initially")
	}

	w.RemoveDir(sub)
	if w.dirs["ephemeral"] {
		t.Error("ephemeral should be deregistered after RemoveDir")
	}

	// Re-add the same directory (still exists on disk, just deregistered from watcher)
	if err := w.AddDir(sub); err != nil {
		t.Fatal(err)
	}
	if !w.dirs["ephemeral"] {
		t.Error("ephemeral should be re-registered after AddDir")
	}
}

func TestWatcherRelPath(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	abs := filepath.Join(dir, "src", "main.go")
	rel, err := w.RelPath(abs)
	if err != nil {
		t.Fatalf("RelPath: %v", err)
	}
	expected := filepath.Join("src", "main.go")
	if rel != expected {
		t.Errorf("RelPath = %q, want %q", rel, expected)
	}
}

func TestWatcherShouldIgnoreNestedPattern(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("*.tmp", "build/")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.ShouldIgnore(filepath.Join(dir, "data.tmp")) {
		t.Error("should ignore .tmp files")
	}
	if w.ShouldIgnore(filepath.Join(dir, "main.go")) {
		t.Error("should not ignore .go files")
	}
}

func TestLoopMultipleFileTypes(t *testing.T) {
	dir := t.TempDir()

	// Pre-create files
	existing := filepath.Join(dir, "existing.go")
	if err := os.WriteFile(existing, []byte("package p"), 0o644); err != nil {
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

	batches := Loop(ctx, w, 100)

	// Create new file
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package new"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Modify existing file
	if err := os.WriteFile(existing, []byte("package p // modified"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		if len(batch.Changed) == 0 {
			t.Error("expected changed files in batch")
		}
	case <-ctx.Done():
		t.Fatal("timed out")
	}
}

func TestBatchType(t *testing.T) {
	b := Batch{
		Changed: []string{"a.go", "b.go"},
		Removed: []string{"c.go"},
	}
	if len(b.Changed) != 2 {
		t.Errorf("Changed count = %d, want 2", len(b.Changed))
	}
	if len(b.Removed) != 1 {
		t.Errorf("Removed count = %d, want 1", len(b.Removed))
	}
}

func TestDefaultDebounceMsValue(t *testing.T) {
	if DefaultDebounceMs != 300 {
		t.Errorf("DefaultDebounceMs = %d, want 300", DefaultDebounceMs)
	}
}

func TestShouldSkipDirMatcherPaths(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("vendor/", "node_modules/")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.shouldSkipDir("vendor", "vendor") {
		t.Error("vendor should be skipped")
	}
	if !w.shouldSkipDir(".cache", ".cache") {
		t.Error(".cache should be skipped (dot prefix)")
	}
	if w.shouldSkipDir("src", "src") {
		t.Error("src should not be skipped")
	}
}

func TestWatcherDeepNestedDirectories(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	for _, rel := range []string{"a", filepath.Join("a", "b"), filepath.Join("a", "b", "c"), filepath.Join("a", "b", "c", "d")} {
		if !w.dirs[rel] {
			t.Errorf("directory %q not registered", rel)
		}
	}
}

func TestWatcherEventsAndErrors(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}

	// Events and Errors should be non-nil channels
	if w.Events() == nil {
		t.Error("Events() returned nil")
	}
	if w.Errors() == nil {
		t.Error("Errors() returned nil")
	}

	_ = w.Close()
}
