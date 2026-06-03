package embedtest_test

import (
	"context"
	"math"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/embed/embedtest"
)

func TestFakeEmbedderShapeAndDeterminism(t *testing.T) {
	f := embedtest.NewFakeEmbedder(embed.Dimensions)
	inputs := []embed.EmbedInput{
		{QualifiedName: "Auth#login", Kind: "method", Snippet: "def login"},
		{QualifiedName: "Auth#logout", Kind: "method", Snippet: "def logout"},
	}

	got, err := f.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != len(inputs) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(inputs))
	}
	for i, vec := range got {
		if len(vec) != embed.Dimensions {
			t.Errorf("vec[%d] dims = %d, want %d", i, len(vec), embed.Dimensions)
		}
	}

	// Same inputs → byte-identical vectors (the oracle depends on this).
	again, _ := f.Embed(context.Background(), inputs)
	for i := range got {
		for j := range got[i] {
			if got[i][j] != again[i][j] {
				t.Fatalf("non-deterministic at [%d][%d]: %v vs %v", i, j, got[i][j], again[i][j])
			}
		}
	}
}

func TestFakeEmbedderDistinctInputsDiffer(t *testing.T) {
	f := embedtest.NewFakeEmbedder(embed.Dimensions)
	got, _ := f.Embed(context.Background(), []embed.EmbedInput{
		{Snippet: "alpha"},
		{Snippet: "beta"},
	})
	if vecEqual(got[0], got[1]) {
		t.Error("distinct inputs produced identical vectors")
	}
}

func TestFakeEmbedderNormalized(t *testing.T) {
	f := embedtest.NewFakeEmbedder(embed.Dimensions)
	got, _ := f.Embed(context.Background(), []embed.EmbedInput{{Snippet: "normalize me"}})
	var sumSq float64
	for _, v := range got[0] {
		sumSq += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(sumSq)-1.0) > 1e-5 {
		t.Errorf("vector not unit length: |v| = %v", math.Sqrt(sumSq))
	}
}

func TestFakeEmbedderEmptyAndZeroDims(t *testing.T) {
	f := embedtest.NewFakeEmbedder(0)
	got, err := f.Embed(context.Background(), []embed.EmbedInput{{Snippet: "x"}})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 0 {
		t.Errorf("zero-dim embedder: got %d vectors of len %d, want 1 of len 0", len(got), len(got[0]))
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func vecEqual(a, b []float32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
