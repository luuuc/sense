// Package watch provides filesystem watching with recursive directory
// registration, debouncing, and gitignore/senseignore filtering.
package watch

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"

	"github.com/luuuc/sense/internal/ignore"
)

// Watcher monitors a directory tree for file changes, filtering by
// ignore rules and deduplicating events. It registers all non-ignored
// directories recursively and tracks new/removed directories.
type Watcher struct {
	fsw     *fsnotify.Watcher
	root    string
	matcher *ignore.Matcher

	mu   sync.Mutex
	dirs map[string]bool // tracked directories (relative paths)
}

// New creates a Watcher for root using the given ignore matcher.
// It recursively registers all non-ignored directories.
func New(root string, matcher *ignore.Matcher) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsw:     fsw,
		root:    root,
		matcher: matcher,
		dirs:    make(map[string]bool),
	}

	if err := w.registerAll(); err != nil {
		_ = fsw.Close()
		return nil, err
	}

	return w, nil
}

// Events returns the raw fsnotify event channel.
func (w *Watcher) Events() <-chan fsnotify.Event {
	return w.fsw.Events
}

// Errors returns the fsnotify error channel.
func (w *Watcher) Errors() <-chan error {
	return w.fsw.Errors
}

// Close stops the watcher and releases resources.
func (w *Watcher) Close() error {
	return w.fsw.Close()
}

// AddDir registers a new directory for watching if it passes ignore
// rules. Called when a directory creation event is observed.
func (w *Watcher) AddDir(absPath string) error {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return nil
	}
	if w.shouldSkipDir(rel, filepath.Base(absPath)) {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.dirs[rel] {
		return nil
	}
	if err := w.fsw.Add(absPath); err != nil {
		return err
	}
	w.dirs[rel] = true
	return nil
}

// RemoveDir deregisters a directory from watching.
func (w *Watcher) RemoveDir(absPath string) {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.dirs, rel)
	_ = w.fsw.Remove(absPath)
}

// ShouldIgnore returns true if a file event path should be filtered out.
func (w *Watcher) ShouldIgnore(absPath string) bool {
	rel, err := filepath.Rel(w.root, absPath)
	if err != nil {
		return true
	}
	return w.matcher.Match(rel, false)
}

// RelPath returns the path relative to root.
func (w *Watcher) RelPath(absPath string) (string, error) {
	return filepath.Rel(w.root, absPath)
}

func (w *Watcher) registerAll() error {
	if err := w.fsw.Add(w.root); err != nil {
		return err
	}
	w.dirs["."] = true

	return filepath.WalkDir(w.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if path == w.root {
			return nil
		}

		name := d.Name()
		rel, relErr := filepath.Rel(w.root, path)
		if relErr != nil {
			return nil
		}

		if w.shouldSkipDir(rel, name) {
			return fs.SkipDir
		}

		if err := w.fsw.Add(path); err != nil {
			return nil // non-fatal: permission errors etc
		}
		w.mu.Lock()
		w.dirs[rel] = true
		w.mu.Unlock()
		return nil
	})
}

func (w *Watcher) shouldSkipDir(rel, name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	return w.matcher.Match(rel, true)
}
