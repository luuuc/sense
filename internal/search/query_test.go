package search_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/search"
)

type fakeEmbedder struct {
	dims int
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	vecs := make([][]float32, len(inputs))
	for i, in := range inputs {
		text := embed.FormatInput(in)
		vec := make([]float32, f.dims)
		for j := range vec {
			vec[j] = float32(len(text)+j) * 0.001
		}
		vecs[i] = vec
	}
	return vecs, nil
}

func (f *fakeEmbedder) Close() error { return nil }

type errorEmbedder struct{}

func (e *errorEmbedder) Embed(context.Context, []embed.EmbedInput) ([][]float32, error) {
	return nil, fmt.Errorf("embedding failed")
}
func (e *errorEmbedder) Close() error { return nil }

func TestEmbedQuery(t *testing.T) {
	ctx := context.Background()
	emb := &fakeEmbedder{dims: 384}

	vec, err := search.EmbedQuery(ctx, emb, "payment error handling")
	if err != nil {
		t.Fatal(err)
	}
	if len(vec) != 384 {
		t.Errorf("expected 384 dimensions, got %d", len(vec))
	}
}

func TestEmbedQueryError(t *testing.T) {
	ctx := context.Background()
	_, err := search.EmbedQuery(ctx, &errorEmbedder{}, "test")
	if err == nil {
		t.Error("expected error from failing embedder")
	}
}
