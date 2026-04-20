package search

import (
	"github.com/coder/hnsw"
)

// hnswIndex wraps coder/hnsw as a VectorIndex. The graph is keyed by
// symbol ID (int64) and uses cosine distance. It is built eagerly from
// all embeddings in the database and rebuilt on scan completion.
type hnswIndex struct {
	graph *hnsw.Graph[int64]
}

// BuildHNSWIndex creates an HNSW index from a set of symbol ID → vector
// pairs. This is the startup path: load all embeddings from SQLite,
// insert them into the graph in one batch. Returns a VectorIndex so
// callers depend on the interface, not the concrete type.
func BuildHNSWIndex(embeddings map[int64][]float32) VectorIndex {
	g := hnsw.NewGraph[int64]()
	g.Distance = hnsw.CosineDistance
	idx := &hnswIndex{graph: g}
	nodes := make([]hnsw.Node[int64], 0, len(embeddings))
	for id, vec := range embeddings {
		nodes = append(nodes, hnsw.MakeNode(id, vec))
	}
	if len(nodes) > 0 {
		idx.graph.Add(nodes...)
	}
	return idx
}

func (h *hnswIndex) Search(query []float32, k int) []VectorResult {
	if h.graph.Len() == 0 {
		return nil
	}
	if k > h.graph.Len() {
		k = h.graph.Len()
	}
	// coder/hnsw's Node doesn't carry the computed distance, so we
	// recompute cosine distance for the (small) result set.
	neighbors := h.graph.Search(query, k)
	results := make([]VectorResult, len(neighbors))
	for i, n := range neighbors {
		dist := hnsw.CosineDistance(query, n.Value)
		results[i] = VectorResult{
			SymbolID:   n.Key,
			Similarity: 1 - dist,
		}
	}
	return results
}

func (h *hnswIndex) Len() int {
	return h.graph.Len()
}
