package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpserver"
	"github.com/luuuc/sense/internal/scan"
)

func TestRunOptionsDefaults(t *testing.T) {
	var opts RunOptions
	if opts.Root != "" {
		t.Error("RunOptions.Root zero value should be empty")
	}
	if opts.EmbeddingsEnabled {
		t.Error("RunOptions.EmbeddingsEnabled zero value should be false")
	}
	if opts.MCP {
		t.Error("RunOptions.MCP zero value should be false")
	}
}

// TestRunInitializesAndExits verifies that the real Run wiring performs its
// initialization (initial scan, service start) end to end and then exits
// cleanly when the context is cancelled. This drives defaultDeps so the
// production scan + freshen.Service path stays exercised alongside the
// fake-driven lifecycle tests below.
func TestRunInitializesAndExits(t *testing.T) {
	dir := t.TempDir()

	// Create a minimal project structure.
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run should initialize and then exit when the context times out.
	err := Run(ctx, RunOptions{Root: dir})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
}

// fakeService stands in for *freshen.Service so the lifecycle tests drive
// Run's orchestration without a real index, watcher, or write adapter.
type fakeService struct {
	startErr error
	started  bool
	stopped  bool
}

func (f *fakeService) Start(context.Context) error { f.started = true; return f.startErr }
func (f *fakeService) Stop()                       { f.stopped = true }

// okScan is a no-op initial scan that reports success.
func okScan(context.Context, scan.Options) (*scan.Result, error) {
	return &scan.Result{}, nil
}

// TestRunInitialScanError returns the wrapped scan error and never reaches
// service creation.
func TestRunInitialScanError(t *testing.T) {
	wantErr := errors.New("boom")
	newServiceCalled := false
	d := deps{
		scan: func(context.Context, scan.Options) (*scan.Result, error) {
			return nil, wantErr
		},
		newService: func(freshen.Config) (serviceRunner, error) {
			newServiceCalled = true
			return &fakeService{}, nil
		},
		runServer: func(mcpserver.RunOptions) error { return nil },
	}

	// Empty Root also exercises the default-to-"." branch; the scan fails
	// before any filesystem work, so no real index is needed.
	err := run(context.Background(), RunOptions{Root: ""}, d)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run error = %v, want wrapped %v", err, wantErr)
	}
	if newServiceCalled {
		t.Error("newService must not be called after an initial-scan failure")
	}
}

// TestRunNewServiceError surfaces a service-construction failure.
func TestRunNewServiceError(t *testing.T) {
	wantErr := errors.New("no index")
	d := deps{
		scan: okScan,
		newService: func(freshen.Config) (serviceRunner, error) {
			return nil, wantErr
		},
		runServer: func(mcpserver.RunOptions) error { return nil },
	}

	err := run(context.Background(), RunOptions{Root: t.TempDir()}, d)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run error = %v, want %v", err, wantErr)
	}
}

// TestRunServiceStartError surfaces a Start failure and does not leave the
// service stopped (Start failed before the defer was armed).
func TestRunServiceStartError(t *testing.T) {
	wantErr := errors.New("start failed")
	fake := &fakeService{startErr: wantErr}
	d := deps{
		scan:       okScan,
		newService: func(freshen.Config) (serviceRunner, error) { return fake, nil },
		runServer:  func(mcpserver.RunOptions) error { return nil },
	}

	err := run(context.Background(), RunOptions{Root: t.TempDir()}, d)
	if !errors.Is(err, wantErr) {
		t.Fatalf("run error = %v, want %v", err, wantErr)
	}
	if fake.stopped {
		t.Error("Stop must not run when Start failed")
	}
}

// TestRunCoHostsAndShutsDownCleanly drives the co-host goroutine and a
// context-cancel shutdown with fakes, and asserts a zero goroutine-leak: the
// MCP co-host goroutine the orchestrator spawned has fully unwound after Run
// returns. The fake runServer blocks on the test context exactly as
// ServeStdio blocks on stdin, so cancellation unwinds deterministically.
func TestRunCoHostsAndShutsDownCleanly(t *testing.T) {
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	serverReturned := make(chan struct{})
	fake := &fakeService{}
	d := deps{
		scan:       okScan,
		newService: func(freshen.Config) (serviceRunner, error) { return fake, nil },
		runServer: func(mcpserver.RunOptions) error {
			close(started)
			<-ctx.Done() // mirror ServeStdio blocking until shutdown
			close(serverReturned)
			return nil
		},
	}

	done := make(chan error, 1)
	go func() { done <- run(ctx, RunOptions{Root: t.TempDir(), MCP: true}, d) }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("co-host server was never started")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error on clean shutdown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not shut down after cancel")
	}

	select {
	case <-serverReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("co-host goroutine did not exit")
	}

	if !fake.started {
		t.Error("service Start was not called")
	}
	if !fake.stopped {
		t.Error("service Stop was not called on shutdown")
	}
	assertNoLeak(t, base)
}

// TestRunCoHostServerErrorTriggersShutdown covers the co-host goroutine's
// error branch: when the MCP server returns an error, the goroutine cancels
// the run context, so Run unwinds and stops the service.
func TestRunCoHostServerErrorTriggersShutdown(t *testing.T) {
	base := runtime.NumGoroutine()

	fake := &fakeService{}
	d := deps{
		scan:       okScan,
		newService: func(freshen.Config) (serviceRunner, error) { return fake, nil },
		runServer: func(mcpserver.RunOptions) error {
			return errors.New("mcp server crashed")
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- run(context.Background(), RunOptions{Root: t.TempDir(), MCP: true}, d)
	}()

	select {
	case err := <-done:
		// The goroutine's cancel() drives a clean shutdown, so Run returns nil.
		if err != nil {
			t.Fatalf("run returned error, want nil after co-host self-cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("co-host server error did not trigger shutdown")
	}

	if !fake.stopped {
		t.Error("service Stop was not called after co-host error")
	}
	assertNoLeak(t, base)
}

// assertNoLeak polls until the live goroutine count returns to base, failing
// if the orchestrator leaked the goroutines it was meant to manage. Polling
// (rather than a single read) absorbs scheduler settle without asserting on
// timing.
func assertNoLeak(t *testing.T, base int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if runtime.NumGoroutine() <= base {
			return
		}
		select {
		case <-deadline:
			t.Errorf("goroutine leak: have %d, want <= %d", runtime.NumGoroutine(), base)
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}
