// Package embedtest provides a deterministic fake embedder for tests that
// need to exercise the embedding pipeline without spinning up ONNX. It lives
// next to the embed.Embedder interface (not folded into a scan helper) so any
// embedder consumer — scan, search, the MCP server — can reuse the same fake
// and keep ONNX out of its test imports.
package embedtest

import (
	"context"
	"math"

	"github.com/luuuc/sense/internal/embed"
)

// FakeEmbedder is a deterministic embed.Embedder. It produces one fixed-length
// vector per input, derived from the formatted input text, so equal inputs map
// to equal vectors and distinct inputs almost always differ. The vectors are
// L2-normalized like the real model's so cosine-similarity consumers behave,
// but the numbers carry no semantic meaning — they exist only to drive the
// fan-out, persistence, and presence paths. Not safe for concurrent use by a
// single instance; the embed pool gives each worker its own.
type FakeEmbedder struct {
	dims int
}

// NewFakeEmbedder returns a FakeEmbedder producing vectors of dims components.
// Use embed.Dimensions to match the bundled model's width.
func NewFakeEmbedder(dims int) *FakeEmbedder {
	return &FakeEmbedder{dims: dims}
}

// Embed returns one deterministic vector per input. Never errors.
func (f *FakeEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	out := make([][]float32, len(inputs))
	for i, in := range inputs {
		out[i] = f.vector(embed.FormatContext(in))
	}
	return out, nil
}

// Close is a no-op; the fake holds no resources.
func (f *FakeEmbedder) Close() error { return nil }

// vector hashes text into a normalized vector. A small FNV-1a walk seeds each
// component, so the mapping is stable across runs and platforms (no maps, no
// randomness) — the property the behavior oracle relies on.
func (f *FakeEmbedder) vector(text string) []float32 {
	const (
		offset = 2166136261
		prime  = 16777619
	)
	vec := make([]float32, f.dims)
	h := uint32(offset)
	for _, b := range []byte(text) {
		h ^= uint32(b)
		h *= prime
	}
	var sumSq float64
	for k := range vec {
		h ^= uint32(k)
		h *= prime
		// Map the unsigned hash into [-1, 1) deterministically: h/2^31 lands
		// in [0, 2), shifted down by 1. No signed conversion, so no overflow.
		v := float32(h)/float32(1<<31) - 1.0
		vec[k] = v
		sumSq += float64(v) * float64(v)
	}
	if sumSq > 0 {
		norm := float32(math.Sqrt(sumSq))
		for k := range vec {
			vec[k] /= norm
		}
	}
	return vec
}
