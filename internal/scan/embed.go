package scan

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/search"
)

const maxEmbedWorkers = 8

// embedSymbols is pass 3: generate embeddings for symbols in changed files.
// Only symbols whose file was re-indexed this scan get new embeddings.
// Orphaned embeddings are cleaned by FK CASCADE when symbols are deleted.
func (h *harness) embedSymbols() error {
	if len(h.changedFileIDs) == 0 {
		return nil
	}

	syms, err := h.idx.SymbolsForFiles(h.ctx, h.changedFileIDs)
	if err != nil {
		return fmt.Errorf("query symbols for embedding: %w", err)
	}
	if len(syms) == 0 {
		return nil
	}

	inputs := make([]embed.EmbedInput, len(syms))
	for i, s := range syms {
		inputs[i] = embed.EmbedInput{
			QualifiedName: s.Qualified,
			Kind:          s.Kind,
			ParentName:    s.ParentName,
			Snippet:       s.Snippet,
		}
	}

	vecs, err := parallelEmbed(h.ctx, inputs)
	if err != nil {
		return fmt.Errorf("generate embeddings: %w", err)
	}

	err = h.idx.InTx(h.ctx, func() error {
		stmt, err := h.idx.PrepareEmbeddingStmt(h.ctx)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for i, vec := range vecs {
			blob := vectorToBlob(vec)
			if _, err := stmt.ExecContext(h.ctx, syms[i].ID, blob); err != nil {
				return fmt.Errorf("write embedding symbol=%d: %w", syms[i].ID, err)
			}
		}
		if err := h.idx.WriteMeta(h.ctx, "embedding_model", embed.ModelID); err != nil {
			return fmt.Errorf("write embedding model meta: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("write embeddings: %w", err)
	}

	h.embedded = len(vecs)
	return nil
}

// buildHNSWIndex updates the prebuilt HNSW index at .sense/hnsw.bin.
// On incremental scans it loads the existing index, deletes stale vectors,
// and inserts fresh ones — avoiding a full O(n log n) rebuild. Falls back
// to a full build when no existing index is found (first scan).
func (h *harness) buildHNSWIndex(senseDir string) error {
	path := filepath.Join(senseDir, "hnsw.bin")

	if updated, err := h.tryIncrementalHNSW(path); updated {
		return nil
	} else if err != nil {
		_, _ = fmt.Fprintf(h.out, "warn: incremental hnsw update failed, rebuilding: %v\n", err)
	}

	embeddings, err := h.idx.LoadEmbeddings(h.ctx)
	if err != nil {
		return fmt.Errorf("load embeddings for hnsw: %w", err)
	}
	if len(embeddings) == 0 {
		return nil
	}
	if err := search.SaveHNSWIndex(path, embeddings); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(h.out, "saved hnsw index (%d vectors) to %s\n", len(embeddings), filepath.Base(path))
	return nil
}

// tryIncrementalHNSW attempts an incremental HNSW update. Returns true if
// the update succeeded, false if a full rebuild is needed instead.
// Pure-deletion (files removed, nothing changed) skips the incremental path
// because the hnsw library's Delete can corrupt small graphs.
func (h *harness) tryIncrementalHNSW(path string) (bool, error) {
	fresh, err := h.idx.EmbeddingsForFiles(h.ctx, h.changedFileIDs)
	if err != nil {
		return false, fmt.Errorf("load changed embeddings: %w", err)
	}
	if len(fresh) == 0 {
		return false, nil
	}

	updated, err := search.UpdateHNSWIndex(path, h.removedSymbolIDs, fresh)
	if err != nil {
		return false, err
	}
	if !updated {
		return false, nil
	}

	delta := len(fresh) - len(h.removedSymbolIDs)
	sign := "+"
	if delta < 0 {
		sign = ""
	}
	_, _ = fmt.Fprintf(h.out, "updated hnsw index (%s%d vectors, %d deleted) at %s\n",
		sign, delta, len(h.removedSymbolIDs), filepath.Base(path))
	return true, nil
}

// parallelEmbed fans inputs across multiple ONNXEmbedder instances to
// saturate available CPU cores. Each embedder owns an independent ONNX
// session and pre-allocated buffers — no shared mutable state.
func parallelEmbed(ctx context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	batches := (len(inputs) + embed.BatchSize - 1) / embed.BatchSize
	ncpu := runtime.NumCPU()
	workers := ncpu
	if workers > batches {
		workers = batches
	}
	if workers > maxEmbedWorkers {
		workers = maxEmbedWorkers
	}
	if workers <= 1 {
		emb, err := embed.NewBundledEmbedder(0)
		if err != nil {
			return nil, err
		}
		defer func() { _ = emb.Close() }()
		return emb.Embed(ctx, inputs)
	}

	threadsPerWorker := ncpu / workers
	if threadsPerWorker < 1 {
		threadsPerWorker = 1
	}

	embedders := make([]*embed.ONNXEmbedder, workers)
	for i := range embedders {
		emb, err := embed.NewBundledEmbedder(threadsPerWorker)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = embedders[j].Close()
			}
			return nil, fmt.Errorf("create embedder %d: %w", i, err)
		}
		embedders[i] = emb
	}
	defer func() {
		for _, emb := range embedders {
			_ = emb.Close()
		}
	}()

	chunkSize := (len(inputs) + workers - 1) / workers
	type result struct {
		vecs [][]float32
		err  error
	}
	results := make([]result, workers)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		start := w * chunkSize
		if start >= len(inputs) {
			break
		}
		end := start + chunkSize
		if end > len(inputs) {
			end = len(inputs)
		}
		wg.Add(1)
		go func(idx int, chunk []embed.EmbedInput) {
			defer wg.Done()
			vecs, err := embedders[idx].Embed(ctx, chunk)
			if err != nil {
				results[idx] = result{err: err}
				cancel()
				return
			}
			results[idx] = result{vecs: vecs}
		}(w, inputs[start:end])
	}
	wg.Wait()

	all := make([][]float32, 0, len(inputs))
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		all = append(all, r.vecs...)
	}
	return all, nil
}

// vectorToBlob serializes a float32 slice to a little-endian byte slice.
func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}
