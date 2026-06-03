package scan

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
)

const maxEmbedWorkers = 4

const embedChunkSize = 512

// embedPool fans out symbol batches across N embedders. The slice
// holds the embed.Embedder interface (rather than *ONNXEmbedder)
// so tests can substitute fakes and exercise the parallel path
// without spinning up ONNX.
type embedPool struct {
	embedders []embed.Embedder
	workers   int
}

// embedderFactory constructs one embedder configured for threads intra-op
// threads. It is the seam that keeps ONNX out of the scan pipeline's tests:
// production wires defaultEmbedderFactory (the bundled ONNX model); a test
// substitutes a fake via SetEmbedderFactory (see export_test.go).
type embedderFactory func(threads int) (embed.Embedder, error)

// defaultEmbedderFactory is the production embedder: the ONNX model bundled in
// the binary. It is a package var, not a hard call, so the export_test.go seam
// can swap it for a fake without widening any exported surface. Mutated only by
// SetEmbedderFactory (with t.Cleanup restore); not safe to override under
// t.Parallel — the scan embedding tests run serially, which is why it can be a
// plain global rather than threaded through Options.
var defaultEmbedderFactory embedderFactory = func(threads int) (embed.Embedder, error) {
	return embed.NewBundledEmbedder(threads)
}

// embedPoolSizing decides the worker count and per-worker intra-op thread
// count from the available CPUs: half the cores, clamped to [2, maxEmbedWorkers],
// then threads split evenly across workers (at least one). Pure so the clamps
// can be exercised across CPU counts without depending on the test host.
func embedPoolSizing(ncpu int) (workers, threadsPerWorker int) {
	workers = ncpu / 2
	if workers < 2 {
		workers = 2
	}
	if workers > maxEmbedWorkers {
		workers = maxEmbedWorkers
	}
	threadsPerWorker = ncpu / workers
	if threadsPerWorker < 1 {
		threadsPerWorker = 1
	}
	return workers, threadsPerWorker
}

func newEmbedPool(newEmbedder embedderFactory) (*embedPool, error) {
	workers, threadsPerWorker := embedPoolSizing(runtime.NumCPU())

	embedders := make([]embed.Embedder, workers)
	for i := range embedders {
		emb, err := newEmbedder(threadsPerWorker)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = embedders[j].Close()
			}
			return nil, fmt.Errorf("create embedder %d: %w", i, err)
		}
		embedders[i] = emb
	}
	return &embedPool{embedders: embedders, workers: workers}, nil
}

func (p *embedPool) embed(ctx context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	batches := (len(inputs) + embed.BatchSize - 1) / embed.BatchSize
	workers := p.workers
	if workers > batches {
		workers = batches
	}
	if workers <= 1 {
		return p.embedders[0].Embed(ctx, inputs)
	}

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
			vecs, err := p.embedders[idx].Embed(ctx, chunk)
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

func (p *embedPool) Close() error {
	var first error
	for _, emb := range p.embedders {
		if err := emb.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// migrateEmbeddingModel checks whether the stored embedding model
// matches the current binary's model. If they differ, it clears all
// embeddings so everything gets re-embedded with the new model on the
// next embed pass. Returns true if a migration occurred.
func (h *harness) migrateEmbeddingModel() (bool, error) {
	stored, err := h.idx.ReadMeta(h.ctx, "embedding_model")
	if err != nil {
		return false, fmt.Errorf("read embedding model: %w", err)
	}
	if stored == "" || stored == embed.ModelID {
		return false, nil
	}

	if err := h.idx.ClearEmbeddings(h.ctx); err != nil {
		return false, fmt.Errorf("clear embeddings: %w", err)
	}
	if err := h.idx.DeleteMeta(h.ctx, "embedding_model"); err != nil {
		return false, fmt.Errorf("delete embedding model meta: %w", err)
	}
	return true, nil
}

// EmbedPending generates embeddings for all symbols that lack them.
// Designed for the MCP server's background embedder — it constructs a
// minimal harness internally and clears the embedding watermark on success.
// Returns the number of symbols embedded. The caller rebuilds the in-memory
// vector index from the embeddings table afterward.
func EmbedPending(ctx context.Context, idx *sqlite.Adapter, root string) (int, error) {
	syms, err := idx.SymbolsWithoutEmbeddings(ctx)
	if err != nil {
		return 0, fmt.Errorf("query pending symbols: %w", err)
	}
	if len(syms) == 0 {
		return 0, nil
	}

	h := &harness{ctx: ctx, idx: idx, root: root, out: io.Discard, warn: io.Discard, newEmbedder: defaultEmbedderFactory}
	h.extendMethodSnippets(syms)
	contextMap := h.buildContextMap(syms)

	fileIDs := uniqueFileIDs(syms)
	paths, pathErr := idx.FilePathsByIDs(ctx, fileIDs)
	if pathErr != nil {
		_, _ = fmt.Fprintf(h.warn, "warn: resolve file paths for embeddings: %v\n", pathErr)
	}
	inputs := buildEmbedInputs(syms, contextMap, paths)

	pool, err := newEmbedPool(h.newEmbedder)
	if err != nil {
		return 0, fmt.Errorf("create embed pool: %w", err)
	}
	defer func() { _ = pool.Close() }()

	var total int
	for i := 0; i < len(inputs); i += embedChunkSize {
		end := i + embedChunkSize
		if end > len(inputs) {
			end = len(inputs)
		}

		vecs, err := pool.embed(ctx, inputs[i:end])
		if err != nil {
			return total, fmt.Errorf("generate embeddings: %w", err)
		}
		if err := writeEmbeddingChunk(ctx, idx, syms[i:end], vecs); err != nil {
			return total, fmt.Errorf("write embeddings: %w", err)
		}
		total += len(vecs)
	}

	if total > 0 {
		if err := idx.WriteMeta(ctx, "embedding_model", embed.ModelID); err != nil {
			return total, fmt.Errorf("write embedding model meta: %w", err)
		}
	}

	if err := idx.DeleteMeta(ctx, "embedding_watermark"); err != nil {
		return total, fmt.Errorf("clear embedding watermark: %w", err)
	}
	return total, nil
}

// buildEmbedInputs assembles the per-symbol embedder input from the resolved
// context and file-path maps. Shared by the synchronous (embedSymbols) and
// backfill (EmbedPending) callers so they build inputs identically.
func buildEmbedInputs(syms []sqlite.EmbedSymbol, contextMap, paths map[int64]string) []embed.EmbedInput {
	inputs := make([]embed.EmbedInput, len(syms))
	for i, s := range syms {
		inputs[i] = embed.EmbedInput{
			QualifiedName: s.Qualified,
			Kind:          s.Kind,
			Snippet:       s.Snippet,
			Context:       contextMap[s.ID],
			FilePath:      paths[s.FileID],
		}
	}
	return inputs
}

// writeEmbeddingChunk persists one chunk of vectors in a single transaction,
// pairing each vector with its symbol id positionally. Shared by both embed
// callers; idx is the indexStore seam both satisfy.
func writeEmbeddingChunk(ctx context.Context, idx indexStore, chunkSyms []sqlite.EmbedSymbol, vecs [][]float32) error {
	return idx.InTx(ctx, func() error {
		stmt, err := idx.PrepareEmbeddingStmt(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()
		for j, vec := range vecs {
			blob := vectorToBlob(vec)
			if _, err := stmt.ExecContext(ctx, chunkSyms[j].ID, blob); err != nil {
				return fmt.Errorf("write embedding symbol=%d: %w", chunkSyms[j].ID, err)
			}
		}
		return nil
	})
}

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

	h.extendMethodSnippets(syms)
	contextMap := h.buildContextMap(syms)

	fileIDs := uniqueFileIDs(syms)
	paths, pathErr := h.idx.FilePathsByIDs(h.ctx, fileIDs)
	if pathErr != nil {
		_, _ = fmt.Fprintf(h.warn, "warn: resolve file paths for embeddings: %v\n", pathErr)
	}
	inputs := buildEmbedInputs(syms, contextMap, paths)

	pool, err := newEmbedPool(h.newEmbedder)
	if err != nil {
		return fmt.Errorf("create embed pool: %w", err)
	}
	defer func() { _ = pool.Close() }()

	h.progress.setPhase("Embedding...", int64(len(inputs)))

	for i := 0; i < len(inputs); i += embedChunkSize {
		end := i + embedChunkSize
		if end > len(inputs) {
			end = len(inputs)
		}

		vecs, err := pool.embed(h.ctx, inputs[i:end])
		if err != nil {
			return fmt.Errorf("generate embeddings: %w", err)
		}
		if err := writeEmbeddingChunk(h.ctx, h.idx, syms[i:end], vecs); err != nil {
			return fmt.Errorf("write embeddings: %w", err)
		}
		h.embedded += len(vecs)
		h.progress.current.Add(int64(len(vecs)))
	}

	if h.embedded > 0 {
		if err := h.idx.WriteMeta(h.ctx, "embedding_model", embed.ModelID); err != nil {
			return fmt.Errorf("write embedding model meta: %w", err)
		}
	}

	return nil
}

func uniqueFileIDs(syms []sqlite.EmbedSymbol) []int64 {
	seen := make(map[int64]bool, len(syms))
	ids := make([]int64, 0, len(syms))
	for _, s := range syms {
		if !seen[s.FileID] {
			seen[s.FileID] = true
			ids = append(ids, s.FileID)
		}
	}
	return ids
}

// buildContextMap calls ContextForFile for each unique file among the
// symbols and merges the results into a flat symbolID→context map.
func (h *harness) buildContextMap(syms []sqlite.EmbedSymbol) map[int64]string {
	seen := make(map[int64]bool)
	result := make(map[int64]string, len(syms))
	for _, s := range syms {
		if seen[s.FileID] {
			continue
		}
		seen[s.FileID] = true
		ctx, err := h.idx.ContextForFile(h.ctx, s.FileID)
		if err != nil {
			continue
		}
		for id, text := range ctx {
			result[id] = text
		}
	}
	return result
}

const maxBodyLines = 10

// extendMethodSnippets replaces single-line snippets for method/function
// symbols with the first N lines of the body read from source. Groups
// symbols by file to avoid re-reading the same file multiple times.
func (h *harness) extendMethodSnippets(syms []sqlite.EmbedSymbol) {
	byFile := make(map[int64][]int)
	for i := range syms {
		if syms[i].Kind == "method" || syms[i].Kind == "function" {
			byFile[syms[i].FileID] = append(byFile[syms[i].FileID], i)
		}
	}
	if len(byFile) == 0 {
		return
	}

	fileIDs := make([]int64, 0, len(byFile))
	for fid := range byFile {
		fileIDs = append(fileIDs, fid)
	}
	paths, err := h.idx.FilePathsByIDs(h.ctx, fileIDs)
	if err != nil {
		return
	}

	for fid, indices := range byFile {
		relPath, ok := paths[fid]
		if !ok {
			continue
		}
		absPath := filepath.Join(h.root, relPath)
		lines, err := readFileLines(absPath)
		if err != nil {
			continue
		}
		for _, i := range indices {
			s := &syms[i]
			start := s.LineStart - 1 // 0-indexed
			end := s.LineStart - 1 + maxBodyLines
			if end > s.LineEnd {
				end = s.LineEnd
			}
			if start < 0 || start >= len(lines) {
				continue
			}
			if end > len(lines) {
				end = len(lines)
			}
			s.Snippet = strings.Join(lines[start:end], "\n")
		}
	}
}

func readFileLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

// vectorToBlob serializes a float32 slice to a little-endian byte slice.
func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}
