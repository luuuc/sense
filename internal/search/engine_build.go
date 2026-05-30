package search

import (
	"context"
	"fmt"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
)

// BuildEngine constructs a search Engine with its vector index and embedder,
// centralizing the wiring that the CLI, MCP server, and benchmark harness
// each used to duplicate. There is exactly one vector-construction path:
// load the embeddings table, build an exact flat index over it, and create
// the bundled query embedder. Nothing is read from or written to disk beyond
// the embeddings already in the index database.
//
// The returned embedder is also stored on the engine; callers own its
// lifecycle and must Close it (it is nil when embeddings are disabled or the
// repo has neither vectors nor pending embedding debt). An embedder is
// created whenever the repo has vectors OR outstanding embedding debt, so the
// MCP server's background embed can upgrade the engine to hybrid search once
// the debt is paid.
func BuildEngine(ctx context.Context, adapter *sqlite.Adapter, dir string) (*Engine, embed.Embedder, error) {
	if !config.IsEmbeddingsEnabled(dir) {
		return NewEngine(adapter, nil, nil), nil, nil
	}

	embeddings, err := adapter.LoadEmbeddings(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("build engine: load embeddings: %w", err)
	}

	var idx VectorIndex
	if len(embeddings) > 0 {
		idx = BuildFlatIndex(embeddings)
	}

	needEmbedder := idx != nil && idx.Len() > 0
	if !needEmbedder {
		debt, derr := adapter.EmbeddingDebtCount(ctx)
		if derr != nil {
			return nil, nil, fmt.Errorf("build engine: embedding debt: %w", derr)
		}
		needEmbedder = debt > 0
	}

	var embedder embed.Embedder
	if needEmbedder {
		embedder, err = embed.NewBundledEmbedder(0)
		if err != nil {
			return nil, nil, fmt.Errorf("build engine: init embedder: %w", err)
		}
	}

	return NewEngine(adapter, idx, embedder), embedder, nil
}
