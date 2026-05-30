package search

// VectorIndex provides nearest-neighbor search over symbol embedding
// vectors. The interface exists so the concrete index implementation can be
// swapped without touching search orchestration logic; the production
// implementation is flatIndex (exact brute-force).
type VectorIndex interface {
	// Search returns the top-k nearest symbol IDs for the given query
	// vector, ordered by decreasing similarity. Each result includes a
	// cosine similarity score in [-1, 1].
	Search(query []float32, k int) []VectorResult

	// Len returns the number of vectors in the index.
	Len() int
}

// VectorResult is a single nearest-neighbor hit.
type VectorResult struct {
	SymbolID   int64
	Similarity float32
}
