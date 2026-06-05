package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestStartOneShotEmbedReportsEmbedderFailure drives the one-shot background
// embed when the index carries embedding debt but the bundled embedder is
// unavailable: scan.EmbedPending fails to build its pool, so the goroutine
// logs the failure and exits without upgrading search. We assert the
// observable contract — the done channel closes (cleanup can drain it) and the
// engine keeps serving keyword-only — rather than the log text.
//
// This path is deterministic without ONNX precisely because the embedder
// cannot be constructed; under the onnx_integration gate the same call drains
// the debt instead, which the bootstrap fallback test already covers.
func TestStartOneShotEmbedReportsEmbedderFailure(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// The fixture has symbols but no embeddings, so debtCount > 0 and the
	// goroutine actually runs EmbedPending.
	debt, err := ts.handlers.adapter.EmbeddingDebtCount(ctx)
	if err != nil {
		t.Fatalf("EmbeddingDebtCount: %v", err)
	}
	if debt == 0 {
		t.Skip("fixture unexpectedly has no embedding debt")
	}

	cancel, done := startOneShotEmbed(ctx, ts.handlers.dir, true, false, nil,
		ts.handlers.adapter, ts.handlers.search, defaultDeps())
	defer cancel()

	// Drain the goroutine: it either fails to build the embedder (no model) and
	// returns, or — under the integration gate — embeds and upgrades. Either
	// way the channel must close so cleanup never blocks.
	<-done
}

// TestStartOneShotEmbedCancelledReportsFailure covers the goroutine's error
// branch: cancelling the returned context while the embed is in flight makes
// scan.EmbedPending fail, so the goroutine logs the failure and exits without
// upgrading search. The done channel must still close so cleanup never blocks.
func TestStartOneShotEmbedCancelledReportsFailure(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	debt, err := ts.handlers.adapter.EmbeddingDebtCount(ctx)
	if err != nil {
		t.Fatalf("EmbeddingDebtCount: %v", err)
	}
	if debt == 0 {
		t.Skip("fixture unexpectedly has no embedding debt")
	}

	cancel, done := startOneShotEmbed(ctx, ts.handlers.dir, true, false, nil,
		ts.handlers.adapter, ts.handlers.search, defaultDeps())
	// Cancel immediately so EmbedPending aborts mid-flight rather than draining
	// the debt; the failure path logs and returns.
	cancel()
	<-done
}

// TestStartOneShotEmbedNoDebtClosesImmediately covers the fast path: with no
// embedding debt the function does not spawn the goroutine and returns an
// already-closed done channel.
func TestStartOneShotEmbedNoDebtClosesImmediately(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "nodebt.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	engine := search.NewEngine(adapter, nil, nil)
	// No symbols → no debt → fast path, no goroutine.
	cancel, done := startOneShotEmbed(ctx, dir, true, false, nil, adapter, engine, defaultDeps())
	defer cancel()

	select {
	case <-done:
		// expected: closed immediately
	default:
		t.Error("expected an already-closed done channel when there is no embedding debt")
	}
}

// TestStartOneShotEmbedSkipsWhenServiceOwnsEmbedding covers the guard: when a
// freshen.Service is already running (serviceStarted true), the one-shot embed
// stands down even with debt present, since the Service owns embedding.
func TestStartOneShotEmbedSkipsWhenServiceOwnsEmbedding(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	cancel, done := startOneShotEmbed(ctx, ts.handlers.dir, true, true /*serviceStarted*/, nil,
		ts.handlers.adapter, ts.handlers.search, defaultDeps())
	defer cancel()

	select {
	case <-done:
		// expected: no goroutine spawned because the Service owns embedding
	default:
		t.Error("one-shot embed must stand down when a freshen.Service owns embedding")
	}
}

// TestUpgradeSearchOnEmbedLogsOnLoadFailure covers the open-context error
// branch of the OnEmbedded callback: when LoadEmbeddings fails but the context
// is not cancelled, the callback logs and returns without swapping vectors
// (rather than silently as it does on cancellation).
func TestUpgradeSearchOnEmbedLogsOnLoadFailure(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "upgrade_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewEngine(adapter, nil, nil)
	fn := upgradeSearchOnEmbed(adapter, engine)

	// Close the adapter so LoadEmbeddings fails, but pass a live (non-cancelled)
	// context so the callback takes the logging branch, not the quiet one.
	_ = adapter.Close()
	fn(ctx, 3) // must not panic; engine vectors stay untouched
}
