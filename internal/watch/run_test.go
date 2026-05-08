package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/scan"
)

func TestRunOptionsMCPDefaultFalse(t *testing.T) {
	var opts RunOptions
	if opts.MCP {
		t.Error("RunOptions.MCP zero value should be false")
	}
}

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
	if err != nil && err != context.Canceled {
		t.Errorf("unexpected error: %v", err)
	}
}
