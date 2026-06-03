package freshen

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The single-writer lock arbitrates indexing across processes on one
// repo. The first process to claim .sense/index.lock owns indexing and
// embedding; any other process (a second `sense mcp`, or `sense scan
// --watch` in a terminal) detects the lock and runs read-only — it still
// serves every query, it just starts no writer. SQLite WAL keeps reads
// safe regardless; the lock only prevents the wasteful double-parse and
// embed-controller thrash of two writers on one index.
const (
	lockFileName = "index.lock"

	// lockAcquireTries bounds the reclaim/retry loop so a pathological
	// race can never spin forever.
	lockAcquireTries = 3
)

// lockHeartbeatInterval is how often the owner refreshes the lock's mtime
// to prove liveness. lockStaleAfter bounds how long a lock survives
// without a heartbeat before another process may reclaim it; it must
// comfortably exceed the interval so a momentarily busy owner is not
// evicted, and it is the backstop for the case a bare pid probe misses:
// pid reuse, where a dead owner's pid is recycled by an unrelated live
// process and would otherwise look alive forever. They are vars only so
// tests can shrink them; production never reassigns them.
var (
	lockHeartbeatInterval = 5 * time.Second
	lockStaleAfter        = 20 * time.Second
)

// IsWriterLocked reports whether a live process currently holds the
// single-writer lock for the index under dir/.sense. It is a read-only
// probe for status reporting — it neither acquires nor modifies the lock.
func IsWriterLocked(dir string) bool {
	path := filepath.Join(dir, ".sense", lockFileName)
	if _, err := os.Stat(path); err != nil {
		return false
	}
	return !lockIsStale(path)
}

// writerLock is a held single-writer lock. It must be released with
// release() to stop the heartbeat and remove the file.
type writerLock struct {
	path string
	pid  int

	mu      sync.Mutex
	stopCh  chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

// acquireWriterLock attempts to become the single writer for the index
// under senseDir. It returns (lock, true, nil) when the lock is acquired —
// the caller owns writing and must call release. It returns (nil, false,
// nil) when another live process already owns the lock — the caller must
// run read-only. A non-nil error indicates an unexpected filesystem
// problem; callers may treat it like "not acquired" and degrade safely.
func acquireWriterLock(senseDir string) (*writerLock, bool, error) {
	path := filepath.Join(senseDir, lockFileName)
	pid := os.Getpid()

	var lastErr error
	for i := 0; i < lockAcquireTries; i++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			// Won the race: stamp our pid and start the heartbeat.
			_, werr := f.WriteString(strconv.Itoa(pid) + "\n")
			cerr := f.Close()
			if werr != nil {
				return nil, false, werr
			}
			if cerr != nil {
				return nil, false, cerr
			}
			l := &writerLock{path: path, pid: pid, stopCh: make(chan struct{})}
			l.startHeartbeat()
			return l, true, nil
		}
		if !os.IsExist(err) {
			return nil, false, err
		}

		// The lock exists. Reclaim it only if its owner is provably gone:
		// a dead pid, or a live pid whose heartbeat has gone stale (the
		// pid-reuse case). Otherwise defer to the live owner.
		if !lockIsStale(path) {
			return nil, false, nil
		}
		// Stale: remove and retry. A concurrent reclaimer may win the
		// re-create; the bounded loop re-reads and defers if so.
		if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
			lastErr = rerr
		}
	}
	return nil, false, lastErr
}

// lockIsStale reports whether the lock at path may be reclaimed: its owner
// pid is dead, or its heartbeat (file mtime) is older than lockStaleAfter,
// or the file is unreadable/corrupt. A fresh lock held by a live owner is
// never stale.
func lockIsStale(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		// Vanished between create-attempt and stat: treat as reclaimable.
		return true
	}
	if time.Since(info.ModTime()) > lockStaleAfter {
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		// Corrupt/empty content: reclaimable.
		return true
	}
	return !pidAlive(pid)
}

// pidAlive reports whether a process with the given pid exists. On Unix,
// signal 0 performs existence/permission checking without delivering a
// signal: nil or EPERM means alive, ESRCH means gone.
func pidAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}

// startHeartbeat refreshes the lock file's mtime on an interval so other
// processes can tell the owner is still alive even across pid reuse.
func (l *writerLock) startHeartbeat() {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		ticker := time.NewTicker(lockHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stopCh:
				return
			case <-ticker.C:
				now := time.Now()
				// If the file is gone (reclaimed) the touch fails harmlessly;
				// we keep ticking until release so we don't silently lose the
				// writer role mid-session.
				_ = os.Chtimes(l.path, now, now)
			}
		}
	}()
}

// release stops the heartbeat and removes the lock file if it still
// belongs to us. It is idempotent.
func (l *writerLock) release() {
	if l == nil {
		return
	}
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return
	}
	l.stopped = true
	close(l.stopCh)
	l.mu.Unlock()

	l.wg.Wait()

	// Only remove the file if we still own it; a reclaimer may have taken
	// over after a stale window, and we must not delete their lock.
	if data, err := os.ReadFile(l.path); err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(data))); perr == nil && pid == l.pid {
			_ = os.Remove(l.path)
		}
	}
}
