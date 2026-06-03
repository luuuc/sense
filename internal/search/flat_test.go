package search_test

import (
	"math"
	"math/rand/v2"
	"sort"
	"testing"

	"github.com/luuuc/sense/internal/search"
)

func TestFlatSearchBasic(t *testing.T) {
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
		4: normalize([]float32{0.9, 0.1, 0}),
	}

	idx := search.BuildFlatIndex(embeddings)
	if idx.Len() != 4 {
		t.Fatalf("expected 4 vectors, got %d", idx.Len())
	}

	results := idx.Search(normalize([]float32{1, 0, 0}), 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	// Exact order: symbol 1 (cosine 1.0) then symbol 4 (cosine ~0.994).
	if results[0].SymbolID != 1 {
		t.Errorf("top result = %d, want symbol 1", results[0].SymbolID)
	}
	if results[1].SymbolID != 4 {
		t.Errorf("second result = %d, want symbol 4", results[1].SymbolID)
	}
	if results[0].Similarity < results[1].Similarity {
		t.Errorf("results not in descending similarity order: %v", results)
	}
	if math.Abs(float64(results[0].Similarity)-1.0) > 1e-5 {
		t.Errorf("top similarity = %f, want ~1.0", results[0].Similarity)
	}
}

func TestFlatSearchEmpty(t *testing.T) {
	idx := search.BuildFlatIndex(nil)
	if idx.Len() != 0 {
		t.Fatalf("expected empty index, got len %d", idx.Len())
	}
	if results := idx.Search([]float32{1, 0, 0}, 5); results != nil {
		t.Errorf("expected nil results from empty index, got %v", results)
	}
}

func TestFlatSearchKLargerThanIndex(t *testing.T) {
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
	}
	idx := search.BuildFlatIndex(embeddings)
	results := idx.Search([]float32{1, 0, 0}, 10)
	if len(results) != 2 {
		t.Errorf("expected 2 results (clamped to index size), got %d", len(results))
	}
}

func TestFlatSearchNonPositiveK(t *testing.T) {
	idx := search.BuildFlatIndex(map[int64][]float32{1: {1, 0, 0}})
	if results := idx.Search([]float32{1, 0, 0}, 0); results != nil {
		t.Errorf("k=0 should return nil, got %v", results)
	}
	if results := idx.Search([]float32{1, 0, 0}, -3); results != nil {
		t.Errorf("k<0 should return nil, got %v", results)
	}
}

func TestFlatSearchDimensionMismatch(t *testing.T) {
	// A query whose dimension differs from the index returns nil rather than
	// panicking on the mismatched dot product.
	idx := search.BuildFlatIndex(map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
	})
	if results := idx.Search([]float32{1, 0, 0, 0, 0}, 2); results != nil {
		t.Fatalf("dimension mismatch should return nil, got %v", results)
	}
}

func TestFlatBuildSkipsMismatchedAndEmptyVectors(t *testing.T) {
	// First inserted (lowest id) sets dim=3. A 2-dim vector and an empty
	// vector are skipped; the index keeps only the dimension-3 rows.
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: {1, 0}, // wrong dim — skipped
		3: {},     // empty — skipped
		4: normalize([]float32{0, 1, 0}),
	}
	idx := search.BuildFlatIndex(embeddings)
	if idx.Len() != 2 {
		t.Fatalf("expected 2 valid vectors, got %d", idx.Len())
	}
	results := idx.Search(normalize([]float32{0, 1, 0}), 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].SymbolID != 4 {
		t.Errorf("top result = %d, want symbol 4", results[0].SymbolID)
	}
}

func TestFlatSearchZeroMagnitudeVector(t *testing.T) {
	// A zero vector has undefined cosine; it must not produce NaN that breaks
	// ordering, and a zero query must not panic.
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: {0, 0, 0},
	}
	idx := search.BuildFlatIndex(embeddings)
	results := idx.Search([]float32{0, 0, 0}, 2)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if math.IsNaN(float64(r.Similarity)) {
			t.Errorf("similarity is NaN for symbol %d", r.SymbolID)
		}
	}
}

// TestFlatSearchRecallGuarantee is the defining invariant of a flat index:
// for a seeded corpus, the index's returned top-k MUST equal the brute-force
// true top-k — same IDs in the same order. An approximate index cannot pass
// this; a flat scan must, every time.
func TestFlatSearchRecallGuarantee(t *testing.T) {
	const (
		n    = 2000
		dims = 384
		k    = 50
		runs = 20
	)
	rng := rand.New(rand.NewPCG(2024, 7))
	embeddings := makeEmbeddings(n, dims, rng)
	idx := search.BuildFlatIndex(embeddings)

	for run := 0; run < runs; run++ {
		query := randomVec(dims, rng)

		want := bruteForceTopK(embeddings, query, k)
		got := idx.Search(query, k)

		if len(got) != k {
			t.Fatalf("run %d: got %d results, want %d", run, len(got), k)
		}
		for i := range want {
			if got[i].SymbolID != want[i].SymbolID {
				t.Fatalf("run %d rank %d: got symbol %d (sim %f), want symbol %d (sim %f)",
					run, i, got[i].SymbolID, got[i].Similarity, want[i].SymbolID, want[i].Similarity)
			}
			if math.Abs(float64(got[i].Similarity)-float64(want[i].Similarity)) > 1e-5 {
				t.Fatalf("run %d rank %d: similarity %f != brute force %f",
					run, i, got[i].Similarity, want[i].Similarity)
			}
		}
	}
}

// bruteForceTopK is the reference implementation: exact cosine over every
// vector, sorted by descending similarity then ascending ID — the same
// tie-break flatIndex uses.
func bruteForceTopK(embeddings map[int64][]float32, query []float32, k int) []search.VectorResult {
	q := normalize(query)
	all := make([]search.VectorResult, 0, len(embeddings))
	for id, vec := range embeddings {
		v := normalize(vec)
		var s float32
		for i := range v {
			s += v[i] * q[i]
		}
		all = append(all, search.VectorResult{SymbolID: id, Similarity: s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Similarity != all[j].Similarity {
			return all[i].Similarity > all[j].Similarity
		}
		return all[i].SymbolID < all[j].SymbolID
	})
	if k > len(all) {
		k = len(all)
	}
	return all[:k]
}

func randomVec(dims int, rng *rand.Rand) []float32 {
	v := make([]float32, dims)
	for j := range dims {
		v[j] = rng.Float32()*2 - 1
	}
	return v
}

func makeEmbeddings(n, dims int, rng *rand.Rand) map[int64][]float32 {
	embeddings := make(map[int64][]float32, n)
	for i := range n {
		embeddings[int64(i+1)] = normalize(randomVec(dims, rng))
	}
	return embeddings
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

func benchmarkFlatSearch(b *testing.B, n int) {
	const dims = 384
	rng := rand.New(rand.NewPCG(42, 0))
	embeddings := makeEmbeddings(n, dims, rng)
	idx := search.BuildFlatIndex(embeddings)
	query := randomVec(dims, rng)

	b.ResetTimer()
	for range b.N {
		idx.Search(query, 50)
	}
}

func BenchmarkFlatSearch75k(b *testing.B)  { benchmarkFlatSearch(b, 75_000) }
func BenchmarkFlatSearch250k(b *testing.B) { benchmarkFlatSearch(b, 250_000) }
func BenchmarkFlatSearch1M(b *testing.B)   { benchmarkFlatSearch(b, 1_000_000) }
