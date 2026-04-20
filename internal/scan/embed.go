package scan

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/luuuc/sense/internal/embed"
)

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

	embedder, err := embed.NewBundledEmbedder()
	if err != nil {
		return fmt.Errorf("create embedder: %w", err)
	}
	defer func() { _ = embedder.Close() }()

	inputs := make([]embed.EmbedInput, len(syms))
	for i, s := range syms {
		inputs[i] = embed.EmbedInput{
			QualifiedName: s.Qualified,
			Kind:          s.Kind,
			ParentName:    s.ParentName,
			Snippet:       s.Snippet,
		}
	}

	vecs, err := embedder.Embed(h.ctx, inputs)
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

// vectorToBlob serializes a float32 slice to a little-endian byte slice.
func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(v))
	}
	return buf
}
