package mcpserver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
	"github.com/luuuc/sense/internal/sqlite"
)

// hermeticEnv disables embeddings and the background watcher so a build
// exercises the bootstrap branches without ONNX, a writer lock, or
// background goroutines. The rebuild path runs a real scan, which stays
// ONNX-free only while SENSE_EMBEDDINGS is off.
func hermeticEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SENSE_EMBEDDINGS", "false")
	t.Setenv("SENSE_WATCH", "false")
}

// symbolCount reports how many symbols with the given name are in the index
// the handlers serve, proving a (re)scan populated it.
func symbolCount(t *testing.T, h *handlers, name string) int {
	t.Helper()
	var n int
	if err := h.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sense_symbols WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("count symbols: %v", err)
	}
	return n
}

// staleUserVersion stamps a non-zero schema version different from the
// current one onto the index, so the next open takes sqlite's rebuild path
// (adapter.Rebuilt == true).
func staleUserVersion(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	db, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open for stamp: %v", err)
	}
	defer func() { _ = db.Close() }()
	// Any non-zero version that differs from SchemaVersion triggers a rebuild.
	if _, err := db.DB().ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatalf("stamp stale user_version: %v", err)
	}
}

// TestBuildMCPServerRebuildsOnSchemaMismatch drives buildMCPServer's
// upgrade-time rebuild branch: an index whose stored schema version is stale
// is detected by cli.OpenIndex (adapter.Rebuilt), so the server runs a fresh
// scan and reopens before building the engine. Embeddings are explicitly off
// so the real rebuild scan stays ONNX-free.
func TestBuildMCPServerRebuildsOnSchemaMismatch(t *testing.T) {
	hermeticEnv(t)

	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	repo.Scan(scan.Options{})
	staleUserVersion(t, repo.Root)

	var diag bytes.Buffer
	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: repo.Root, diag: &diag})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	if s == nil || h == nil {
		t.Fatal("rebuild branch should still build a server and handlers")
	}
	// The rebuild scan repopulated the index from source.
	if got := symbolCount(t, h, "Hello"); got == 0 {
		t.Error("rebuild scan should have repopulated the index, but Hello is absent")
	}
	// The injected writer captures the rebuild diagnostics by text.
	if out := diag.String(); !strings.Contains(out, "schema version mismatch") || !strings.Contains(out, "rebuild complete") {
		t.Errorf("expected rebuild diagnostics in diag, got: %q", out)
	}
}

// TestBuildMCPServerWarnsOnModelMismatch drives the embedding-model warning
// branch: the stored model differs from the binary's, so buildMCPServer warns
// yet still builds the engine. With the 27-05 io.Writer seam the warning text
// is captured through an injected writer, so the assertion is both the branch
// outcome — engine built despite the mismatch — and the warning itself.
func TestBuildMCPServerWarnsOnModelMismatch(t *testing.T) {
	hermeticEnv(t)

	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	_, adapter := repo.Scan(scan.Options{})
	if err := adapter.WriteMeta(context.Background(), "embedding_model", "stale-model-v0"); err != nil {
		t.Fatalf("write stale model meta: %v", err)
	}

	var diag bytes.Buffer
	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: repo.Root, diag: &diag})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	if s == nil || h == nil {
		t.Fatal("model-mismatch warning must not stop the server from building")
	}
	if got := symbolCount(t, h, "Hello"); got == 0 {
		t.Error("engine should serve the existing index despite the model warning")
	}
	if out := diag.String(); !strings.Contains(out, "embedding model changed") || !strings.Contains(out, "stale-model-v0") {
		t.Errorf("expected model-mismatch warning in diag, got: %q", out)
	}
}

// TestResolveDiagDefaultsToStderr pins the seam's default: with no writer
// injected, bootstrap diagnostics go to os.Stderr byte-for-byte (production
// output is unchanged), and an injected writer is used verbatim.
func TestResolveDiagDefaultsToStderr(t *testing.T) {
	if got := resolveDiag(RunOptions{}); got != os.Stderr {
		t.Errorf("default diag = %v, want os.Stderr", got)
	}
	var buf bytes.Buffer
	if got := resolveDiag(RunOptions{diag: &buf}); got != &buf {
		t.Error("injected diag writer should be returned verbatim")
	}
}

// TestBuildMCPServerHostsBackgroundWatcher covers the self-hosted freshening
// branch: with no external watcher (WatchState nil) and watching enabled, the
// server starts its own freshen.Service so the index stays current for the
// session. Embeddings are off, so the Service runs ONNX-free; cleanup stops
// it deterministically.
func TestBuildMCPServerHostsBackgroundWatcher(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	t.Setenv("SENSE_WATCH", "true")

	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	repo.Scan(scan.Options{})

	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: repo.Root})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	if s == nil {
		t.Fatal("expected a server")
	}
	if h.freshen == nil {
		t.Error("expected the server to host its own freshen.Service when watching is enabled")
	}
}

// TestBuildMCPServerDegradesWhenWatcherUnavailable covers the graceful
// degrade branch: watching is enabled, but the freshen.Service cannot be
// constructed (here, a malformed config.yml fails its config load). The
// server warns and still builds, serving queries read-only without auto
// refresh.
func TestBuildMCPServerDegradesWhenWatcherUnavailable(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	t.Setenv("SENSE_WATCH", "true")

	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	repo.Scan(scan.Options{})
	// Malformed config.yml: cli.OpenIndex still works (it only opens sqlite),
	// but freshen.NewService's config load fails, so the watcher is skipped.
	repo.Write(filepath.Join(".sense", "config.yml"), "ignore: [unterminated\n")

	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: repo.Root})
	if err != nil {
		t.Fatalf("buildMCPServer should degrade, not error: %v", err)
	}
	defer cleanup()

	if s == nil {
		t.Fatal("expected a server even when the watcher is unavailable")
	}
	if h.freshen != nil {
		t.Error("freshen.Service should be nil when it could not be constructed")
	}
}

// TestBuildMCPServerOneShotEmbedFallback covers the one-shot background embed
// branch: embeddings are enabled, but no freshen.Service runs (watching off)
// and no external watcher owns the index, so the server itself closes the
// embedding debt once and upgrades search to hybrid in place. This path needs
// the bundled embedder (real ONNX inference), so it skips when the model is
// unavailable, mirroring the other embedding-path tests. It runs under the
// coverage gate's onnx_integration build, which fetches the model.
func TestBuildMCPServerOneShotEmbedFallback(t *testing.T) {
	probe, err := embed.NewBundledEmbedder(0)
	if err != nil {
		t.Skipf("bundled embedder unavailable: %v", err)
	}
	_ = probe.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	t.Setenv("SENSE_WATCH", "false") // no Service → the one-shot embed is the fallback

	// A real repo on disk with outstanding embedding debt (scanned with
	// embeddings off), so the fallback's EmbedPending has source to embed.
	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	repo.Scan(scan.Options{})

	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: repo.Root})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()
	if s == nil {
		t.Fatal("expected a server")
	}

	// Let the fallback embed drain the debt before cleanup cancels its
	// context, so the success path (load + upgrade search in place) runs
	// rather than the cancel branch. We assert the observable outcome — debt
	// reaches zero — not the timing of the in-place SetVectors upgrade.
	waitForEmbedDebtZero(t, repo.Root)
}

// waitForEmbedDebtZero polls the index until the embedding debt drains to
// zero, proving the background embed committed. It opens its own read
// connection so it observes the embed goroutine's WAL writes.
func waitForEmbedDebtZero(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open for debt poll: %v", err)
	}
	defer func() { _ = a.Close() }()

	deadline := time.After(15 * time.Second)
	for {
		n, err := a.EmbeddingDebtCount(ctx)
		if err == nil && n == 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("embedding debt did not drain (last count err=%v)", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// TestBuildMCPServerDefaultsDirToCWD covers the empty-Dir branch: when Dir is
// blank, buildMCPServer falls back to the working directory.
func TestBuildMCPServerDefaultsDirToCWD(t *testing.T) {
	hermeticEnv(t)

	repo := scantest.NewRepo(t, map[string]string{
		"main.go": "package main\n\nfunc Hello() {}\n",
	})
	repo.Scan(scan.Options{})
	t.Chdir(repo.Root)

	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: ""})
	if err != nil {
		t.Fatalf("buildMCPServer with empty Dir: %v", err)
	}
	defer cleanup()
	if s == nil {
		t.Fatal("empty Dir should default to the working directory and build a server")
	}
}
