package watch

import (
	"context"
	"time"

	"github.com/fsnotify/fsnotify"
)

const DefaultDebounceMs = 300

// Batch is a set of unique file paths that changed within a debounce window.
type Batch struct {
	Changed []string // relative paths of created/modified files
	Removed []string // relative paths of removed files
}

// Loop runs the debounce event loop, emitting batches of changed paths
// on the returned channel. It blocks until ctx is cancelled.
// debounceMs controls how long to wait after the last event before
// emitting a batch (0 uses DefaultDebounceMs).
func Loop(ctx context.Context, w *Watcher, debounceMs int) <-chan Batch {
	if debounceMs <= 0 {
		debounceMs = DefaultDebounceMs
	}
	dur := time.Duration(debounceMs) * time.Millisecond

	out := make(chan Batch, 1)

	go func() {
		defer close(out)

		changed := make(map[string]bool)
		removed := make(map[string]bool)
		var timer *time.Timer
		var timerC <-chan time.Time

		flush := func() {
			if len(changed) == 0 && len(removed) == 0 {
				return
			}
			b := Batch{}
			for p := range changed {
				b.Changed = append(b.Changed, p)
			}
			for p := range removed {
				if !changed[p] {
					b.Removed = append(b.Removed, p)
				}
			}
			changed = make(map[string]bool)
			removed = make(map[string]bool)
			if len(b.Changed) > 0 || len(b.Removed) > 0 {
				select {
				case out <- b:
				case <-ctx.Done():
				}
			}
		}

		for {
			select {
			case <-ctx.Done():
				if timer != nil {
					timer.Stop()
				}
				flush()
				return

			case ev, ok := <-w.Events():
				if !ok {
					flush()
					return
				}
				if ev.Name == "" {
					continue
				}

				// Handle directory creation/removal
				if ev.Has(fsnotify.Create) {
					if isDir(ev.Name) {
						_ = w.AddDir(ev.Name)
						continue
					}
				}
				if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
					w.RemoveDir(ev.Name)
				}

				if w.ShouldIgnore(ev.Name) {
					continue
				}

				rel, err := w.RelPath(ev.Name)
				if err != nil {
					continue
				}

				if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
					removed[rel] = true
				} else {
					changed[rel] = true
				}

				// Reset debounce timer
				if timer == nil {
					timer = time.NewTimer(dur)
					timerC = timer.C
				} else {
					timer.Reset(dur)
				}

			case <-timerC:
				flush()
				timer = nil
				timerC = nil

			case _, ok := <-w.Errors():
				if !ok {
					return
				}
			}
		}
	}()

	return out
}
