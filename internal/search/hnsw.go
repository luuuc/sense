package search

import (
	"bufio"
	"fmt"
	"os"

	"github.com/coder/hnsw"
)

// hnswIndex wraps coder/hnsw as a VectorIndex. The graph is keyed by
// symbol ID (int64) and uses cosine distance. It is built eagerly from
// all embeddings in the database and rebuilt on scan completion.
type hnswIndex struct {
	graph *hnsw.Graph[int64]
}

func buildGraph(embeddings map[int64][]float32) *hnsw.Graph[int64] {
	g := hnsw.NewGraph[int64]()
	g.Distance = hnsw.CosineDistance
	nodes := make([]hnsw.Node[int64], 0, len(embeddings))
	for id, vec := range embeddings {
		nodes = append(nodes, hnsw.MakeNode(id, vec))
	}
	if len(nodes) > 0 {
		g.Add(nodes...)
	}
	return g
}

// BuildHNSWIndex creates an HNSW index from a set of symbol ID → vector
// pairs. This is the startup path: load all embeddings from SQLite,
// insert them into the graph in one batch. Returns a VectorIndex so
// callers depend on the interface, not the concrete type.
func BuildHNSWIndex(embeddings map[int64][]float32) VectorIndex {
	return &hnswIndex{graph: buildGraph(embeddings)}
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

// SaveHNSWIndex builds an HNSW index from embeddings and persists it
// to a binary file. Called at the end of scan so search can load the
// prebuilt index instead of rebuilding from raw vectors on every
// invocation.
func SaveHNSWIndex(path string, embeddings map[int64][]float32) error {
	g := buildGraph(embeddings)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create hnsw index: %w", err)
	}
	defer func() { _ = f.Close() }()
	w := bufio.NewWriter(f)
	if err := g.Export(w); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("export hnsw index: %w", err)
	}
	if err := w.Flush(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("flush hnsw index: %w", err)
	}
	return nil
}

// LoadHNSWIndex reads a previously-saved HNSW index from disk.
// Returns nil, nil if the file does not exist (caller should fall
// back to BuildHNSWIndex).
func LoadHNSWIndex(path string) (VectorIndex, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open hnsw index: %w", err)
	}
	defer func() { _ = f.Close() }()
	g := hnsw.NewGraph[int64]()
	g.Distance = hnsw.CosineDistance
	if err := g.Import(bufio.NewReader(f)); err != nil {
		return nil, fmt.Errorf("import hnsw index: %w", err)
	}
	return &hnswIndex{graph: g}, nil
}
