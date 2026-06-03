package freshen

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/scan"
)

func TestProcessBatchNormal(t *testing.T) {
	var cancelled, started bool
	var logMsgs []string

	opts := processOptions{
		root: ".",
		log: func(format string, _ ...any) {
			logMsgs = append(logMsgs, format)
		},
		runIncremental: func(_ context.Context, _ scan.IncrementalOptions) (*scan.Result, error) {
			return &scan.Result{Changed: 1, Removed: 0, Symbols: 5, Duration: time.Second}, nil
		},
		cancelEmbed: func() { cancelled = true },
		startEmbed:  func() { started = true },
	}

	batch := Batch{Changed: []string{"a.go"}, Removed: []string{}}
	err := processBatch(context.Background(), batch, opts)
	if err != nil {
		t.Fatalf("processBatch error: %v", err)
	}

	if !cancelled {
		t.Error("expected cancelEmbed to be called")
	}
	if !started {
		t.Error("expected startEmbed to be called")
	}
	if len(logMsgs) == 0 {
		t.Error("expected log message for re-index result")
	}
}

func TestProcessBatchEmpty(t *testing.T) {
	var called bool
	opts := processOptions{
		log: func(_ string, _ ...any) {},
		runIncremental: func(_ context.Context, _ scan.IncrementalOptions) (*scan.Result, error) {
			called = true
			return nil, nil
		},
		cancelEmbed: func() {},
		startEmbed:  func() {},
	}

	batch := Batch{Changed: []string{}, Removed: []string{}}
	err := processBatch(context.Background(), batch, opts)
	if err != nil {
		t.Fatalf("processBatch error: %v", err)
	}
	if called {
		t.Error("runIncremental should not be called for empty batch")
	}
}

func TestProcessBatchScanError(t *testing.T) {
	var started, logged bool

	scanErr := errors.New("scan failed")
	opts := processOptions{
		log: func(_ string, _ ...any) {
			logged = true
		},
		runIncremental: func(_ context.Context, _ scan.IncrementalOptions) (*scan.Result, error) {
			return nil, scanErr
		},
		cancelEmbed: func() {},
		startEmbed:  func() { started = true },
	}

	batch := Batch{Changed: []string{"a.go"}, Removed: []string{}}
	err := processBatch(context.Background(), batch, opts)
	if err == nil {
		t.Fatal("expected error from processBatch")
	}
	if !errors.Is(err, scanErr) {
		t.Fatalf("expected scanErr, got %v", err)
	}
	if started {
		t.Error("startEmbed should not be called after scan error")
	}
	if !logged {
		t.Error("expected log call for scan error")
	}
}

func TestProcessBatchDoesNotPanicOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := processOptions{
		log: func(_ string, _ ...any) {},
		runIncremental: func(_ context.Context, _ scan.IncrementalOptions) (*scan.Result, error) {
			return &scan.Result{}, nil
		},
		cancelEmbed: func() {},
		startEmbed:  func() {},
	}

	batch := Batch{Changed: []string{"a.go"}, Removed: []string{}}
	err := processBatch(ctx, batch, opts)
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmbedControllerStartDisabled(t *testing.T) {
	ec := &embedController{enabled: false}
	ec.Start()
	if ec.cancel != nil {
		t.Error("Start should not set cancel when disabled")
	}
}

func TestEmbedControllerStartCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ec := &embedController{
		enabled: true,
		ctx:     ctx,
		checkDebt: func(context.Context) (int, error) {
			return 10, nil
		},
	}
	ec.Start()
	if ec.cancel != nil {
		t.Error("Start should not set cancel when context is already cancelled")
	}
}

func TestEmbedControllerStartCheckDebtError(t *testing.T) {
	var logged string
	ec := &embedController{
		enabled: true,
		ctx:     context.Background(),
		log:     func(f string, a ...any) { logged = fmt.Sprintf(f, a...) },
		checkDebt: func(context.Context) (int, error) {
			return 0, errors.New("debt check failed")
		},
	}
	ec.Start()
	if ec.cancel != nil {
		t.Error("Start should not set cancel when checkDebt errors")
	}
	if !strings.Contains(logged, "check embedding debt") {
		t.Errorf("expected log about debt check error, got: %q", logged)
	}
}

func TestEmbedControllerStartZeroDebt(t *testing.T) {
	ec := &embedController{
		enabled: true,
		ctx:     context.Background(),
		checkDebt: func(context.Context) (int, error) {
			return 0, nil
		},
	}
	ec.Start()
	if ec.cancel != nil {
		t.Error("Start should not set cancel when debt is zero")
	}
}

func TestEmbedControllerStartDoubleStart(t *testing.T) {
	ec := &embedController{
		enabled: true,
		ctx:     context.Background(),
		log:     func(string, ...any) {},
		checkDebt: func(context.Context) (int, error) {
			return 5, nil
		},
		runEmbed: func(context.Context) (int, error) {
			return 1, nil
		},
	}
	ec.Start()
	if ec.cancel == nil {
		t.Fatal("Start should set cancel on first call")
	}

	// Second start should be a no-op
	ec.Start()
	ec.Cancel()
}

func TestEmbedControllerStartGoroutineSuccess(t *testing.T) {
	var logged string
	ec := &embedController{
		enabled: true,
		ctx:     context.Background(),
		log:     func(f string, a ...any) { logged = fmt.Sprintf(f, a...) },
		checkDebt: func(context.Context) (int, error) {
			return 5, nil
		},
		runEmbed: func(context.Context) (int, error) {
			return 3, nil
		},
	}
	ec.Start()
	if ec.cancel == nil {
		t.Fatal("Start should set cancel")
	}

	ec.Cancel()

	if !strings.Contains(logged, "background embed complete") {
		t.Errorf("expected log about completion, got: %q", logged)
	}
}

func TestEmbedControllerOnEmbeddedCalled(t *testing.T) {
	gotN := make(chan int, 1)
	ec := &embedController{
		enabled:    true,
		ctx:        context.Background(),
		log:        func(string, ...any) {},
		onEmbedded: func(_ context.Context, n int) { gotN <- n },
		checkDebt:  func(context.Context) (int, error) { return 5, nil },
		runEmbed:   func(context.Context) (int, error) { return 3, nil },
	}
	ec.Start()

	select {
	case n := <-gotN:
		if n != 3 {
			t.Errorf("onEmbedded n = %d, want 3", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onEmbedded")
	}
	ec.Cancel()
}

func TestEmbedControllerOnEmbeddedSkippedWhenZero(t *testing.T) {
	var called bool
	ec := &embedController{
		enabled:    true,
		ctx:        context.Background(),
		log:        func(string, ...any) {},
		onEmbedded: func(_ context.Context, _ int) { called = true },
		checkDebt:  func(context.Context) (int, error) { return 5, nil },
		runEmbed:   func(context.Context) (int, error) { return 0, nil },
	}
	ec.Start()
	ec.Cancel()
	if called {
		t.Error("onEmbedded should not be called when n == 0")
	}
}

func TestEmbedControllerStartGoroutineError(t *testing.T) {
	logCh := make(chan string, 1)
	ec := &embedController{
		enabled: true,
		ctx:     context.Background(),
		log:     func(f string, a ...any) { logCh <- fmt.Sprintf(f, a...) },
		checkDebt: func(context.Context) (int, error) {
			return 5, nil
		},
		runEmbed: func(context.Context) (int, error) {
			return 0, errors.New("embed failed")
		},
	}
	ec.Start()

	select {
	case logged := <-logCh:
		if !strings.Contains(logged, "background embed error") {
			t.Errorf("expected log about error, got: %q", logged)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for error log")
	}
	ec.Cancel()
}

func TestEmbedControllerStartGoroutineContextCancelled(t *testing.T) {
	var logged string
	ctx, cancel := context.WithCancel(context.Background())

	ec := &embedController{
		enabled: true,
		ctx:     ctx,
		log:     func(f string, a ...any) { logged = fmt.Sprintf(f, a...) },
		checkDebt: func(context.Context) (int, error) {
			return 5, nil
		},
		runEmbed: func(ectx context.Context) (int, error) {
			<-ectx.Done()
			return 0, ectx.Err()
		},
	}
	ec.Start()
	cancel()

	ec.Cancel()

	// When context is cancelled, the error should not be logged
	if strings.Contains(logged, "background embed error") {
		t.Error("should not log error when context is cancelled")
	}
}

func TestEmbedControllerCancelIdempotent(_ *testing.T) {
	ec := &embedController{}
	ec.Cancel()
	ec.Cancel()
}
