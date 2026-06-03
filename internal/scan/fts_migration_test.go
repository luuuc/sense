package scan_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/scan"
)

// downgradeFTS rewrites the index's FTS table to an older shape that lacks the
// snippet and name_parts columns, so the next sqlite.Open detects a stale FTS
// table and migrates it. It drops the four sync triggers first because they
// reference the dropped columns.
func downgradeFTS(t *testing.T, dbPath string) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open to downgrade fts: %v", err)
	}
	defer func() { _ = db.Close() }()

	for _, trig := range []string{
		"sense_symbols_fts_insert", "sense_symbols_fts_delete",
		"sense_symbols_fts_update", "sense_symbols_fts_update_after",
	} {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+trig); err != nil {
			t.Fatalf("drop trigger %s: %v", trig, err)
		}
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS sense_symbols_fts"); err != nil {
		t.Fatalf("drop fts table: %v", err)
	}
	// Recreate without snippet/name_parts so ftsNeedsMigration returns true.
	if _, err := db.ExecContext(ctx,
		"CREATE VIRTUAL TABLE sense_symbols_fts USING fts5(name, qualified, docstring)"); err != nil {
		t.Fatalf("recreate stale fts table: %v", err)
	}
}

// TestScanMigratesStaleFTSIndex covers the scan-time notice that fires when the
// index carries a pre-snippet FTS table: sqlite.Open migrates it on reopen and
// scan reports that keyword search will repopulate during the run.
func TestScanMigratesStaleFTSIndex(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc Run() int { return 1 }\n")
	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("first scan: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	downgradeFTS(t, dbPath)

	var out bytes.Buffer
	opts := scan.Options{Root: root, Output: &out, Warnings: io.Discard}
	if _, err := scan.Run(ctx, opts); err != nil {
		t.Fatalf("rescan after fts downgrade: %v", err)
	}

	if !strings.Contains(out.String(), "migrated fts index") {
		t.Errorf("expected migrated fts index notice, got:\n%s", out.String())
	}
}
