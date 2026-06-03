package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestFTS5DocstringStaleRowUpsert pins the FTS5 contract that pitch
// 25-07's Card 7 calls out: every prior-version scan wrote
// `docstring=”` rows into sense_symbols_fts (the column existed long
// before any extractor populated it). When a re-scan UPSERTs the same
// (file_id, qualified) symbol with a new non-empty docstring, the
// FTS5 update trigger must overwrite the stale empty row so a MATCH
// against words that exist ONLY in the new docstring returns the
// expected symbol_id.
//
// The test deliberately renames the symbol on UPSERT to a generic
// term that doesn't contain the search words — that way a passing
// assertion *can only* be the docstring-driven match, not a stale
// name-driven match.
//
// Companion assertion: the same MATCH on the pre-UPSERT (stale-empty)
// row returns nothing, proving the test isn't passing by accident on
// the original name or snippet.
func TestFTS5DocstringStaleRowUpsert(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "billing/payment.go", Language: "go",
		Hash: "v1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed: symbol named `validate_payment` with EMPTY docstring,
	// matching the on-disk state every prior-version index is in.
	seeded := &model.Symbol{
		FileID: fid, Name: "validate_payment", Qualified: "billing.validate_payment",
		Kind: "function", LineStart: 1, LineEnd: 10,
		Snippet:   "func validate_payment() {}",
		Docstring: "",
	}
	seededID, err := a.WriteSymbol(ctx, seeded)
	if err != nil {
		t.Fatal(err)
	}

	// Sanity: the seeded row must actually exist in sense_symbols_fts.
	// Without this guard a regression that stopped inserting empty-
	// docstring rows entirely (rather than inserting them as stale) would
	// still pass the post-UPSERT assertion, because the UPSERT would
	// happen to insert a fresh non-stale row. Asserting an FTS hit on
	// the symbol's pre-UPSERT name pins the "row exists" precondition.
	seedHit, err := a.KeywordSearch(ctx, "validate_payment", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(seedHit) != 1 || seedHit[0].SymbolID != seededID {
		t.Fatalf("seed precondition: expected FTS5 row for validate_payment with id %d, got %+v", seededID, seedHit)
	}

	// Companion: MATCH against words that will only be in the NEW
	// docstring. Pre-UPSERT, the row's docstring is "", name is
	// "validate_payment", snippet is "func validate_payment() {}".
	// None of those contain "amount before charging".
	pre, err := a.KeywordSearch(ctx, "amount before charging", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre) != 0 {
		t.Fatalf("pre-UPSERT: expected 0 results for stale row, got %d (rows: %+v)", len(pre), pre)
	}

	// UPSERT the same (file_id, qualified) with a docstring containing
	// the search words AND a generic name that doesn't, so the only
	// possible match path is the docstring column.
	updated := &model.Symbol{
		FileID: fid, Name: "check", Qualified: "billing.validate_payment",
		Kind: "function", LineStart: 1, LineEnd: 10,
		Snippet:   "func check() {}",
		Docstring: "validate payment amount before charging",
	}
	updatedID, err := a.WriteSymbol(ctx, updated)
	if err != nil {
		t.Fatal(err)
	}
	if updatedID != seededID {
		t.Fatalf("UPSERT returned new id %d (want same as seed %d) — upsert key broken",
			updatedID, seededID)
	}

	post, err := a.KeywordSearch(ctx, "amount before charging", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(post) != 1 {
		t.Fatalf("post-UPSERT: expected 1 result, got %d (rows: %+v)", len(post), post)
	}
	if post[0].SymbolID != seededID {
		t.Errorf("post-UPSERT: symbol_id = %d, want %d (the seeded row, proving the FTS update trigger fired on UPSERT)",
			post[0].SymbolID, seededID)
	}
}
