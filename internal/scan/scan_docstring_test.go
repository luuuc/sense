package scan_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// docstrTestExtractor drives the scan harness without coupling Card 1's
// wiring tests to a real per-language extractor (the per-language cards
// land later in this pitch). It emits exactly one symbol per file whose
// Docstring is the file's contents verbatim, so a round-trip assertion
// compares the on-disk source bytes to what came back out of the index.
//
// The ".docstrwire" extension is reserved for this test file. Any other
// test attempting to register an extractor for that extension will trip
// the duplicate-registration panic in extract.Register.
type docstrTestExtractor struct{}

func (docstrTestExtractor) Language() string          { return "docstrwiretest" }
func (docstrTestExtractor) Extensions() []string      { return []string{".docstrwire"} }
func (docstrTestExtractor) Grammar() *sitter.Language { return nil }
func (docstrTestExtractor) Tier() extract.Tier        { return extract.TierBasic }
func (docstrTestExtractor) Extract(*sitter.Tree, []byte, string, extract.Emitter) error {
	return nil
}

func (docstrTestExtractor) ExtractRaw(source []byte, filePath string, emit extract.Emitter) error {
	name := strings.TrimSuffix(filepath.Base(filePath), ".docstrwire")
	return emit.Symbol(extract.EmittedSymbol{
		Name:      name,
		Qualified: name,
		Kind:      model.KindFunction,
		LineStart: 1,
		LineEnd:   1,
		Docstring: string(source),
	})
}

func init() { extract.Register(docstrTestExtractor{}) }

// querySymbol opens the scanned index and returns the single symbol with
// the given qualified name, or fails the test if zero or more than one
// matches.
func querySymbol(t *testing.T, root, qualified string) model.Symbol {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	rows, err := a.Query(ctx, index.Filter{Name: qualified})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("Query(name=%q): got %d rows, want 1", qualified, len(rows))
	}
	return rows[0]
}

// TestDocstringRoundTrip pins the wiring added in this card: a non-empty
// Docstring set by an extractor must travel through scan.Run into the
// SQLite row and read back byte-for-byte. The test extractor emits the
// file's contents as the docstring, so this assertion would fail if any
// step truncated, normalised, or stripped the value.
func TestDocstringRoundTrip(t *testing.T) {
	root := t.TempDir()
	const docstring = "validate payment amount before charging the card"
	src := []byte(docstring)
	if err := os.WriteFile(filepath.Join(root, "check.docstrwire"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	got := querySymbol(t, root, "check")
	if got.Docstring != docstring {
		t.Errorf("Docstring round-trip mismatch\nhave: %q\nwant: %q", got.Docstring, docstring)
	}
}

// TestDocstringEmptyRoundTrip pins the zero-value path: a symbol whose
// extractor emits Docstring="" must read back as the empty string — not
// NULL, not a fallback, and not a panic. This guards against a future
// change that swaps the column for sql.NullString or adds a "missing
// docstring" sentinel: either would silently regress every extractor
// that legitimately has no comment to attach.
func TestDocstringEmptyRoundTrip(t *testing.T) {
	root := t.TempDir()
	// Empty file → extractor emits Docstring="".
	if err := os.WriteFile(filepath.Join(root, "bare.docstrwire"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("scan.Run: %v", err)
	}

	got := querySymbol(t, root, "bare")
	if got.Docstring != "" {
		t.Errorf("Docstring on empty-input symbol = %q, want \"\"", got.Docstring)
	}
}
