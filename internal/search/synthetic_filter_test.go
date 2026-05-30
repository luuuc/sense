package search_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestSyntheticSymbolsFilteredFromSearch proves the synthetic value-object
// base symbols (ruby-core:Struct / ruby-core:Data) never surface in search
// results, while an ordinary same-named symbol still does — the control
// that keeps this from passing on an over-broad filter.
func TestSyntheticSymbolsFilteredFromSearch(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	a, err := sqlite.Open(context.Background(), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/struct.rb", Language: "ruby",
		Hash: "h", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	// The synthetic plumbing symbol and an ordinary symbol literally named
	// "Struct" — both match an FTS query for "Struct".
	for _, s := range []model.Symbol{
		{FileID: fid, Name: "Struct", Qualified: "ruby-core:Struct", Kind: "class", LineStart: 1, LineEnd: 1},
		{FileID: fid, Name: "Struct", Qualified: "Geometry::Struct", Kind: "class", LineStart: 2, LineEnd: 3},
	} {
		if _, err := a.WriteSymbol(ctx, &s); err != nil {
			t.Fatalf("write symbol %q: %v", s.Qualified, err)
		}
	}

	engine, embedder, err := search.BuildEngine(ctx, a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if embedder != nil {
		defer func() { _ = embedder.Close() }()
	}

	results, _, err := engine.Search(ctx, search.Options{Query: "Struct", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	var sawReal, sawSynthetic bool
	for _, r := range results {
		switch r.Qualified {
		case "ruby-core:Struct":
			sawSynthetic = true
		case "Geometry::Struct":
			sawReal = true
		}
	}
	if sawSynthetic {
		t.Error("synthetic ruby-core:Struct leaked into search results")
	}
	if !sawReal {
		t.Error("ordinary Geometry::Struct missing from search results (filter is over-broad)")
	}
}

// TestRouteSymbolsFilteredFromSearch proves synthetic route:* helper symbols
// never surface in search, while an ordinary same-named app method does.
func TestRouteSymbolsFilteredFromSearch(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	a, err := sqlite.Open(context.Background(), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	ctx := context.Background()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "config/routes.rb", Language: "ruby",
		Hash: "h", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	for _, s := range []model.Symbol{
		{FileID: fid, Name: "orders_path", Qualified: "route:orders_path", Kind: "constant", LineStart: 1, LineEnd: 1},
		{FileID: fid, Name: "orders_path", Qualified: "Billing#orders_path", Kind: "method", LineStart: 2, LineEnd: 3},
	} {
		if _, err := a.WriteSymbol(ctx, &s); err != nil {
			t.Fatalf("write symbol %q: %v", s.Qualified, err)
		}
	}

	engine, embedder, err := search.BuildEngine(ctx, a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if embedder != nil {
		defer func() { _ = embedder.Close() }()
	}

	results, _, err := engine.Search(ctx, search.Options{Query: "orders_path", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	var sawReal, sawSynthetic bool
	for _, r := range results {
		switch r.Qualified {
		case "route:orders_path":
			sawSynthetic = true
		case "Billing#orders_path":
			sawReal = true
		}
	}
	if sawSynthetic {
		t.Error("synthetic route:orders_path leaked into search results")
	}
	if !sawReal {
		t.Error("ordinary Billing#orders_path missing from search results (filter is over-broad)")
	}
}
