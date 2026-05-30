package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestDocumentFrequency verifies the per-term symbol counts used by the
// generic-token penalty: a token shared by many symbols (via the
// decomposed name_parts column) has high DF; a rare domain token has low
// DF; an absent token is 0; and a term that sanitizes to empty is 0
// without erroring.
func TestDocumentFrequency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "df.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/ui.js", Language: "javascript",
		Hash: "h1", Symbols: 4, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Three symbols decompose to include "prevent"; one rare symbol
	// includes "listing".
	for _, s := range []model.Symbol{
		{FileID: fid, Name: "preventClose", Qualified: "ui.preventClose", Kind: "function", LineStart: 1, LineEnd: 2},
		{FileID: fid, Name: "preventEscape", Qualified: "ui.preventEscape", Kind: "function", LineStart: 3, LineEnd: 4},
		{FileID: fid, Name: "preventResize", Qualified: "ui.preventResize", Kind: "function", LineStart: 5, LineEnd: 6},
		{FileID: fid, Name: "ListingGuard", Qualified: "shop.ListingGuard", Kind: "class", LineStart: 7, LineEnd: 8},
	} {
		sym := s
		if _, err := a.WriteSymbol(ctx, &sym); err != nil {
			t.Fatal(err)
		}
	}

	// "prevent" appears twice to exercise the dedup fast-path.
	df, err := a.DocumentFrequency(ctx, []string{"prevent", "prevent", "listing", "absentxyz", "  "})
	if err != nil {
		t.Fatal(err)
	}

	if df["prevent"] != 3 {
		t.Errorf("DF(prevent) = %d, want 3", df["prevent"])
	}
	if df["listing"] != 1 {
		t.Errorf("DF(listing) = %d, want 1", df["listing"])
	}
	if df["absentxyz"] != 0 {
		t.Errorf("DF(absentxyz) = %d, want 0", df["absentxyz"])
	}
	if df["  "] != 0 {
		t.Errorf("DF(whitespace term) = %d, want 0", df["  "])
	}
}

// TestDocumentFrequencyQueryError verifies the MATCH-query error path:
// a closed database makes the per-term COUNT(*) fail, and the error is
// wrapped with the offending term.
func TestDocumentFrequencyQueryError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "df.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := a.DocumentFrequency(ctx, []string{"prevent"}); err == nil {
		t.Fatal("expected error from DocumentFrequency on closed db, got nil")
	}
}
