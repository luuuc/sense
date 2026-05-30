package search

import (
	"math"
	"runtime"
	"sort"
	"sync"
)

// flatIndex is an exact, brute-force VectorIndex. It holds every vector in
// one contiguous row-major []float32 buffer (n*dim) and scans the whole set
// in parallel for each query, returning the TRUE top-k by cosine similarity.
//
// There is no approximation: unlike a graph index, a flat scan cannot skip a
// correct neighbour, so recall is 100%. Vectors are L2-normalized at build
// time and the query is normalized at search time, so cosine similarity is a
// plain dot product. For the corpus sizes Sense indexes (tens to a few
// hundred thousand symbols) a parallel contiguous scan is fast enough that
// the recall guarantee is worth more than sub-linear search.
type flatIndex struct {
	ids  []int64   // ids[r] is the symbol ID of row r
	vecs []float32 // n*dim, row-major, L2-normalized
	dim  int
}

// BuildFlatIndex constructs an exact vector index from symbol ID → vector
// pairs. Vectors are copied into a single contiguous buffer and normalized
// so search reduces to a dot product. Vectors whose length does not match
// the index dimension (taken from the first vector inserted) are skipped.
//
// The embedder already L2-normalizes its output, so the stored vectors are
// effectively unit-norm; re-normalizing here is defensive, not required —
// it keeps the index correct (true cosine) regardless of how the vectors
// were produced, at a one-time O(n*dim) build cost.
func BuildFlatIndex(embeddings map[int64][]float32) VectorIndex {
	f := &flatIndex{}
	if len(embeddings) == 0 {
		return f
	}

	// Iterate ids in ascending order so the row layout is deterministic;
	// this keeps tie-breaking between equal-similarity rows stable.
	ids := make([]int64, 0, len(embeddings))
	for id := range embeddings {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	for _, id := range ids {
		vec := embeddings[id]
		if len(vec) == 0 {
			continue
		}
		if f.dim == 0 {
			f.dim = len(vec)
			f.ids = make([]int64, 0, len(embeddings))
			f.vecs = make([]float32, 0, len(embeddings)*f.dim)
		}
		if len(vec) != f.dim {
			continue
		}
		f.ids = append(f.ids, id)
		f.vecs = appendNormalized(f.vecs, vec)
	}
	return f
}

// appendNormalized appends the L2-normalized form of vec to dst and returns
// the extended slice. A zero-magnitude vector is appended unchanged.
func appendNormalized(dst, vec []float32) []float32 {
	var sum float64
	for _, x := range vec {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return append(dst, vec...)
	}
	inv := float32(1 / math.Sqrt(sum))
	for _, x := range vec {
		dst = append(dst, x*inv)
	}
	return dst
}

// Search returns the exact top-k symbols by cosine similarity to query,
// ordered by decreasing similarity. Returns nil when the index is empty,
// k <= 0, or the query dimension does not match the index.
func (f *flatIndex) Search(query []float32, k int) []VectorResult {
	n := len(f.ids)
	if n == 0 || f.dim == 0 || k <= 0 || len(query) != f.dim {
		return nil
	}
	if k > n {
		k = n
	}

	q := normalizeQuery(query)

	workers := runtime.GOMAXPROCS(0)
	if workers < 1 {
		workers = 1
	}
	if workers > n {
		workers = n
	}

	// Each worker scans a contiguous row range and keeps its own bounded
	// top-k heap, so no shared state is touched on the hot path.
	rowsPer := (n + workers - 1) / workers
	locals := make([]*topKHeap, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		start := w * rowsPer
		if start >= n {
			break
		}
		end := start + rowsPer
		if end > n {
			end = n
		}
		wg.Add(1)
		go func(w, start, end int) {
			defer wg.Done()
			h := newTopKHeap(k)
			for r := start; r < end; r++ {
				row := f.vecs[r*f.dim : (r+1)*f.dim]
				h.push(f.ids[r], dot(q, row))
			}
			locals[w] = h
		}(w, start, end)
	}
	wg.Wait()

	// Merge the per-worker heaps into one bounded top-k.
	merged := newTopKHeap(k)
	for _, h := range locals {
		if h == nil {
			continue
		}
		for i := range h.ids {
			merged.push(h.ids[i], h.sims[i])
		}
	}

	return merged.sortedDescending()
}

// Len returns the number of vectors in the index.
func (f *flatIndex) Len() int { return len(f.ids) }

// normalizeQuery returns an L2-normalized copy of v so the caller's slice is
// never mutated. A zero-magnitude vector is returned as an unmodified copy.
func normalizeQuery(v []float32) []float32 {
	out := make([]float32, len(v))
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		copy(out, v)
		return out
	}
	inv := float32(1 / math.Sqrt(sum))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

// dot is the inner product of two equal-length vectors. For L2-normalized
// inputs it equals cosine similarity.
func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// topKHeap keeps the k largest (id, similarity) pairs seen as a binary
// min-heap keyed by similarity, so the smallest survivor sits at index 0 and
// is evicted in O(log k) when a larger candidate arrives. Bounding the heap
// at k avoids sorting the full corpus.
//
// It is hand-rolled rather than container/heap on purpose: this sits on the
// per-query hot path over up to ~1M rows, and the stdlib heap's interface
// dispatch plus any-boxing of each pushed element would allocate and slow the
// inner loop. Two parallel slices keyed by similarity keep it allocation-free.
type topKHeap struct {
	k    int
	ids  []int64
	sims []float32
}

func newTopKHeap(k int) *topKHeap {
	return &topKHeap{k: k, ids: make([]int64, 0, k), sims: make([]float32, 0, k)}
}

func (h *topKHeap) push(id int64, sim float32) {
	if len(h.sims) < h.k {
		h.ids = append(h.ids, id)
		h.sims = append(h.sims, sim)
		h.siftUp(len(h.sims) - 1)
		return
	}
	if sim <= h.sims[0] {
		return
	}
	h.ids[0] = id
	h.sims[0] = sim
	h.siftDown(0)
}

func (h *topKHeap) siftUp(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if h.sims[i] >= h.sims[parent] {
			return
		}
		h.swap(i, parent)
		i = parent
	}
}

func (h *topKHeap) siftDown(i int) {
	n := len(h.sims)
	for {
		smallest, l, r := i, 2*i+1, 2*i+2
		if l < n && h.sims[l] < h.sims[smallest] {
			smallest = l
		}
		if r < n && h.sims[r] < h.sims[smallest] {
			smallest = r
		}
		if smallest == i {
			return
		}
		h.swap(i, smallest)
		i = smallest
	}
}

func (h *topKHeap) swap(i, j int) {
	h.sims[i], h.sims[j] = h.sims[j], h.sims[i]
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
}

// sortedDescending drains the heap into a result slice ordered by decreasing
// similarity, breaking ties by ascending symbol ID for deterministic output.
func (h *topKHeap) sortedDescending() []VectorResult {
	out := make([]VectorResult, len(h.ids))
	for i := range h.ids {
		out[i] = VectorResult{SymbolID: h.ids[i], Similarity: h.sims[i]}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Similarity != out[j].Similarity {
			return out[i].Similarity > out[j].Similarity
		}
		return out[i].SymbolID < out[j].SymbolID
	})
	return out
}
