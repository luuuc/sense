package freshen

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

// The git fast path handles bulk working-tree changes from a branch switch
// or pull. fsnotify under such churn can drop events or split them across
// debounce windows; a single `git diff` between the previous and current
// HEAD is authoritative and complete, so on a HEAD change we re-index from
// the diff in one batch rather than reacting file-by-file. The general
// watcher already ignores .git/ (dot-directories are never registered), so
// the two never feed each other in a loop. Only standard repos (a .git
// directory) get the fast path; worktrees and bare/odd layouts fall back
// to the general watcher, which still re-indexes the changed files.

var errNotGitDir = errors.New("freshen: not a standard git repository")

// gitHead returns the current HEAD commit sha for the repo at root, or ""
// when root is not a git repo or git is unavailable (a local command, no
// network — honoring the no-implicit-network contract).
func gitHead(root string) string {
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitDiffNames returns the working-tree paths changed and removed between
// two commits, relative to root. A rename is reported as the old path
// removed and the new path changed. Non-source paths are left in: a
// subsequent incremental simply yields no symbols for them, and a removed
// path not in the index is a harmless no-op.
func gitDiffNames(root, from, to string) (changed, removed []string) {
	if from == "" || to == "" || from == to {
		return nil, nil
	}
	out, err := exec.Command("git", "-C", root, "diff", "--name-status", "-z", from, to).Output()
	if err != nil {
		return nil, nil
	}
	return parseDiffNameStatus(out)
}

// parseDiffNameStatus parses `git diff --name-status -z` output. Fields are
// NUL-separated. Most entries are STATUS\0path; renames and copies are
// R###\0old\0new. Truncated trailing entries are ignored rather than
// panicking.
func parseDiffNameStatus(out []byte) (changed, removed []string) {
	parts := strings.Split(string(out), "\x00")
	i := 0
	for i < len(parts) {
		status := parts[i]
		i++
		if status == "" {
			continue
		}
		switch status[0] {
		case 'R', 'C':
			if i+1 >= len(parts) {
				return changed, removed
			}
			removed = append(removed, parts[i])
			changed = append(changed, parts[i+1])
			i += 2
		case 'D':
			if i >= len(parts) {
				return changed, removed
			}
			removed = append(removed, parts[i])
			i++
		default: // A, M, T, U, ...
			if i >= len(parts) {
				return changed, removed
			}
			changed = append(changed, parts[i])
			i++
		}
	}
	return changed, removed
}

// newGitHeadWatcher returns an fsnotify watcher registered on the repo's
// .git directory, so HEAD-replacing writes (git writes HEAD.lock then
// renames it onto HEAD) surface as events. Watching the directory rather
// than the HEAD file survives that atomic rename. Returns errNotGitDir for
// anything that is not a standard .git directory.
func newGitHeadWatcher(root string) (*fsnotify.Watcher, error) {
	gitDir := filepath.Join(root, ".git")
	info, err := os.Stat(gitDir)
	if err != nil || !info.IsDir() {
		return nil, errNotGitDir
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := w.Add(gitDir); err != nil {
		_ = w.Close()
		return nil, err
	}
	return w, nil
}

// runGitHead watches for .git/HEAD changes and reconciles the index from a
// git diff on each one. It drops every other .git/ event. It exits when
// the context is cancelled or the watcher closes.
func (s *Service) runGitHead(ctx context.Context) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.gitFsw.Events:
			if !ok {
				return // watcher closed
			}
			if filepath.Base(ev.Name) == "HEAD" {
				s.reconcileGitHead(ctx)
			}
		case <-s.gitFsw.Errors:
			// fsnotify errors are non-actionable here; drain to keep the
			// watcher's internal goroutine from blocking. Lifecycle is
			// governed by ctx, so a closed Errors channel cannot busy-loop:
			// Stop cancels ctx before closing the watcher.
		}
	}
}

// reconcileGitHead re-indexes the diff between the last reconciled HEAD and
// the current one. It is a no-op when HEAD is unchanged (duplicate events
// from the lock+rename collapse here) or the diff is empty. It runs under
// the shared write mutex, so it never interleaves with a batch or repair.
func (s *Service) reconcileGitHead(ctx context.Context) {
	newHead := gitHead(s.root)
	if newHead == "" || newHead == s.lastHead {
		return
	}
	changed, removed := gitDiffNames(s.root, s.lastHead, newHead)
	s.lastHead = newHead
	if len(changed) == 0 && len(removed) == 0 {
		return
	}
	s.log("sense: git HEAD changed, re-indexing %d changed / %d removed from diff", len(changed), len(removed))
	s.writeMu.Lock()
	_ = processBatch(ctx, Batch{Changed: changed, Removed: removed}, s.pOpts)
	s.writeMu.Unlock()
	s.markIndexed(ctx)
}
