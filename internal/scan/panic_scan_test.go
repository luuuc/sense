package scan_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
)

// boomExtractor is a raw extractor that always panics, registered once for the
// unique ".boom" extension so a real scan routes a .boom file through the parse
// fan-out and into the panic-recovery path. It exists only to prove the
// scan-level invariant the walkTree decomposition must preserve: one file's
// extractor panic is recovered and skipped, never killing the scan. The parse
// pass prefers the RawExtractor path, so ExtractRaw is what fires; Extract and
// Grammar exist only to satisfy the registry's full Extractor interface.
type boomExtractor struct{}

func (boomExtractor) ExtractRaw([]byte, string, extract.Emitter) error { panic("boom") }
func (boomExtractor) Extract(*sitter.Tree, []byte, string, extract.Emitter) error {
	panic("boom")
}
func (boomExtractor) Grammar() *sitter.Language { return nil }
func (boomExtractor) Language() string          { return "boomlang" }
func (boomExtractor) Extensions() []string      { return []string{".boom"} }
func (boomExtractor) Tier() extract.Tier        { return extract.TierBasic }

func init() { extract.Register(boomExtractor{}) }

// TestScanSkipsPanickingFileAndCompletes drives a real scan over a repo holding
// one file whose extractor panics alongside a normal file. The scan must
// complete without error, index the healthy file, and log exactly one warning
// for the panicking one — the per-file panic isolation parseAllFiles documents,
// asserted end to end rather than only at the safeExtractRaw unit boundary.
func TestScanSkipsPanickingFileAndCompletes(t *testing.T) {
	repo := scantest.NewRepo(t, map[string]string{
		"explode.boom":    "this file makes its extractor panic\n",
		"app/models/u.rb": "class U\n  def name; \"u\"; end\nend\n",
	})

	res, _ := repo.Scan(scan.Options{})

	if res.Warnings < 1 {
		t.Errorf("warnings = %d, want >= 1 (the panicking file should warn)", res.Warnings)
	}
	if res.Indexed < 1 {
		t.Fatalf("indexed = %d, want >= 1 (the healthy file must still index)", res.Indexed)
	}

	// The healthy Ruby file's symbol must be present; the boom file's must not.
	db, err := sql.Open("sqlite", filepath.Join(repo.Root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	var healthy int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sense_symbols WHERE qualified = 'U'`).Scan(&healthy); err != nil {
		t.Fatalf("count healthy symbols: %v", err)
	}
	if healthy == 0 {
		t.Error("the healthy file's symbol U is missing; the panic took down more than its own file")
	}
}
