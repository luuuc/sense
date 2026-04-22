package search

import (
	"bufio"
	"bytes"
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

// UpdateHNSWIndex loads an existing HNSW index from disk, applies incremental
// upserts and deletes, and writes the updated index back. Returns false if the
// index file doesn't exist (caller should fall back to full build).
//
// The coder/hnsw library's Delete can leave empty upper layers that crash
// Search, and its Export can serialize dangling neighbor references. To guard
// against shipping a corrupt index, the updated graph is exported, reimported,
// and verified with a test search before writing to disk. Any panic from the
// library is recovered into a false return so the caller falls back to a full
// rebuild.
func UpdateHNSWIndex(path string, toRemove []int64, toUpsert map[int64][]float32) (updated bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			updated = false
			err = nil
		}
	}()

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open hnsw index for update: %w", err)
	}
	g := hnsw.NewGraph[int64]()
	g.Distance = hnsw.CosineDistance
	if err := g.Import(bufio.NewReader(f)); err != nil {
		_ = f.Close()
		return false, fmt.Errorf("import hnsw index for update: %w", err)
	}
	_ = f.Close()

	for id, vec := range toUpsert {
		g.Delete(id)
		g.Add(hnsw.MakeNode(id, vec))
	}

	hasRemovals := false
	for _, id := range toRemove {
		if _, replacing := toUpsert[id]; replacing {
			continue
		}
		g.Delete(id)
		hasRemovals = true
	}

	// When there are standalone removals (not compensated by upserts),
	// the hnsw library can leave dangling neighbor pointers that only
	// crash on reimport. Verify via Export/Import round-trip + search
	// before writing to disk. Upsert-only updates are safe (Delete+Add
	// per key keeps the graph connected) and skip verification.
	if hasRemovals && g.Len() > 0 {
		var buf bytes.Buffer
		if err := g.Export(&buf); err != nil {
			return false, nil
		}
		check := hnsw.NewGraph[int64]()
		check.Distance = hnsw.CosineDistance
		if err := check.Import(&buf); err != nil {
			return false, nil
		}
		if dims := check.Dims(); dims > 0 {
			check.Search(make([]float32, dims), 1)
		}
		g = check
	}

	if g.Len() > 0 {
		out, err := os.Create(path)
		if err != nil {
			return false, fmt.Errorf("create hnsw index for update: %w", err)
		}
		defer func() { _ = out.Close() }()
		w := bufio.NewWriter(out)
		if err := g.Export(w); err != nil {
			_ = os.Remove(path)
			return false, fmt.Errorf("export updated hnsw index: %w", err)
		}
		if err := w.Flush(); err != nil {
			_ = os.Remove(path)
			return false, fmt.Errorf("flush updated hnsw index: %w", err)
		}
	}
	return true, nil
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
