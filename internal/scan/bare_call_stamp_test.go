package scan_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestScanStampsBareCallBindsMeta pins the staleness signal for the G-10
// bare-call resolution fix: a fresh scan stamps bare_call_binds, a plain
// rescan preserves it, and an index stripped of the stamp (one built by a
// pre-fix binary, still carrying fabricated bare→method edges) is only
// re-stamped by a full-write scan — a plain rescan of unchanged files
// re-resolves nothing and must not claim healed.
func TestScanStampsBareCallBindsMeta(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readMetaKey(t, root, "bare_call_binds"); got != "1" {
		t.Fatalf("bare_call_binds = %q after fresh scan, want 1", got)
	}

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if got := readMetaKey(t, root, "bare_call_binds"); got != "1" {
		t.Errorf("bare_call_binds = %q after plain rescan, want preserved 1", got)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := a.DeleteMeta(ctx, "bare_call_binds"); err != nil {
		t.Fatalf("delete meta: %v", err)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan on stripped index: %v", err)
	}
	if got := readMetaKey(t, root, "bare_call_binds"); got != "" {
		t.Errorf("bare_call_binds = %q after plain rescan of unchanged files, want unstamped", got)
	}

	opts := quietOpts(root)
	opts.Rebuild = true
	if _, err := scan.Run(context.Background(), opts); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := readMetaKey(t, root, "bare_call_binds"); got != "1" {
		t.Errorf("bare_call_binds = %q after rebuild, want 1", got)
	}
}
