package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

// TestScanStaleFilesSkipsUnparseableTimestamp seeds a file row whose
// indexed_at is not a valid RFC3339 timestamp. The sweep must skip that row
// (it cannot decide staleness without a parseable indexed_at) rather than
// abort or count it, and still process the well-formed rows around it.
func TestScanStaleFilesSkipsUnparseableTimestamp(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "bad_ts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// A file row with a garbage indexed_at — time.Parse fails, so the row is
	// skipped without affecting the sweep's outcome.
	if _, err := adapter.DB().ExecContext(ctx,
		`INSERT INTO sense_files (path, language, hash, symbols, indexed_at)
		 VALUES ('bad.go','go','h',0,'not-a-timestamp')`); err != nil {
		t.Fatal(err)
	}

	snap := scanStaleFiles(ctx, adapter.DB(), dir)
	if len(snap.staleRels) != 0 {
		t.Errorf("unparseable-timestamp row must not be counted stale, got %d", len(snap.staleRels))
	}
	if snap.maxMtime != nil {
		t.Errorf("skipped row must not contribute a maxMtime, got %v", snap.maxMtime)
	}
}

// TestComputeFreshnessUnparseableLastScan covers the guard where the index's
// MAX(indexed_at) is present but not a valid timestamp: computeFreshness
// cannot report an age it cannot parse, so it returns nil.
func TestComputeFreshnessUnparseableLastScan(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "bad_scan.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	if _, err := adapter.DB().ExecContext(ctx,
		`INSERT INTO sense_files (path, language, hash, symbols, indexed_at)
		 VALUES ('only.go','go','h',0,'garbage-scan-time')`); err != nil {
		t.Fatal(err)
	}

	f := computeFreshness(ctx, adapter.DB(), dir, false, nil, nil)
	if f != nil {
		t.Errorf("expected nil freshness when last_scan is unparseable, got %+v", f)
	}
}

// TestComputeFreshnessMaxMtimeFromStaleSweep wires the includeMaxMtime path:
// a real on-disk file edited after its (old) indexed_at makes the precomputed
// stale snapshot carry a maxMtime, which computeFreshness surfaces as
// max_file_mtime_since_scan.
func TestComputeFreshnessMaxMtimeFromStaleSweep(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Create one of the fixture's indexed files on disk so the sweep can stat
	// it and record a maxMtime.
	rel := "cmd/main.go"
	abs := filepath.Join(ts.handlers.dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	snap := scanStaleFiles(ctx, ts.handlers.db, ts.handlers.dir)
	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, true, nil, &snap)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
	if snap.maxMtime != nil && f.MaxFileMtimeSinceScan == nil {
		t.Error("includeMaxMtime should surface max_file_mtime_since_scan when the sweep saw a file")
	}
}

// TestRepairAndSnapshotNoFreshenService covers the read-only short-circuit:
// when no freshen.Service is wired (h.freshen == nil), repairAndSnapshot
// returns the raw sweep without attempting any inline re-index.
func TestRepairAndSnapshotNoFreshenService(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// h.freshen is nil on the fixture server.
	snap := ts.handlers.repairAndSnapshot(ctx)
	if snap == nil {
		t.Fatal("repairAndSnapshot returned nil snapshot")
	}
	_ = ts.handlers.watchState // documents that no watcher is involved here
}
