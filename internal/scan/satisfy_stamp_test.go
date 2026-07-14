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
