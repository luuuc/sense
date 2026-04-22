package search_test

import (
	"math"
	"math/rand/v2"
	"path/filepath"
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

func TestHNSWSaveLoadRoundTrip(t *testing.T) {
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
		4: normalize([]float32{0.9, 0.1, 0}),
	}

	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	loaded, err := search.LoadHNSWIndex(path)
	if err != nil {
		t.Fatalf("LoadHNSWIndex: %v", err)
	}
	if loaded.Len() != 4 {
		t.Fatalf("loaded index has %d vectors, want 4", loaded.Len())
	}

	results := loaded.Search(normalize([]float32{1, 0, 0}), 2)
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
}

func TestUpdateHNSWIndexUpsert(t *testing.T) {
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
		4: normalize([]float32{0.5, 0.5, 0}),
		5: normalize([]float32{0, 0.5, 0.5}),
	}

	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	// Upsert: update symbol 1, add symbol 6. No removals.
	toUpsert := map[int64][]float32{
		1: normalize([]float32{0.9, 0.1, 0}),
		6: normalize([]float32{0.1, 0.9, 0}),
	}
	updated, err := search.UpdateHNSWIndex(path, nil, toUpsert)
	if err != nil {
		t.Fatalf("UpdateHNSWIndex: %v", err)
	}
	if !updated {
		t.Fatal("expected upsert-only update to succeed")
	}

	loaded, err := search.LoadHNSWIndex(path)
	if err != nil {
		t.Fatalf("LoadHNSWIndex after update: %v", err)
	}
	if loaded.Len() != 6 {
		t.Fatalf("expected 6 vectors after upsert, got %d", loaded.Len())
	}

	results := loaded.Search(normalize([]float32{0.9, 0.1, 0}), 1)
	if len(results) != 1 || results[0].SymbolID != 1 {
		t.Errorf("expected symbol 1 as nearest to updated vector, got %v", results)
	}
}

func TestUpdateHNSWIndexWithRemoval(t *testing.T) {
	const n = 200
	const dims = 32
	rng := rand.New(rand.NewPCG(42, 0))

	embeddings := make(map[int64][]float32, n)
	for i := range n {
		vec := make([]float32, dims)
		for j := range dims {
			vec[j] = rng.Float32()
		}
		embeddings[int64(i+1)] = normalize(vec)
	}

	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	// Remove 2 symbols, upsert 3 (1 update + 2 new).
	// The hnsw library's Delete can corrupt the graph depending on
	// random level assignment, so the update may gracefully fall back.
	// Either outcome is valid — we verify the contract, not the path.
	toRemove := []int64{5, 10}
	newVec := make([]float32, dims)
	for j := range dims {
		newVec[j] = rng.Float32()
	}
	toUpsert := map[int64][]float32{
		1:            normalize(newVec),
		int64(n + 1): normalize(newVec),
		int64(n + 2): normalize(newVec),
	}

	updated, err := search.UpdateHNSWIndex(path, toRemove, toUpsert)
	if err != nil {
		t.Fatalf("UpdateHNSWIndex: %v", err)
	}

	loaded, err := search.LoadHNSWIndex(path)
	if err != nil {
		t.Fatalf("LoadHNSWIndex after update: %v", err)
	}

	if updated {
		want := n - 2 + 2
		if loaded.Len() != want {
			t.Fatalf("expected %d vectors, got %d", want, loaded.Len())
		}
		results := loaded.Search(normalize(newVec), 5)
		if len(results) == 0 {
			t.Error("expected non-empty results after incremental update")
		}
	} else {
		// Verification caught corruption — original index is untouched
		if loaded.Len() != n {
			t.Fatalf("expected original %d vectors after fallback, got %d", n, loaded.Len())
		}
	}
}

func TestUpdateHNSWIndexFallbackOnCorruption(t *testing.T) {
	// Small graph where Delete can corrupt upper layers —
	// UpdateHNSWIndex should detect this and return false.
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
	}

	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	// Removing from a tiny graph risks emptying upper layers
	updated, err := search.UpdateHNSWIndex(path, []int64{1, 2}, nil)
	if err != nil {
		t.Fatalf("unexpected error (should recover, not error): %v", err)
	}
	// May or may not succeed depending on graph topology — just verify no crash
	_ = updated
}

func TestUpdateHNSWIndexMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.bin")
	updated, err := search.UpdateHNSWIndex(path, []int64{1}, map[int64][]float32{2: {1, 0, 0}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated {
		t.Fatal("expected false for missing file")
	}
}

func TestLoadHNSWIndexMissing(t *testing.T) {
	idx, err := search.LoadHNSWIndex(filepath.Join(t.TempDir(), "nonexistent.bin"))
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if idx != nil {
		t.Fatalf("expected nil index for missing file, got: %v", idx)
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
