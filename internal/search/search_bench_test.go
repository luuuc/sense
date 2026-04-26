package search_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/benchmark"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

func BenchmarkSearch(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "search-bench.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = adapter.Close() })

	fix, err := benchmark.BuildFixture(ctx, adapter, 500)
	if err != nil {
		b.Fatalf("BuildFixture: %v", err)
	}

	embeddings, err := adapter.LoadEmbeddings(ctx)
	if err != nil {
		b.Fatalf("LoadEmbeddings: %v", err)
	}

	vectorIdx := search.BuildHNSWIndex(embeddings)
	engine := search.NewEngine(adapter, vectorIdx, &fixedEmbedder{})
	_ = fix.SymbolIDs

	b.Run("keyword", func(b *testing.B) {
		for b.Loop() {
			_, _, _, _ = engine.Search(ctx, search.Options{
				Query: "Symbol0",
				Limit: 10,
			})
		}
	})

	b.Run("hybrid", func(b *testing.B) {
		for b.Loop() {
			_, _, _, _ = engine.Search(ctx, search.Options{
				Query: "how does Symbol0 work",
				Limit: 10,
			})
		}
	})
}

type fixedEmbedder struct{}

func (f *fixedEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	vecs := make([][]float32, len(inputs))
	for i := range inputs {
		vec := make([]float32, 384)
		vec[0] = 0.5
		vec[1] = 0.3
		vecs[i] = vec
	}
	return vecs, nil
}

func (f *fixedEmbedder) Close() error { return nil }
