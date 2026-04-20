package search

import (
	"context"
	"fmt"

	"github.com/luuuc/sense/internal/embed"
)

// EmbedQuery embeds a search query string using the same model that
// produced the symbol embeddings. The query is treated as a plain text
// snippet — no qualified name, kind, or parent context — so the
// embedding captures intent rather than structural identity.
func EmbedQuery(ctx context.Context, embedder embed.Embedder, query string) ([]float32, error) {
	input := embed.EmbedInput{Snippet: query}
	vecs, err := embedder.Embed(ctx, []embed.EmbedInput{input})
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embed query: no vectors returned")
	}
	return vecs[0], nil
}
