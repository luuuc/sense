package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
)

// This file holds the freshness footer and read-repair concern shared by the
// sense_graph, sense_blast, and sense_status handlers: a single stale-file
// sweep, the footer it feeds, and the inline re-index that keeps an
// edit-then-immediately-query honest before the debounced watcher fires.

// staleSnapshot is one pass over the indexed files: the relative paths
// whose on-disk mtime is newer than their indexed_at, plus the newest
// mtime seen. It is computed once per query and shared between read-repair
// (which re-indexes the stale paths) and the freshness footer (which
// reports the count), so the per-query stat sweep happens at most once on
// the common path.
type staleSnapshot struct {
	staleRels []string
	maxMtime  *time.Time
}

func scanStaleFiles(ctx context.Context, db *sql.DB, dir string) staleSnapshot {
	var snap staleSnapshot
	rows, err := db.QueryContext(ctx, `SELECT path, indexed_at FROM sense_files`)
	if err != nil {
		return snap
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
		mtime := info.ModTime()
		if mtime.After(indexedAt) {
			snap.staleRels = append(snap.staleRels, path)
		}
		if snap.maxMtime == nil || mtime.After(*snap.maxMtime) {
			snap.maxMtime = &mtime
		}
	}
	return snap
}

// computeFreshness builds the freshness footer. When snap is non-nil it
// reuses that precomputed stale sweep instead of running its own, so a
// handler that already swept for read-repair does not stat the tree twice.
func computeFreshness(ctx context.Context, db *sql.DB, dir string, includeMaxMtime bool, ws *mcpio.WatchState, snap *staleSnapshot) *mcpio.Freshness {
	var lastScanStr sql.NullString
	row := db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`)
	if err := row.Scan(&lastScanStr); err != nil || !lastScanStr.Valid {
		return nil
	}

	lastScan, err := time.Parse(time.RFC3339Nano, lastScanStr.String)
	if err != nil {
		return nil
	}

	ageSeconds := int64(time.Since(lastScan).Seconds())
	lastScanFmt := lastScan.UTC().Format(time.RFC3339)

	f := &mcpio.Freshness{
		LastScan:        &lastScanFmt,
		IndexAgeSeconds: &ageSeconds,
	}

	if snap == nil {
		s := scanStaleFiles(ctx, db, dir)
		snap = &s
	}
	staleCount := len(snap.staleRels)
	f.StaleFilesSeen = &staleCount

	if includeMaxMtime && snap.maxMtime != nil {
		ts := snap.maxMtime.UTC().Format(time.RFC3339)
		f.MaxFileMtimeSinceScan = &ts
	}

	if ws != nil {
		watching, watchSince, lastIndexed, pending := ws.Snapshot()
		if watching {
			f.Watching = &watching
			ts := watchSince.UTC().Format(time.RFC3339)
			f.WatchSince = &ts
			if !lastIndexed.IsZero() {
				lu := lastIndexed.UTC().Format(time.RFC3339)
				f.LastUpdate = &lu
				age := int64(time.Since(lastIndexed).Seconds())
				f.IndexUpdateAgeSeconds = &age
			}
			p := pending
			f.Pending = &p
		}
	}

	return f
}

// maxInlineRepairFiles bounds how many stale files a single query will
// re-index inline. Beyond this (e.g. a branch switch touching dozens of
// files) the query answers on current data and defers to the background
// watcher and the git fast path — read-repair must never become
// "re-resolve the world" on a hot query.
const maxInlineRepairFiles = 8

// repairAndSnapshot sweeps for stale files and, when this server owns the
// writer and the stale set is small, re-indexes those files inline before
// the query reads them — so an edit-then-immediately-query reflects the
// edit even before the debounced watcher fires. It returns the freshness
// snapshot to reuse for the footer (recomputed only when a repair changed
// the index). When nothing is stale it is free; when read-only or the
// stale set is large it is a no-op sweep.
func (h *handlers) repairAndSnapshot(ctx context.Context) *staleSnapshot {
	snap := scanStaleFiles(ctx, h.db, h.dir)
	if h.freshen == nil || !h.freshen.Writing() {
		return &snap
	}
	n := len(snap.staleRels)
	if n == 0 || n > maxInlineRepairFiles {
		return &snap
	}
	if err := h.freshen.RepairFiles(ctx, snap.staleRels); err != nil {
		fmt.Fprintf(os.Stderr, "sense mcp: read-repair failed: %v\n", err)
		return &snap
	}
	// The repaired files are now current; re-sweep so the footer is honest.
	after := scanStaleFiles(ctx, h.db, h.dir)
	return &after
}
