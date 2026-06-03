// Package mcpserver implements the `sense mcp` stdio server that
// exposes graph, blast, and status tools over the Model Context
// Protocol. Built on github.com/mark3labs/mcp-go — the de-facto Go
// MCP SDK. Each handler is a thin wrapper around the same engine code
// the CLI commands call, marshalled through internal/mcpio.
package mcpserver

import (
	"context"
	"fmt"
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
//
//nolint:gocyclo,gocognit // 27-05: retired by the mcpserver split
func buildMCPServer(opts RunOptions) (*server.MCPServer, *handlers, func(), error) {
	dir := opts.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("sense mcp: getwd: %w", err)
		}
		dir = wd
	}

	ctx := context.Background()
	adapter, err := cli.OpenIndex(ctx, dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sense mcp: %w", err)
	}

	if adapter.Rebuilt {
		_ = adapter.Close()
		fmt.Fprintf(os.Stderr, "sense mcp: schema version mismatch — rebuilding index...\n")
		if _, err := scan.Run(ctx, scan.Options{
			Root:              dir,
			Output:            os.Stderr,
			Warnings:          os.Stderr,
			EmbeddingsEnabled: cli.EmbeddingsEnabled(dir),
			Embed:             true,
		}); err != nil {
			return nil, nil, nil, fmt.Errorf("sense mcp: rebuild scan: %w", err)
		}
		adapter, err = cli.OpenIndex(ctx, dir)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("sense mcp: reopen after rebuild: %w", err)
		}
		fmt.Fprintf(os.Stderr, "sense mcp: rebuild complete\n")
	}

	if storedModel, _ := adapter.ReadMeta(ctx, "embedding_model"); storedModel != "" && storedModel != embed.ModelID {
		fmt.Fprintf(os.Stderr, "sense mcp: embedding model changed (index: %s, binary: %s). Search results may be degraded. Run `sense scan --rebuild` to re-embed.\n", storedModel, embed.ModelID)
	}

	engine, embedder, err := search.BuildEngine(ctx, adapter, dir)
	if err != nil {
		_ = adapter.Close()
		return nil, nil, nil, fmt.Errorf("sense mcp: %w", err)
	}

	embeddingsEnabled := cli.EmbeddingsEnabled(dir)

	// Background freshening. When no external watcher owns this index
	// (WatchState == nil), the server hosts its own freshen.Service so the
	// index stays current for the whole session — catching edits from any
	// source, not just the agent's Write/Edit. The Service owns embedding;
	// the one-shot embed below only runs as a fallback when the Service is
	// absent. When an external watcher co-hosts this server (WatchState !=
	// nil), the server defers watching and embedding to that process.
	var (
		freshenSvc     *freshen.Service
		watchState     = opts.WatchState
		serviceStarted bool
	)
	if opts.WatchState == nil && config.IsWatchEnabled(dir) {
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
		} else if err := svc.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "sense mcp: background watcher unavailable, index will not auto-refresh: %v\n", err)
			svc.Stop()
		} else {
			freshenSvc = svc
			serviceStarted = true
			watchState = ws
		}
	}

	// One-shot background embed: only when the Service is not running it.
	// That is, the index has embedding debt, embeddings are enabled, no
	// external watcher owns the index, and the Service failed to start.
	embedCtx, cancelEmbed := context.WithCancel(ctx)
	embedDone := make(chan struct{})
	hasDebt := false
	if embeddingsEnabled && !serviceStarted && opts.WatchState == nil {
		if debtCount, _ := adapter.EmbeddingDebtCount(ctx); debtCount > 0 {
			hasDebt = true
		}
	}
	if hasDebt {
		go func() {
			defer close(embedDone)
			n, err := scan.EmbedPending(embedCtx, adapter, dir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sense mcp: background embed failed: %v\n", err)
				return
			}
			if n == 0 {
				return
			}
			// Rebuild the flat index from the freshly written embeddings so
			// search upgrades from keyword-only to hybrid in place.
			embeddings, lerr := adapter.LoadEmbeddings(embedCtx)
			if lerr != nil {
				fmt.Fprintf(os.Stderr, "sense mcp: reload embeddings after embed: %v\n", lerr)
				return
			}
			engine.SetVectors(search.BuildFlatIndex(embeddings))
			fmt.Fprintf(os.Stderr, "sense mcp: embeddings complete (%d symbols) — search upgraded to hybrid mode\n", n)
		}()
	} else {
		close(embedDone)
	}

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
