//go:build onnx_integration

// This file is the ONNX integration test. It is excluded from the default
// `go test` path by the onnx_integration build tag, so unit tests stay
// ONNX-free; CI runs it explicitly (make cover passes the tag, with deps
// fetched first). Unlike a skipped test, it FAILS LOUD when the bundled model
// or runtime is missing — a skipped integration test reads green while
// asserting nothing, which is the failure mode this test exists to prevent.
package embed

import (
	"context"
	"math"
	"testing"
)

// requireDeps fails loudly (never t.Skip) when the bundled model, vocabulary,
// or ONNX Runtime library is absent. Under the onnx_integration tag the deps
// are a precondition, not an option: a missing dep is a broken CI setup, not a
// reason to silently pass.
func requireDeps(t *testing.T) {
	t.Helper()
	if len(modelBytes) == 0 {
		t.Fatal("model not bundled; run scripts/fetch-deps.sh --local before the onnx_integration run")
	}
	if len(vocabBytes) == 0 {
		t.Fatal("vocab not bundled; run scripts/fetch-deps.sh --local before the onnx_integration run")
	}
	if len(loadBundledORTLib()) == 0 {
		t.Fatal("ONNX Runtime library not bundled; run scripts/fetch-deps.sh --local before the onnx_integration run")
	}
}

// TestIntegrationBundledEmbedRoundTrip drives one real embedding end to end:
// extract the bundled runtime, init the ONNX environment, build a session, and
// embed real inputs. It exercises the CGO shell (NewBundledEmbedder →
// InitORTLibrary → NewONNXEmbedder → Embed → embedBatch → session.Run → Close)
// that no fake can reach, and asserts the model's contract: 384-dimensional,
// L2-normalized vectors.
func TestIntegrationBundledEmbedRoundTrip(t *testing.T) {
	requireDeps(t)

	emb, err := NewBundledEmbedder(0)
	if err != nil {
		t.Fatalf("NewBundledEmbedder: %v", err)
	}
	defer func() { _ = emb.Close() }()

	inputs := []EmbedInput{
		{QualifiedName: "Auth#login", Kind: "method", Snippet: "def login(email, password)"},
		{QualifiedName: "Auth#logout", Kind: "method", Snippet: "def logout(token)"},
	}
	vecs, err := emb.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != len(inputs) {
		t.Fatalf("got %d vectors, want %d", len(vecs), len(inputs))
	}
	for i, v := range vecs {
		if len(v) != Dimensions {
			t.Errorf("vec[%d] dims = %d, want %d", i, len(v), Dimensions)
		}
		var sumSq float64
		for _, x := range v {
			sumSq += float64(x) * float64(x)
		}
		if math.Abs(math.Sqrt(sumSq)-1.0) > 1e-3 {
			t.Errorf("vec[%d] not L2-normalized: |v| = %v", i, math.Sqrt(sumSq))
		}
	}
}

// TestIntegrationThreadCountDeterminism covers the intraOpThreads > 0 session
// option (an all-cores default leaves it unset) and proves the thread count is
// a performance knob, not a correctness one: the same inputs embed to the same
// vectors whether the session runs single- or multi-threaded.
func TestIntegrationThreadCountDeterminism(t *testing.T) {
	requireDeps(t)

	inputs := []EmbedInput{{QualifiedName: "Svc#run", Kind: "method", Snippet: "def run"}}

	allCores, err := NewBundledEmbedder(0)
	if err != nil {
		t.Fatalf("NewBundledEmbedder(0): %v", err)
	}
	defer func() { _ = allCores.Close() }()
	a, err := allCores.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embed (all cores): %v", err)
	}

	twoThreads, err := NewBundledEmbedder(2)
	if err != nil {
		t.Fatalf("NewBundledEmbedder(2): %v", err)
	}
	defer func() { _ = twoThreads.Close() }()
	b, err := twoThreads.Embed(context.Background(), inputs)
	if err != nil {
		t.Fatalf("embed (2 threads): %v", err)
	}

	for i := range a[0] {
		if a[0][i] != b[0][i] {
			t.Fatalf("thread count changed the vector at [%d]: %v vs %v", i, a[0][i], b[0][i])
		}
	}
}

// TestIntegrationEmbedRespectsCancel covers the context-cancellation branch in
// Embed against the real session — a unit fake cannot, because the branch sits
// before any embedder-specific work.
func TestIntegrationEmbedRespectsCancel(t *testing.T) {
	requireDeps(t)

	emb, err := NewBundledEmbedder(0)
	if err != nil {
		t.Fatalf("NewBundledEmbedder: %v", err)
	}
	defer func() { _ = emb.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := emb.Embed(ctx, []EmbedInput{{Snippet: "x"}}); err == nil {
		t.Fatal("expected Embed to honor a cancelled context")
	}
}
