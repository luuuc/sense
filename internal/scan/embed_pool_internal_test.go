package scan

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/luuuc/sense/internal/embed"
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

// trackingEmbedder records whether Close was called, so the factory
// error-cleanup path can be asserted.
type trackingEmbedder struct {
	closed bool
}

func (e *trackingEmbedder) Embed(context.Context, []embed.EmbedInput) ([][]float32, error) {
	return nil, nil
}
func (e *trackingEmbedder) Close() error { e.closed = true; return nil }

func TestEmbedPoolSizing(t *testing.T) {
	cases := []struct {
		ncpu        int
		wantWorkers int
		wantThreads int
	}{
		{ncpu: 1, wantWorkers: 2, wantThreads: 1},  // clamp up to 2; threads floor at 1
		{ncpu: 2, wantWorkers: 2, wantThreads: 1},  // half = 1 → clamp to 2
		{ncpu: 4, wantWorkers: 2, wantThreads: 2},  // half = 2, no clamp
		{ncpu: 6, wantWorkers: 3, wantThreads: 2},  // half = 3
		{ncpu: 8, wantWorkers: 4, wantThreads: 2},  // half = 4 = cap
		{ncpu: 16, wantWorkers: 4, wantThreads: 4}, // half = 8 → clamp down to 4
	}
	for _, c := range cases {
		workers, threads := embedPoolSizing(c.ncpu)
		if workers != c.wantWorkers || threads != c.wantThreads {
			t.Errorf("embedPoolSizing(%d) = (%d, %d), want (%d, %d)",
				c.ncpu, workers, threads, c.wantWorkers, c.wantThreads)
		}
	}
}

func TestNewEmbedPoolBuildsWorkers(t *testing.T) {
	var threads []int
	pool, err := newEmbedPool(func(t int) (embed.Embedder, error) {
		threads = append(threads, t)
		return &trackingEmbedder{}, nil
	})
	if err != nil {
		t.Fatalf("newEmbedPool: %v", err)
	}
	defer func() { _ = pool.Close() }()

	if pool.workers < 2 || pool.workers > maxEmbedWorkers {
		t.Errorf("workers = %d, want within [2,%d]", pool.workers, maxEmbedWorkers)
	}
	if len(pool.embedders) != pool.workers {
		t.Errorf("embedders = %d, want %d", len(pool.embedders), pool.workers)
	}
	if len(threads) != pool.workers {
		t.Errorf("factory called %d times, want %d", len(threads), pool.workers)
	}
	for _, n := range threads {
		if n < 1 {
			t.Errorf("threadsPerWorker = %d, want >= 1", n)
		}
	}
}

func TestNewEmbedPoolFactoryErrorClosesCreated(t *testing.T) {
	want := errors.New("factory boom")
	var created []*trackingEmbedder
	calls := 0
	pool, err := newEmbedPool(func(int) (embed.Embedder, error) {
		calls++
		if calls == 2 {
			return nil, want
		}
		e := &trackingEmbedder{}
		created = append(created, e)
		return e, nil
	})
	if pool != nil {
		t.Errorf("pool = %v, want nil on factory error", pool)
	}
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	// Workers is always >= 2, so the first embedder was built before the
	// second call failed — it must have been closed during cleanup.
	if len(created) != 1 {
		t.Fatalf("created %d embedders before failure, want 1", len(created))
	}
	if !created[0].closed {
		t.Error("embedder created before the failure was not closed")
	}
}
