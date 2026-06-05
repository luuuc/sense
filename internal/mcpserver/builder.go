// Package mcpserver implements the `sense mcp` stdio server that
// exposes graph, blast, and status tools over the Model Context
// Protocol. Built on github.com/mark3labs/mcp-go — the de-facto Go
// MCP SDK. Each handler is a thin wrapper around the same engine code
// the CLI commands call, marshalled through internal/mcpio.
package mcpserver

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

const serverInstructions = mcpio.ServerInstructions

// RunOptions configures the MCP server.
type RunOptions struct {
	Dir        string
	WatchState *mcpio.WatchState // nil when not in watch mode

	// diag receives the bootstrap diagnostics (schema-rebuild and
	// embedding-model-mismatch warnings). It is an internal construction seam,
	// not part of the public API: it defaults to os.Stderr, and tests in this
	// package set it to capture and assert the warning text. Keeping it a
	// per-call field (never a package global) means concurrent servers cannot
	// stomp each other's output.
	diag io.Writer
}

// resolveDiag returns the writer for bootstrap diagnostics, defaulting to
// os.Stderr so production output is unchanged when no writer is injected.
func resolveDiag(opts RunOptions) io.Writer {
	if opts.diag != nil {
		return opts.diag
	}
	return os.Stderr
}

// deps are buildMCPServer's process-edge collaborators, injected so the
// orchestration error paths — a working-directory fault, a self-healing
// rebuild whose scan or reopen fails, a one-shot embed that finds no work or
// cannot reload its vectors — are reachable in a test without a live ONNX
// runtime or a forced on-disk corruption. Production wires the real functions
// via defaultDeps; the public Run / RunWithOptions signatures carry no seam.
// This mirrors the lifecycle-collaborator pattern internal/watch already uses.
type deps struct {
	getwd          func() (string, error)
	openIndex      func(context.Context, string) (*sqlite.Adapter, error)
	scanRun        func(context.Context, scan.Options) (*scan.Result, error)
	embedPending   func(context.Context, *sqlite.Adapter, string) (int, error)
	loadEmbeddings func(context.Context, *sqlite.Adapter) (map[int64][]float32, error)
}

// defaultDeps wires the builder's collaborators to the real implementations.
func defaultDeps() deps {
	return deps{
		getwd:        os.Getwd,
		openIndex:    cli.OpenIndex,
		scanRun:      scan.Run,
		embedPending: scan.EmbedPending,
		loadEmbeddings: func(ctx context.Context, a *sqlite.Adapter) (map[int64][]float32, error) {
			return a.LoadEmbeddings(ctx)
		},
	}
}

// Run starts the MCP stdio server with default options.
func Run(dir string) error {
	return RunWithOptions(RunOptions{Dir: dir})
}

// RunWithOptions starts the MCP stdio server with explicit options.
func RunWithOptions(opts RunOptions) error {
	s, _, cleanup, err := buildMCPServer(opts)
	if err != nil {
		return err
	}
	defer cleanup()
	return server.ServeStdio(s)
}

// buildMCPServer creates the MCP server and handlers without starting stdio
// transport. Returns the server, handlers, a cleanup function, and any error.
// It reads as a sequence of named steps: resolve the directory, open the index
// (rebuilding on a schema mismatch), warn on a model mismatch, build the search
// engine, start the background freshener and one-shot embed, then assemble the
// server.
func buildMCPServer(opts RunOptions) (*server.MCPServer, *handlers, func(), error) {
	return buildMCPServerWithDeps(opts, defaultDeps())
}

// buildMCPServerWithDeps is buildMCPServer's testable core: identical
// orchestration, but the process edges arrive through d so a test can drive the
// error paths. Production calls it through buildMCPServer with defaultDeps.
func buildMCPServerWithDeps(opts RunOptions, d deps) (*server.MCPServer, *handlers, func(), error) {
	diag := resolveDiag(opts)

	dir, err := resolveDir(opts, d)
	if err != nil {
		return nil, nil, nil, err
	}

	ctx := context.Background()
	adapter, err := openIndexWithRebuild(ctx, dir, diag, d)
	if err != nil {
		return nil, nil, nil, err
	}

	warnModelMismatch(ctx, adapter, diag)

	engine, embedder, err := search.BuildEngine(ctx, adapter, dir)
	if err != nil {
		_ = adapter.Close()
		return nil, nil, nil, fmt.Errorf("sense mcp: %w", err)
	}

	embeddingsEnabled := cli.EmbeddingsEnabled(dir)

	freshenSvc, watchState, serviceStarted := startFreshenService(ctx, dir, embeddingsEnabled, opts.WatchState, adapter, engine)
	cancelEmbed, embedDone := startOneShotEmbed(ctx, dir, embeddingsEnabled, serviceStarted, opts.WatchState, adapter, engine, d)

	tracker := metrics.NewTracker(adapter.DB())

	s := server.NewMCPServer(
		"sense",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithInstructions(serverInstructions),
	)

	defaults := profile.DefaultParams()
	textFallback := search.NewTextFallback()

	h := &handlers{adapter: adapter, db: adapter.DB(), dir: dir, search: engine, textFallback: textFallback, watchState: watchState, freshen: freshenSvc, tracker: tracker, defaults: defaults, seenSymbols: make(map[int64]bool)}

	s.AddTool(searchTool(), withAliasing("sense_search", h.handleSearch))
	s.AddTool(graphTool(), withAliasing("sense_graph", h.handleGraph))
	s.AddTool(blastTool(), withAliasing("sense_blast", h.handleBlast))
	s.AddTool(conventionsTool(), withAliasing("sense_conventions", h.handleConventions))
	s.AddTool(statusTool(), withAliasing("sense_status", h.handleStatus))

	cleanup := func() {
		// Stop the watcher first so no further re-index batch starts, then
		// drain any one-shot embed, then release the read-side resources.
		if freshenSvc != nil {
			freshenSvc.Stop()
		}
		cancelEmbed()
		<-embedDone
		if embedder != nil {
			_ = embedder.Close()
		}
		tracker.Close()
		_ = adapter.Close()
	}

	return s, h, cleanup, nil
}

// resolveDir returns the index directory, falling back to the working
// directory when Dir is blank.
func resolveDir(opts RunOptions, d deps) (string, error) {
	if opts.Dir != "" {
		return opts.Dir, nil
	}
	wd, err := d.getwd()
	if err != nil {
		return "", fmt.Errorf("sense mcp: getwd: %w", err)
	}
	return wd, nil
}

// openIndexWithRebuild opens the index and, when cli.OpenIndex reports a schema
// version mismatch, runs a fresh scan and reopens before returning. The two
// rebuild diagnostics are written to diag.
func openIndexWithRebuild(ctx context.Context, dir string, diag io.Writer, d deps) (*sqlite.Adapter, error) {
	adapter, err := d.openIndex(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("sense mcp: %w", err)
	}
	if !adapter.Rebuilt {
		return adapter, nil
	}

	_ = adapter.Close()
	_, _ = fmt.Fprintf(diag, "sense mcp: schema version mismatch — rebuilding index...\n")
	if _, err := d.scanRun(ctx, scan.Options{
		Root:              dir,
		Output:            os.Stderr,
		Warnings:          os.Stderr,
		EmbeddingsEnabled: cli.EmbeddingsEnabled(dir),
		Embed:             true,
	}); err != nil {
		return nil, fmt.Errorf("sense mcp: rebuild scan: %w", err)
	}
	adapter, err = d.openIndex(ctx, dir)
	if err != nil {
		return nil, fmt.Errorf("sense mcp: reopen after rebuild: %w", err)
	}
	_, _ = fmt.Fprintf(diag, "sense mcp: rebuild complete\n")
	return adapter, nil
}

// warnModelMismatch writes a degradation warning to diag when the index was
// embedded with a model different from the binary's. It is advisory only — the
// engine still serves the existing index.
func warnModelMismatch(ctx context.Context, adapter *sqlite.Adapter, diag io.Writer) {
	if storedModel, _ := adapter.ReadMeta(ctx, "embedding_model"); storedModel != "" && storedModel != embed.ModelID {
		_, _ = fmt.Fprintf(diag, "sense mcp: embedding model changed (index: %s, binary: %s). Search results may be degraded. Run `sense scan --rebuild` to re-embed.\n", storedModel, embed.ModelID)
	}
}

// startFreshenService starts a server-hosted freshen.Service when no external
// watcher owns the index and watching is enabled, so the index stays current
// for the whole session. It returns the service (nil when not started), the
// watch state the server should report freshness against, and whether the
// service started. The Service owns embedding when it runs; the one-shot embed
// is the fallback for when it does not.
func startFreshenService(ctx context.Context, dir string, embeddingsEnabled bool, externalWatch *mcpio.WatchState, adapter *sqlite.Adapter, engine *search.Engine) (*freshen.Service, *mcpio.WatchState, bool) {
	if externalWatch != nil || !config.IsWatchEnabled(dir) {
		return nil, externalWatch, false
	}

	ws := &mcpio.WatchState{}
	svc, serr := freshen.NewService(freshen.Config{
		Root:              dir,
		EmbeddingsEnabled: embeddingsEnabled,
		WatchState:        ws,
		Log:               func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) },
		OnEmbedded:        upgradeSearchOnEmbed(adapter, engine),
	})
	if serr != nil {
		fmt.Fprintf(os.Stderr, "sense mcp: background watcher unavailable, index will not auto-refresh: %v\n", serr)
		return nil, externalWatch, false
	}
	if err := svc.Start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "sense mcp: background watcher unavailable, index will not auto-refresh: %v\n", err)
		svc.Stop()
		return nil, externalWatch, false
	}
	return svc, ws, true
}

// startOneShotEmbed closes the index's embedding debt once in the background
// when embeddings are enabled, no freshen.Service runs them, and no external
// watcher owns the index. On success it swaps the freshly built vectors into
// the live engine, upgrading search to hybrid in place. It always returns a
// cancel function and a done channel (closed immediately when no embed runs)
// so cleanup can drain it uniformly.
func startOneShotEmbed(ctx context.Context, dir string, embeddingsEnabled, serviceStarted bool, externalWatch *mcpio.WatchState, adapter *sqlite.Adapter, engine *search.Engine, d deps) (context.CancelFunc, <-chan struct{}) {
	embedCtx, cancelEmbed := context.WithCancel(ctx)
	embedDone := make(chan struct{})

	hasDebt := false
	if embeddingsEnabled && !serviceStarted && externalWatch == nil {
		if debtCount, _ := adapter.EmbeddingDebtCount(ctx); debtCount > 0 {
			hasDebt = true
		}
	}
	if !hasDebt {
		close(embedDone)
		return cancelEmbed, embedDone
	}

	go func() {
		defer close(embedDone)
		n, err := d.embedPending(embedCtx, adapter, dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sense mcp: background embed failed: %v\n", err)
			return
		}
		if n == 0 {
			return
		}
		// Rebuild the flat index from the freshly written embeddings so
		// search upgrades from keyword-only to hybrid in place.
		embeddings, lerr := d.loadEmbeddings(embedCtx, adapter)
		if lerr != nil {
			fmt.Fprintf(os.Stderr, "sense mcp: reload embeddings after embed: %v\n", lerr)
			return
		}
		engine.SetVectors(search.BuildFlatIndex(embeddings))
		fmt.Fprintf(os.Stderr, "sense mcp: embeddings complete (%d symbols) — search upgraded to hybrid mode\n", n)
	}()

	return cancelEmbed, embedDone
}

// upgradeSearchOnEmbed returns an OnEmbedded callback that reloads the
// freshly written embeddings and swaps them into the live search engine,
// upgrading search from keyword-only to hybrid in place. It reads through
// the server's own adapter, which sees the Service's committed writes via
// WAL. Errors are logged and skipped; the next embed round retries.
func upgradeSearchOnEmbed(adapter *sqlite.Adapter, engine *search.Engine) func(context.Context, int) {
	return func(ctx context.Context, n int) {
		embeddings, lerr := adapter.LoadEmbeddings(ctx)
		if lerr != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(os.Stderr, "sense mcp: reload embeddings after embed: %v\n", lerr)
			}
			return
		}
		engine.SetVectors(search.BuildFlatIndex(embeddings))
		fmt.Fprintf(os.Stderr, "sense mcp: embeddings complete (%d symbols) — search upgraded to hybrid mode\n", n)
	}
}
