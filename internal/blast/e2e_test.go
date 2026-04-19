package blast_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestE2EBlastOnSenseRepo is the pitch's acceptance gate: after a
// full scan of the Sense repo itself, blast.Compute on a real
// symbol must return a non-empty DirectCallers list and a valid
// Risk classification.
//
// The pitch uses the placeholder name "Scanner" in its acceptance
// text; no such symbol exists in the Sense codebase. `extract.Register`
// stands in as the subject because every language extractor's init()
// function calls it — the five Tier-Basic extractors (ruby, python,
// tsjs, golang, rust) give a predictable multi-caller fan-in that
// proves the full emit → resolve → hydrate → classify pipeline is
// operating on real source.
//
// The Sense directory is a tempdir so the repo's working tree stays
// clean. repoRoot is resolved relative to this test file's location:
// internal/blast/... → `../..` is the module root.
func TestE2EBlastOnSenseRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("E2E: scan-and-query takes ~200ms; run without -short")
	}
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	senseDir := t.TempDir()
	ctx := context.Background()
	res, err := scan.Run(ctx, scan.Options{
		Root:     repoRoot,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("scan.Run on repo root: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatal("no symbols indexed from repo — scan may not have walked correctly")
	}
	t.Logf("scanned repo: %d files, %d indexed, %d symbols, %d edges, %d unresolved",
		res.Files, res.Indexed, res.Symbols, res.Edges, res.Unresolved)

	dbPath := filepath.Join(senseDir, "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	all, err := adapter.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	var subjectID int64
	for _, s := range all {
		if s.Qualified == "extract.Register" {
			subjectID = s.ID
			break
		}
	}
	if subjectID == 0 {
		t.Fatal("symbol extract.Register missing from index — scan didn't cover internal/extract/")
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	out, err := blast.Compute(ctx, db, subjectID, blast.Options{
		MaxHops:      3,
		IncludeTests: true,
	})
	if err != nil {
		t.Fatalf("blast.Compute on extract.Register: %v", err)
	}
	t.Logf("extract.Register blast: %d direct, %d indirect, %d total affected, risk=%s, reasons=%v",
		len(out.DirectCallers), len(out.IndirectCallers), out.TotalAffected, out.Risk, out.RiskReasons)

	if len(out.DirectCallers) == 0 {
		t.Errorf("DirectCallers empty; expected every registered language extractor's init() to appear")
	}
	switch out.Risk {
	case blast.RiskLow, blast.RiskMedium, blast.RiskHigh:
		// any of the three tiers is a pass — the pitch's criterion
		// is "a Risk value", not a specific tier.
	default:
		t.Errorf("Risk = %q, want one of %q / %q / %q",
			out.Risk, blast.RiskLow, blast.RiskMedium, blast.RiskHigh)
	}
	if len(out.RiskReasons) == 0 {
		t.Error("RiskReasons empty; Card 11 should always populate at least one reason")
	}
}
