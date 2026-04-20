package search_test

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/search"
)

func TestHNSWSearchBasic(t *testing.T) {
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
		4: normalize([]float32{0.9, 0.1, 0}),
	}

	idx := search.BuildHNSWIndex(embeddings)
	if idx.Len() != 4 {
		t.Fatalf("expected 4 vectors, got %d", idx.Len())
	}

	results := idx.Search(normalize([]float32{1, 0, 0}), 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	ids := map[int64]bool{}
	for _, r := range results {
		ids[r.SymbolID] = true
	}
	if !ids[1] || !ids[4] {
		t.Errorf("expected symbols 1 and 4 in top-2, got %v", ids)
	}
	if results[0].Similarity < 0.9 {
		t.Errorf("top result similarity %f too low", results[0].Similarity)
	}
}

func TestHNSWSearchEmpty(t *testing.T) {
	idx := search.BuildHNSWIndex(nil)
	results := idx.Search([]float32{1, 0, 0}, 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results from empty index, got %d", len(results))
	}
}

func TestHNSWSearchKLargerThanIndex(t *testing.T) {
	embeddings := map[int64][]float32{
		1: {1, 0, 0},
		2: {0, 1, 0},
	}
	idx := search.BuildHNSWIndex(embeddings)
	results := idx.Search([]float32{1, 0, 0}, 10)
	if len(results) != 2 {
		t.Errorf("expected 2 results (clamped to index size), got %d", len(results))
	}
}

func TestHNSWPerformance1K(t *testing.T) {
	const n = 1000
	const dims = 384
	rng := rand.New(rand.NewPCG(42, 0))

	embeddings := make(map[int64][]float32, n)
	for i := range n {
		vec := make([]float32, dims)
		for j := range dims {
			vec[j] = rng.Float32()
		}
		embeddings[int64(i+1)] = normalize(vec)
	}

	start := time.Now()
	idx := search.BuildHNSWIndex(embeddings)
	loadTime := time.Since(start)

	if idx.Len() != n {
		t.Fatalf("expected %d vectors, got %d", n, idx.Len())
	}

	query := make([]float32, dims)
	for j := range dims {
		query[j] = rng.Float32()
	}
	query = normalize(query)

	start = time.Now()
	results := idx.Search(query, 10)
	searchTime := time.Since(start)

	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}

	t.Logf("1K symbols: load=%v, search=%v", loadTime, searchTime)

	if loadTime > 500*time.Millisecond {
		t.Errorf("load time %v exceeds 500ms budget", loadTime)
	}
	if searchTime > 50*time.Millisecond {
		t.Errorf("search time %v exceeds 50ms budget", searchTime)
	}
}

func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	norm := float32(math.Sqrt(sum))
	if norm == 0 {
		return v
	}
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x / norm
	}
	return out
}
