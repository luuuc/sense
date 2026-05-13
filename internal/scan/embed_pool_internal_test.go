package scan

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/sqlite"
)

// fakeEmbedder satisfies the embedder interface for testing the
// parallel fan-out path in embedPool.embed without spinning up ONNX.
type fakeEmbedder struct {
	id         int
	calls      atomic.Int32
	totalInput atomic.Int32
	failErr    error
}

func (f *fakeEmbedder) Embed(_ context.Context, inputs []embed.EmbedInput) ([][]float32, error) {
	f.calls.Add(1)
	f.totalInput.Add(int32(len(inputs)))
	if f.failErr != nil {
		return nil, f.failErr
	}
	out := make([][]float32, len(inputs))
	for i := range inputs {
		// Encode (worker-id, input-index) so the caller can verify
		// that results are concatenated in the right order.
		out[i] = []float32{float32(f.id), float32(i)}
	}
	return out, nil
}

func (f *fakeEmbedder) Close() error { return nil }

func makeInputs(n int) []embed.EmbedInput {
	out := make([]embed.EmbedInput, n)
	for i := range out {
		out[i] = embed.EmbedInput{Snippet: "x"}
	}
	return out
}

func TestEmbedPoolEmptyInputs(t *testing.T) {
	pool := &embedPool{
		embedders: []embed.Embedder{&fakeEmbedder{id: 0}, &fakeEmbedder{id: 1}},
		workers:   2,
	}
	got, err := pool.embed(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty embed: %v", err)
	}
	if got != nil {
		t.Errorf("empty embed returned %v, want nil", got)
	}
}

func TestEmbedPoolParallelFanOut(t *testing.T) {
	// Need batches >= 2 to trigger the parallel path; BatchSize=50 → 100 inputs.
	const total = 2 * embed.BatchSize

	e0 := &fakeEmbedder{id: 0}
	e1 := &fakeEmbedder{id: 1}
	pool := &embedPool{embedders: []embed.Embedder{e0, e1}, workers: 2}

	got, err := pool.embed(context.Background(), makeInputs(total))
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(got) != total {
		t.Fatalf("len(got) = %d, want %d", len(got), total)
	}

	// Each worker should have been called once with its half.
	if e0.calls.Load() != 1 || e1.calls.Load() != 1 {
		t.Errorf("calls: e0=%d e1=%d, want 1 each", e0.calls.Load(), e1.calls.Load())
	}
	if int(e0.totalInput.Load()+e1.totalInput.Load()) != total {
		t.Errorf("total inputs across workers = %d, want %d",
			e0.totalInput.Load()+e1.totalInput.Load(), total)
	}

	// Order: first chunk comes from worker 0 (id=0), second from worker 1 (id=1).
	if got[0][0] != 0 {
		t.Errorf("got[0] worker id = %v, want 0", got[0][0])
	}
	if got[total-1][0] != 1 {
		t.Errorf("got[last] worker id = %v, want 1", got[total-1][0])
	}
}

func TestEmbedPoolWorkerError(t *testing.T) {
	want := errors.New("worker boom")
	e0 := &fakeEmbedder{id: 0, failErr: want}
	e1 := &fakeEmbedder{id: 1}
	pool := &embedPool{embedders: []embed.Embedder{e0, e1}, workers: 2}

	_, err := pool.embed(context.Background(), makeInputs(2*embed.BatchSize))
	if err == nil {
		t.Fatal("expected error from failing worker")
	}
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestEmbedPoolCloseAggregatesFirstError(t *testing.T) {
	want := errors.New("close boom")
	pool := &embedPool{
		embedders: []embed.Embedder{
			&closingEmbedder{closeErr: want},
			&closingEmbedder{closeErr: errors.New("second")},
		},
	}
	if err := pool.Close(); !errors.Is(err, want) {
		t.Errorf("Close returned %v, want first error %v", err, want)
	}
}

type closingEmbedder struct {
	closeErr error
}

func (c *closingEmbedder) Embed(context.Context, []embed.EmbedInput) ([][]float32, error) {
	return nil, nil
}
func (c *closingEmbedder) Close() error { return c.closeErr }

// TestTryIncrementalHNSWNoChangedFiles pins the early-return path: with
// no changedFileIDs there is nothing to load and the function must
// signal "not updated, no error" so the caller falls back to a full rebuild.
func TestTryIncrementalHNSWNoChangedFiles(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(tmp, "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	h := &harness{ctx: ctx, idx: adapter, out: io.Discard, warn: io.Discard}
	updated, err := h.tryIncrementalHNSW(filepath.Join(tmp, "hnsw.bin"))
	if err != nil {
		t.Fatalf("tryIncrementalHNSW: %v", err)
	}
	if updated {
		t.Error("expected updated=false with no changedFileIDs")
	}
}

// TestBuildHNSWIndexEmptyDB exercises the empty-embeddings short-circuit
// in buildHNSWIndex (LoadEmbeddings returns nil → return nil without
// writing anything to disk).
func TestBuildHNSWIndexEmptyDB(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(tmp, "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	senseDir := tmp
	h := &harness{ctx: ctx, idx: adapter, out: io.Discard, warn: io.Discard}
	if err := h.buildHNSWIndex(senseDir); err != nil {
		t.Fatalf("buildHNSWIndex on empty DB: %v", err)
	}
	// Should not have written hnsw.bin.
	if _, err := os.Stat(filepath.Join(senseDir, "hnsw.bin")); err == nil {
		t.Error("hnsw.bin should not exist for empty DB")
	}
}
