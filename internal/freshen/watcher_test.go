package freshen

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/ignore"
)

func TestWatcherRegistersDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.dirs["."] {
		t.Error("root not registered")
	}
	if !w.dirs["src"] {
		t.Error("src not registered")
	}
	if !w.dirs[filepath.Join("src", "pkg")] {
		t.Error("src/pkg not registered")
	}
	if w.dirs[".git"] {
		t.Error(".git should be skipped")
	}
}

func TestWatcherIgnoresMatchedFiles(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("*.log")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.ShouldIgnore(filepath.Join(dir, "debug.log")) {
		t.Error("should ignore .log files")
	}
	if w.ShouldIgnore(filepath.Join(dir, "main.go")) {
		t.Error("should not ignore .go files")
	}
}

func TestWatcherNewBadPath(t *testing.T) {
	matcher := ignore.New()
	_, err := New("/nonexistent/path/that/does/not/exist", matcher)
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestWatcherShouldIgnoreExactMatch(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("debug.log")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.ShouldIgnore(filepath.Join(dir, "debug.log")) {
		t.Error("should ignore exact match")
	}
}

func TestWatcherShouldIgnorePrefixMatch(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("test*")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.ShouldIgnore(filepath.Join(dir, "test_main.go")) {
		t.Error("should ignore prefix match")
	}
	if w.ShouldIgnore(filepath.Join(dir, "main_test.go")) {
		t.Error("should not ignore suffix match")
	}
}

func TestWatcherShouldIgnoreNestedPath(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New("vendor/")
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	if !w.ShouldIgnore(filepath.Join(dir, "vendor", "pkg", "file.go")) {
		t.Error("should ignore nested path under vendor/")
	}
	if w.ShouldIgnore(filepath.Join(dir, "src", "vendor.go")) {
		t.Error("should not ignore file named vendor.go outside vendor/")
	}
}

func TestWatcherRemoveDirNonExistent(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Removing a directory that was never registered should not panic.
	w.RemoveDir(filepath.Join(dir, "never-registered"))
}

func TestWatcherAddDirReturnsErrorForNonExistent(t *testing.T) {
	// Cover the fsw.Add error branch in AddDir (watcher.go:81-83):
	// fsnotify rejects paths that don't exist on disk.
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	err = w.AddDir(filepath.Join(dir, "does-not-exist"))
	if err == nil {
		t.Error("expected error when AddDir targets a non-existent path")
	}
}

func TestDebounceBatchesEvents(t *testing.T) {
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

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"), []byte("package b"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case batch := <-batches:
		if len(batch.Changed) == 0 {
			t.Error("expected changed files in batch")
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for batch")
	}
}

func TestWatcherAddDirAlreadyRegistered(t *testing.T) {
	dir := t.TempDir()
	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Adding the root directory again should be a no-op
	err = w.AddDir(dir)
	if err != nil {
		t.Errorf("AddDir on already-registered dir should not error, got %v", err)
	}
}

func TestWatcherAddDirSkipDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}

	matcher := ignore.New()
	w, err := New(dir, matcher)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = w.Close() }()

	// Adding a hidden directory should be skipped
	err = w.AddDir(filepath.Join(dir, ".hidden"))
	if err != nil {
		t.Errorf("AddDir on hidden dir should not error, got %v", err)
	}
}
