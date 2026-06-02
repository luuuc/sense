package scan_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedMetric writes a lifetime counter into the index the way metrics/tracker
// would, so a rebuild has something to preserve.
func seedMetric(t *testing.T, dbPath, key string, value int) {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open to seed metric: %v", err)
	}
	defer func() { _ = a.Close() }()
	if _, err := a.DB().ExecContext(ctx,
		"INSERT INTO sense_metrics(key, value) VALUES (?, ?) "+
			"ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value); err != nil {
		t.Fatalf("seed metric %s: %v", key, err)
	}
}

func readMetric(t *testing.T, dbPath, key string) int {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open to read metric: %v", err)
	}
	defer func() { _ = a.Close() }()
	var v int
	if err := a.DB().QueryRowContext(ctx,
		"SELECT value FROM sense_metrics WHERE key = ?", key).Scan(&v); err != nil {
		t.Fatalf("read metric %s: %v", key, err)
	}
	return v
}

// TestScanRebuildRegeneratesAndPreservesMetrics is the --rebuild contract: it
// bypasses the content-hash skip so every file is re-parsed and re-resolved
// from source, while the lifetime metrics survive. A plain rescan (which skips
// unchanged files) is run first as the baseline the rebuild departs from.
func TestScanRebuildRegeneratesAndPreservesMetrics(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "lib.go"), "package main\n\nfunc Helper() int { return 1 }\n")
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc Run() int { return Helper() }\n")
	ctx := context.Background()

	first, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if first.Symbols == 0 {
		t.Fatal("first scan produced no symbols — test cannot prove regeneration")
	}
	if first.Edges == 0 {
		t.Fatal("first scan produced no edges — test cannot prove edge regeneration")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	seedMetric(t, dbPath, "lifetime_queries", 77)

	// Baseline: a plain rescan skips every unchanged file via the hash map.
	plain, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if plain.Changed != 0 {
		t.Errorf("plain rescan Changed = %d, want 0 (hashes unchanged)", plain.Changed)
	}
	if plain.Skipped == 0 {
		t.Error("plain rescan Skipped = 0, want >0 (unchanged files should skip)")
	}

	// --rebuild empties sense_files, so the hash-skip misses and every file
	// is re-parsed and re-resolved.
	opts := quietOpts(root)
	opts.Rebuild = true
	rebuilt, err := scan.Run(ctx, opts)
	if err != nil {
		t.Fatalf("rebuild scan: %v", err)
	}
	if rebuilt.Skipped != 0 {
		t.Errorf("rebuild Skipped = %d, want 0 (nothing should skip)", rebuilt.Skipped)
	}
	if rebuilt.Changed != rebuilt.Indexed {
		t.Errorf("rebuild Changed = %d, want = Indexed %d (every file re-parsed)",
			rebuilt.Changed, rebuilt.Indexed)
	}
	if rebuilt.Symbols != first.Symbols {
		t.Errorf("rebuild Symbols = %d, want %d (regenerated from source)",
			rebuilt.Symbols, first.Symbols)
	}
	if rebuilt.Edges != first.Edges {
		t.Errorf("rebuild Edges = %d, want %d (regenerated from source)",
			rebuilt.Edges, first.Edges)
	}

	// The lifetime metric survived the drop-and-recreate.
	if got := readMetric(t, dbPath, "lifetime_queries"); got != 77 {
		t.Errorf("lifetime_queries after rebuild = %d, want 77 (metrics must persist)", got)
	}
}
