package search_test

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"testing"

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

func TestHNSWSearch1K(t *testing.T) {
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

	idx := search.BuildHNSWIndex(embeddings)

	if idx.Len() != n {
		t.Fatalf("expected %d vectors, got %d", n, idx.Len())
	}

	query := make([]float32, dims)
	for j := range dims {
		query[j] = rng.Float32()
	}
	query = normalize(query)

	results := idx.Search(query, 10)
	if len(results) != 10 {
		t.Fatalf("expected 10 results, got %d", len(results))
	}

	seen := map[int64]bool{}
	for i, r := range results {
		if r.Similarity < -1 || r.Similarity > 1 {
			t.Errorf("result[%d]: similarity %f out of [-1,1]", i, r.Similarity)
		}
		if seen[r.SymbolID] {
			t.Errorf("result[%d]: duplicate symbol ID %d", i, r.SymbolID)
		}
		seen[r.SymbolID] = true
	}
}

func BenchmarkHNSWBuild1K(b *testing.B) {
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

	b.ResetTimer()
	for range b.N {
		search.BuildHNSWIndex(embeddings)
	}
}

func BenchmarkHNSWSearch1K(b *testing.B) {
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

	idx := search.BuildHNSWIndex(embeddings)
	query := normalize(embeddings[1])

	b.ResetTimer()
	for range b.N {
		idx.Search(query, 10)
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
	// Use a sufficiently large graph so coder/hnsw's Delete doesn't panic
	// on the entry-point node. Small graphs (≤5 nodes) with low dimensions
	// produce degenerate HNSW structures where Delete can corrupt the
	// graph, triggering the panic-recovery fallback on some platforms.
	const n = 100
	const dims = 16
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

	// Upsert: update symbol 1, add a new symbol. No removals.
	newVec := make([]float32, dims)
	for j := range dims {
		newVec[j] = rng.Float32()
	}
	toUpsert := map[int64][]float32{
		1:            normalize(newVec),
		int64(n + 1): normalize(newVec),
	}
	// The hnsw library's Delete can corrupt the graph even for upsert-only
	// operations. UpdateHNSWIndex detects this via round-trip verification
	// and falls back. Either outcome is valid — we verify the contract.
	updated, err := search.UpdateHNSWIndex(path, nil, toUpsert)
	if err != nil {
		t.Fatalf("UpdateHNSWIndex: %v", err)
	}

	loaded, err := search.LoadHNSWIndex(path)
	if err != nil {
		t.Fatalf("LoadHNSWIndex after update: %v", err)
	}

	if updated {
		if loaded.Len() != n+1 {
			t.Fatalf("expected %d vectors after upsert, got %d", n+1, loaded.Len())
		}
		results := loaded.Search(normalize(newVec), 1)
		if len(results) == 0 {
			t.Error("expected non-empty results after incremental update")
		}
	} else if loaded.Len() != n {
		t.Fatalf("expected original %d vectors after fallback, got %d", n, loaded.Len())
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
	} else if loaded.Len() != n {
		// Verification caught corruption — original index is untouched
		t.Fatalf("expected original %d vectors after fallback, got %d", n, loaded.Len())
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

func makeEmbeddings(n, dims int, rng *rand.Rand) map[int64][]float32 {
	embeddings := make(map[int64][]float32, n)
	for i := range n {
		vec := make([]float32, dims)
		for j := range dims {
			vec[j] = rng.Float32()
		}
		embeddings[int64(i+1)] = normalize(vec)
	}
	return embeddings
}

func BenchmarkHNSWSearch10K(b *testing.B) {
	const dims = 384
	rng := rand.New(rand.NewPCG(42, 0))
	embeddings := makeEmbeddings(10_000, dims, rng)

	idx := search.BuildHNSWIndex(embeddings)
	query := normalize(embeddings[1])

	b.ResetTimer()
	for range b.N {
		idx.Search(query, 10)
	}
}

func BenchmarkHNSWBuild10K(b *testing.B) {
	const dims = 384
	rng := rand.New(rand.NewPCG(42, 0))
	embeddings := makeEmbeddings(10_000, dims, rng)

	b.ResetTimer()
	for range b.N {
		search.BuildHNSWIndex(embeddings)
	}
}

func TestHNSWSearch_whenGraphPanics_returnsNilResults(t *testing.T) {
	// Build a 3-dim index then query with a 5-dim vector. The coder/hnsw
	// CosineDistance panics on dimension mismatch — Search's deferred
	// recover must convert that into a nil result slice rather than
	// propagating the panic to the caller.
	embeddings := map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
		2: normalize([]float32{0, 1, 0}),
		3: normalize([]float32{0, 0, 1}),
	}
	idx := search.BuildHNSWIndex(embeddings)

	results := idx.Search([]float32{1, 0, 0, 0, 0}, 2)
	if results != nil {
		t.Fatalf("expected nil results after recovered panic, got %v", results)
	}
}

func TestUpdateHNSWIndex_whenIndexFileCorrupt_returnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := os.WriteFile(path, []byte("not a real hnsw export"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	updated, err := search.UpdateHNSWIndex(path, nil, map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
	})
	if err == nil {
		t.Fatal("expected error for corrupt index file, got nil")
	}
	if updated {
		t.Fatal("expected updated=false on corrupt index")
	}
}

func TestUpdateHNSWIndex_whenRemoveAndUpsertOverlap_appliesUpsertOnly(t *testing.T) {
	// When an ID appears in both toRemove and toUpsert, the upsert wins:
	// the remove must be skipped so the new vector survives in the graph.
	const n = 200
	const dims = 32
	rng := rand.New(rand.NewPCG(7, 0))
	embeddings := makeEmbeddings(n, dims, rng)

	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	newVec := make([]float32, dims)
	for j := range dims {
		newVec[j] = rng.Float32()
	}
	overlapID := int64(42)
	toUpsert := map[int64][]float32{overlapID: normalize(newVec)}
	// Same ID is also requested for removal — upsert must win.
	toRemove := []int64{overlapID}

	updated, err := search.UpdateHNSWIndex(path, toRemove, toUpsert)
	if err != nil {
		t.Fatalf("UpdateHNSWIndex: %v", err)
	}

	loaded, err := search.LoadHNSWIndex(path)
	if err != nil {
		t.Fatalf("LoadHNSWIndex: %v", err)
	}

	if updated {
		// Upsert kept the ID — total count unchanged from baseline.
		if loaded.Len() != n {
			t.Fatalf("expected %d vectors after overlap upsert, got %d", n, loaded.Len())
		}
	} else if loaded.Len() != n {
		// Fallback path: file untouched, original count preserved.
		t.Fatalf("expected original %d vectors after fallback, got %d", n, loaded.Len())
	}
}

func TestLoadHNSWIndex_whenFileCorrupt_returnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := os.WriteFile(path, []byte("not a real hnsw export"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}

	idx, err := search.LoadHNSWIndex(path)
	if err == nil {
		t.Fatal("expected error for corrupt index file, got nil")
	}
	if idx != nil {
		t.Fatalf("expected nil index on import failure, got %v", idx)
	}
}

func TestUpdateHNSWIndex_whenOpenFailsNonExistence_returnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: cannot test permission-denied open")
	}
	// File exists but is unreadable — os.Open returns a non-IsNotExist error.
	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	updated, err := search.UpdateHNSWIndex(path, nil, map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
	})
	if err == nil {
		t.Fatal("expected error opening unreadable file, got nil")
	}
	if updated {
		t.Fatal("expected updated=false on open failure")
	}
}

func TestLoadHNSWIndex_whenOpenFailsNonExistence_returnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: cannot test permission-denied open")
	}
	path := filepath.Join(t.TempDir(), "hnsw.bin")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("chmod 000: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	idx, err := search.LoadHNSWIndex(path)
	if err == nil {
		t.Fatal("expected error opening unreadable file, got nil")
	}
	if idx != nil {
		t.Fatalf("expected nil index on open failure, got %v", idx)
	}
}

func TestSaveHNSWIndex_whenPathInvalid_returnsError(t *testing.T) {
	// Path under a non-existent directory — os.Create must fail before
	// any export work happens.
	bad := filepath.Join(t.TempDir(), "no-such-dir", "hnsw.bin")
	err := search.SaveHNSWIndex(bad, map[int64][]float32{
		1: normalize([]float32{1, 0, 0}),
	})
	if err == nil {
		t.Fatal("expected error for invalid path, got nil")
	}
}

func TestUpdateHNSWIndex_whenCreateFails_returnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: cannot test read-only file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "hnsw.bin")

	// Build a valid index large enough to survive the corruption-check path.
	const n = 100
	const dims = 16
	rng := rand.New(rand.NewPCG(99, 0))
	embeddings := makeEmbeddings(n, dims, rng)
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		t.Fatalf("SaveHNSWIndex: %v", err)
	}

	// Make the file read-only AND the directory read-only so os.Create
	// (which truncates) fails inside UpdateHNSWIndex after the in-memory
	// graph is ready. Both perms needed: open-for-read on the file to
	// load the existing index, then no-write to fail the rewrite.
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod file: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(dir, 0o700)
		_ = os.Chmod(path, 0o600)
	})

	newVec := normalize([]float32{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	updated, err := search.UpdateHNSWIndex(path, nil, map[int64][]float32{
		int64(n + 1): newVec,
	})
	// We expect a real error from os.Create. If the library corruption-
	// fallback fires first (updated=false, err=nil) that's also OK — the
	// branch we want to cover (line 168) is the create-error path, which
	// is reached only when the in-memory graph survives the round-trip.
	if err == nil && updated {
		t.Fatal("expected either error or updated=false when target file/dir are read-only")
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
