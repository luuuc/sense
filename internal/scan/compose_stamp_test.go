package scan_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestScanStampsComposesGoMeta pins the staleness signal for the G-7 Go
// composition-edge fix: a fresh scan stamps composes_go, a plain rescan
// preserves it, and an index stripped of the stamp (one built by a pre-fix
// binary, its Go composed_by still empty) is only re-stamped by a full-write
// scan — a plain rescan of unchanged files re-extracts nothing and must not
// claim healed.
func TestScanStampsComposesGoMeta(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readMetaKey(t, root, "composes_go"); got != "1" {
		t.Fatalf("composes_go = %q after fresh scan, want 1", got)
	}

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if got := readMetaKey(t, root, "composes_go"); got != "1" {
		t.Errorf("composes_go = %q after plain rescan, want preserved 1", got)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := a.DeleteMeta(ctx, "composes_go"); err != nil {
		t.Fatalf("delete meta: %v", err)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan on stripped index: %v", err)
	}
	if got := readMetaKey(t, root, "composes_go"); got != "" {
		t.Errorf("composes_go = %q after plain rescan of unchanged files, want unstamped", got)
	}

	opts := quietOpts(root)
	opts.Rebuild = true
	if _, err := scan.Run(context.Background(), opts); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := readMetaKey(t, root, "composes_go"); got != "1" {
		t.Errorf("composes_go = %q after rebuild, want 1", got)
	}
}

// TestGoComposesEdgePersists proves the G-7 fix end-to-end through
// resolve/persist: a named struct field typed by an in-repo type lands as a
// composes edge row, and a field typed by an unindexed external (std-lib
// time.Time) persists NO edge — unresolved targets drop at resolve rather
// than anchoring noise.
func TestGoComposesEdgePersists(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "customer.go"), `package shop

import "time"

type Customer struct{}

type Order struct {
	c       Customer
	stamped time.Time
}
`)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if n := countComposesEdges(t, root, "shop.Order", "shop.Customer"); n != 1 {
		t.Errorf("composes edges shop.Order -> shop.Customer = %d, want 1", n)
	}
	if n := countComposesEdges(t, root, "shop.Order", ""); n != 1 {
		t.Errorf("total composes edges from shop.Order = %d, want 1 (time.Time must drop)", n)
	}
}

func countComposesEdges(t *testing.T, root, source, target string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = db.Close() }()

	q := `SELECT COUNT(*)
	      FROM sense_edges e
	      JOIN sense_symbols ss ON ss.id = e.source_id
	      JOIN sense_symbols st ON st.id = e.target_id
	      WHERE e.kind = 'composes' AND ss.qualified = ?
	        AND (? = '' OR st.qualified = ?)`
	var n int
	if err := db.QueryRowContext(context.Background(), q, source, target, target).Scan(&n); err != nil {
		t.Fatalf("count composes edges: %v", err)
	}
	return n
}
