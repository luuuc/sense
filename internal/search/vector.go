package search

// VectorIndex provides approximate nearest-neighbor search over
// symbol embedding vectors. The interface exists so the HNSW library
// can be swapped without touching search orchestration logic.
type VectorIndex interface {
	// Search returns the top-k nearest symbol IDs for the given query
	// vector, ordered by decreasing similarity. Each result includes a
	// similarity score in [0, 1].
	Search(query []float32, k int) []VectorResult

	// Len returns the number of vectors in the index.
	Len() int
}

// VectorResult is a single nearest-neighbor hit.
type VectorResult struct {
	SymbolID   int64
	Similarity float32
}
