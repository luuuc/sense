package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
	"github.com/luuuc/sense/internal/sqlite"
)

// These tests drive buildMCPServer's process-edge error paths through the
// injected deps — the getwd fault, a self-healing rebuild whose scan or reopen
// fails, and a one-shot embed that finds no work or cannot reload its vectors.
// They are why mcpserver/builder.go no longer needs a straggler exception: the
// orchestration boundary is now a controllable seam, not an inline edge.

func TestBuildMCPServerWithDepsGetwdFault(t *testing.T) {
	d := defaultDeps()
	d.getwd = func() (string, error) { return "", errors.New("boom getwd") }

	// Dir blank forces resolveDir down the getwd branch.
	_, _, _, err := buildMCPServerWithDeps(RunOptions{Dir: ""}, d)
	if err == nil {
		t.Fatal("expected a getwd error to propagate, got nil")
	}
	if !strings.Contains(err.Error(), "getwd") {
		t.Errorf("error = %v, want it to mention getwd", err)
	}
}

func TestBuildMCPServerWithDepsRebuildScanFails(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n"})
	repo.Scan(scan.Options{})
	staleUserVersion(t, repo.Root) // makes cli.OpenIndex report Rebuilt

	d := defaultDeps()
	d.scanRun = func(context.Context, scan.Options) (*scan.Result, error) {
		return nil, errors.New("rebuild scan boom")
	}

	_, _, _, err := buildMCPServerWithDeps(RunOptions{Dir: repo.Root}, d)
	if err == nil || !strings.Contains(err.Error(), "rebuild scan") {
		t.Fatalf("expected rebuild-scan error, got %v", err)
	}
}

func TestBuildMCPServerWithDepsReopenAfterRebuildFails(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n"})
	repo.Scan(scan.Options{})
	staleUserVersion(t, repo.Root)

	base := defaultDeps()
	calls := 0
	d := base
	d.openIndex = func(ctx context.Context, dir string) (*sqlite.Adapter, error) {
		calls++
		if calls == 1 {
			// First open: the genuine (now Rebuilt-flagged) adapter.
			return base.openIndex(ctx, dir)
		}
		// Reopen after the (faked-successful) rebuild scan fails.
		return nil, errors.New("reopen boom")
	}
	d.scanRun = func(context.Context, scan.Options) (*scan.Result, error) { return nil, nil }

	_, _, _, err := buildMCPServerWithDeps(RunOptions{Dir: repo.Root}, d)
	if err == nil || !strings.Contains(err.Error(), "reopen after rebuild") {
		t.Fatalf("expected reopen-after-rebuild error, got %v", err)
	}
	if calls != 2 {
		t.Errorf("expected openIndex called twice (open + reopen), got %d", calls)
	}
}

func TestStartOneShotEmbedNoPendingWorkClosesQuietly(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()
	if debt, _ := ts.handlers.adapter.EmbeddingDebtCount(ctx); debt == 0 {
		t.Skip("fixture unexpectedly has no embedding debt")
	}

	d := defaultDeps()
	// Debt is present so the goroutine runs, but EmbedPending reports it embedded
	// nothing (a concurrent embedder drained it) — the n == 0 early return.
	d.embedPending = func(context.Context, *sqlite.Adapter, string) (int, error) { return 0, nil }

	cancel, done := startOneShotEmbed(ctx, ts.handlers.dir, true, false, nil,
		ts.handlers.adapter, ts.handlers.search, d)
	defer cancel()
	<-done // must close; the n == 0 branch returns without reloading vectors
}

func TestStartOneShotEmbedReloadFailureClosesQuietly(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()
	if debt, _ := ts.handlers.adapter.EmbeddingDebtCount(ctx); debt == 0 {
		t.Skip("fixture unexpectedly has no embedding debt")
	}

	d := defaultDeps()
	// Embed reports work done, but reloading the fresh vectors fails — the
	// goroutine logs and exits without swapping vectors into the live engine.
	d.embedPending = func(context.Context, *sqlite.Adapter, string) (int, error) { return 3, nil }
	d.loadEmbeddings = func(context.Context, *sqlite.Adapter) (map[int64][]float32, error) {
		return nil, errors.New("reload boom")
	}

	cancel, done := startOneShotEmbed(ctx, ts.handlers.dir, true, false, nil,
		ts.handlers.adapter, ts.handlers.search, d)
	defer cancel()
	<-done
}
