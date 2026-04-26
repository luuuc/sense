package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/mcpserver"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// RunOptions configures the watch mode.
type RunOptions struct {
	Root              string
	EmbeddingsEnabled bool
	MCP               bool // start MCP server concurrently
}

// Run starts watch mode: performs an initial scan, starts the file
// watcher and debounce loop, and optionally runs the MCP server
// concurrently. Blocks until SIGINT/SIGTERM or context cancellation.
func Run(ctx context.Context, opts RunOptions) error {
	root := opts.Root
	if root == "" {
		root = "."
	}
	root, _ = filepath.Abs(root)

	log := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, time.Now().UTC().Format("15:04:05")+" "+format+"\n", args...)
	}

	log("sense: watch mode starting (root=%s)", root)

	// Initial full scan
	log("sense: initial scan...")
	initialRes, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            os.Stderr,
		Warnings:          os.Stderr,
		EmbeddingsEnabled: opts.EmbeddingsEnabled,
	})
	if err != nil {
		return fmt.Errorf("initial scan: %w", err)
	}
	log("sense: initial scan complete (%d files, %d symbols in %s)", initialRes.Files, initialRes.Symbols, initialRes.Duration)

	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	matcher, err := ignore.Build(root, cfg.Ignore)
	if err != nil {
		return fmt.Errorf("build ignore matcher: %w", err)
	}

	// Open adapter for incremental writes
	senseDir := filepath.Join(root, ".sense")
	dbPath := filepath.Join(senseDir, "index.db")
	writeAdapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("open index for watch: %w", err)
	}
	defer func() { _ = writeAdapter.Close() }()

	// Set up signal handling for graceful shutdown
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Shared watch state — injected into the MCP server so it can
	// report watching status without a package-level global.
	ws := &mcpio.WatchState{}

	// Start MCP server concurrently if requested
	if opts.MCP {
		go func() {
			if err := mcpserver.RunWithOptions(mcpserver.RunOptions{
				Dir:        root,
				WatchState: ws,
			}); err != nil {
				log("sense: mcp server error: %v", err)
				cancel()
			}
		}()
	}

	// Start file watcher
	w, err := New(root, matcher)
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer func() { _ = w.Close() }()

	debounceMs := cfg.Scan.WatchDebounceMs
	if debounceMs <= 0 {
		debounceMs = DefaultDebounceMs
	}

	batches := Loop(ctx, w, debounceMs)

	// Parser cache persists across batches to avoid re-allocating
	// tree-sitter parsers on every file change event.
	parsers := scan.NewParserCache()
	defer parsers.Close()

	// Background embed management: the watch loop owns all embedding.
	// The MCP server skips its own background embed when WatchState is set.
	var embedCancel context.CancelFunc
	var embedDone chan struct{}

	startEmbed := func() {
		if !opts.EmbeddingsEnabled || ctx.Err() != nil {
			return
		}
		debt, derr := writeAdapter.EmbeddingDebtCount(ctx)
		if derr != nil {
			log("sense: check embedding debt: %v", derr)
			return
		}
		if debt == 0 {
			return
		}
		var embedCtx context.Context
		embedCtx, embedCancel = context.WithCancel(ctx)
		embedDone = make(chan struct{})
		go func() {
			defer close(embedDone)
			n, err := scan.EmbedPending(embedCtx, writeAdapter, root, senseDir)
			if err != nil {
				if embedCtx.Err() == nil {
					log("sense: background embed error: %v", err)
				}
				return
			}
			if n > 0 {
				log("sense: background embed complete (%d symbols)", n)
			}
		}()
	}

	cancelEmbed := func() {
		if embedCancel != nil {
			embedCancel()
			<-embedDone
			embedCancel = nil
			embedDone = nil
		}
	}
	defer cancelEmbed()

	// Mark watch start for status reporting
	ws.Set(true, time.Now().UTC())
	defer ws.Set(false, time.Time{})

	startEmbed()

	log("sense: watching for changes (debounce=%dms)", debounceMs)

	for {
		select {
		case <-ctx.Done():
			log("sense: shutting down")
			return nil
		case batch, ok := <-batches:
			if !ok {
				return nil
			}
			total := len(batch.Changed) + len(batch.Removed)
			if total == 0 {
				continue
			}
			cancelEmbed()
			res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
				Root:          root,
				Idx:           writeAdapter,
				Matcher:       matcher,
				MaxFileSizeKB: cfg.Scan.MaxFileSizeKB,
				Output:        io.Discard,
				Warnings:      os.Stderr,
				Changed:       batch.Changed,
				Removed:       batch.Removed,
				Parsers:       parsers,
			})
			if err != nil {
				log("sense: re-index error: %v", err)
				continue
			}
			log("sense: re-indexed %d files (%d changed, %d removed, %d symbols) in %s",
				total, res.Changed, res.Removed, res.Symbols, res.Duration)
			startEmbed()
		}
	}
}


