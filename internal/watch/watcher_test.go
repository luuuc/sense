package watch

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
