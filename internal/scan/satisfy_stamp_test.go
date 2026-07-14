package scan_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestScanStampsSatisfyUnbudgetedMeta pins the healing signal for the G-2
// satisfaction-budget fix: a fresh scan stamps satisfy_unbudgeted, and —
// unlike the rebuild-gated stamps (composes_go et al.) — a PLAIN rescan
// re-stamps a stripped index, because the satisfy pass recomputes over the
// full symbol table on every scan. The status advisory can therefore honestly
// say "run 'sense scan'" instead of demanding a rebuild.
func TestScanStampsSatisfyUnbudgetedMeta(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readMetaKey(t, root, "satisfy_unbudgeted"); got != "1" {
		t.Fatalf("satisfy_unbudgeted = %q after fresh scan, want 1", got)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := a.DeleteMeta(ctx, "satisfy_unbudgeted"); err != nil {
		t.Fatalf("delete meta: %v", err)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if got := readMetaKey(t, root, "satisfy_unbudgeted"); got != "1" {
		t.Errorf("satisfy_unbudgeted = %q after plain rescan, want re-stamped 1 (the pass runs every scan)", got)
	}
}

// TestScanStampsSatisfyArityMeta mirrors the unbudgeted stamp for the
// arity-matching fix: fresh scan stamps, and a PLAIN rescan re-stamps a
// stripped index because the satisfy pass recomputes every scan.
func TestScanStampsSatisfyArityMeta(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readMetaKey(t, root, "satisfy_arity"); got != "1" {
		t.Fatalf("satisfy_arity = %q after fresh scan, want 1", got)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := a.DeleteMeta(ctx, "satisfy_arity"); err != nil {
		t.Fatalf("delete meta: %v", err)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if got := readMetaKey(t, root, "satisfy_arity"); got != "1" {
		t.Errorf("satisfy_arity = %q after plain rescan, want re-stamped 1", got)
	}
}

// TestSatisfyClearsStaleEdges pins the shrink path: a satisfaction edge that
// no longer holds (here: injected junk) must DISAPPEAR on a plain rescan —
// the pass owns its edge set and rewrites it wholesale. Without the clear,
// upsert semantics keep stale junk forever and a tightening of the matcher
// never reaches existing indexes.
func TestSatisfyClearsStaleEdges(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), `package kv

import "context"

type Closer interface {
	Close(ctx context.Context) error
}

type GoodStore struct{}

func (g *GoodStore) Close(ctx context.Context) error { return nil }

type JunkStore struct{}

func (j *JunkStore) Close() error { return nil }
`)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	// Inject the junk pair a pre-arity binary would have written: JunkStore's
	// Close() does NOT satisfy Closer's Close(ctx) error, so the arity-aware
	// pass never emits it — a persisted stale row is exactly the upgrade case.
	if _, err := a.DB().ExecContext(ctx, `
		INSERT INTO sense_edges (source_id, target_id, kind, file_id, line, confidence)
		SELECT s.id, i.id, 'inherits', s.file_id, 1, 0.9
		FROM sense_symbols s, sense_symbols i
		WHERE s.name='JunkStore' AND i.name='Closer'`); err != nil {
		t.Fatalf("inject stale edge: %v", err)
	}
	var before int
	_ = a.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges e JOIN sense_symbols i ON e.target_id=i.id
		WHERE e.kind='inherits' AND i.kind='interface' AND e.confidence=0.9`).Scan(&before)
	if before != 2 {
		t.Fatalf("fixture expects the true edge + the injected junk, got %d", before)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	a2, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = a2.Close() }()
	var after int
	_ = a2.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges e JOIN sense_symbols i ON e.target_id=i.id
		WHERE e.kind='inherits' AND i.kind='interface' AND e.confidence=0.9`).Scan(&after)
	if after != 1 {
		t.Errorf("want exactly the true GoodStore edge after rescan, got %d (before=%d)", after, before)
	}
	var junk int
	_ = a2.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges e
		JOIN sense_symbols s ON e.source_id=s.id JOIN sense_symbols i ON e.target_id=i.id
		WHERE s.name='JunkStore' AND i.name='Closer' AND e.kind='inherits'`).Scan(&junk)
	if junk != 0 {
		t.Errorf("the arity-junk edge survived: Close() must not satisfy Close(ctx) error")
	}
}
