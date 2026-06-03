package freshen

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// maxReconcileFiles bounds the session-start catch-up so it stays quick and
// well within a hook's timeout. Larger drift (e.g. a big pull while the
// editor was closed) is left to a full rescan or the live watcher rather
// than blocking the first query on a heavy re-index.
const maxReconcileFiles = 64

// AcquireWriterLock claims the single-writer role for the index under
// dir/.sense on behalf of a short-lived caller (the session-start hook).
// When ok is true the caller owns writing and must invoke release; when
// false another live process already owns indexing and release is a no-op.
func AcquireWriterLock(dir string) (release func(), ok bool) {
	lock, acquired, err := acquireWriterLock(filepath.Join(dir, ".sense"))
	if err != nil || !acquired {
		return func() {}, false
	}
	return lock.release, true
}

// ReconcileDrift re-indexes files modified since the index was last
// written — the drift that accumulates while no watcher is running (the
// editor closed, edits or a pull in between). It is structural-only and
// bounded: if more than maxReconcileFiles drifted it does nothing and
// returns skipped=true, deferring to a full rescan or the live watcher.
//
// It deliberately handles modifications only, not deletions: removing
// index rows on a single startup stat-miss would risk discarding live data
// on a transient filesystem hiccup, and the live watcher already removes
// genuinely deleted files in real time. The caller must already hold the
// writer lock (see AcquireWriterLock) so it does not race the server's own
// writer.
func ReconcileDrift(ctx context.Context, adapter *sqlite.Adapter, dir string) (changed int, skipped bool, err error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return 0, false, err
	}
	matcher, err := ignore.Build(dir, cfg.Ignore)
	if err != nil {
		return 0, false, err
	}

	chPaths := driftPaths(ctx, adapter.DB(), dir)
	if len(chPaths) == 0 {
		return 0, false, nil
	}
	if len(chPaths) > maxReconcileFiles {
		return 0, true, nil
	}

	_, err = scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:              dir,
		Idx:               adapter,
		Matcher:           matcher,
		MaxFileSizeKB:     cfg.Scan.MaxFileSizeKB,
		EmbeddingsEnabled: false,
		Output:            io.Discard,
		Warnings:          io.Discard,
		Changed:           chPaths,
	})
	return len(chPaths), false, err
}

// driftPaths returns indexed files whose on-disk mtime is newer than their
// indexed_at. A git pull bumps file mtimes, so this catches git changes too
// without persisting a HEAD. Files missing on disk are left alone (see
// ReconcileDrift) — they are not treated as deletions here.
func driftPaths(ctx context.Context, db *sql.DB, dir string) (changed []string) {
	rows, err := db.QueryContext(ctx, `SELECT path, indexed_at FROM sense_files`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var path, indexedAtStr string
		if err := rows.Scan(&path, &indexedAtStr); err != nil {
			continue
		}
		indexedAt, err := time.Parse(time.RFC3339Nano, indexedAtStr)
		if err != nil {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, path))
		if err != nil {
			continue
		}
		if info.ModTime().After(indexedAt) {
			changed = append(changed, path)
		}
	}
	return changed
}
