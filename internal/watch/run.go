package watch

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
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
	embedCtl := &embedController{
		enabled: opts.EmbeddingsEnabled,
		ctx:     ctx,
		log:     log,
		checkDebt: func(ctx context.Context) (int, error) {
			return writeAdapter.EmbeddingDebtCount(ctx)
		},
		runEmbed: func(ctx context.Context) (int, error) {
			return scan.EmbedPending(ctx, writeAdapter, root, senseDir)
		},
	}
	defer embedCtl.Cancel()

	// Mark watch start for status reporting
	ws.Set(true, time.Now().UTC())
	defer ws.Set(false, time.Time{})

	embedCtl.Start()

	log("sense: watching for changes (debounce=%dms)", debounceMs)

	pOpts := processOptions{
		root:           root,
		matcher:        matcher,
		maxFileSizeKB:  cfg.Scan.MaxFileSizeKB,
		parsers:        parsers,
		idx:            writeAdapter,
		log:            log,
		runIncremental: scan.RunIncremental,
		cancelEmbed:    embedCtl.Cancel,
		startEmbed:     embedCtl.Start,
	}

	for {
		select {
		case <-ctx.Done():
			log("sense: shutting down")
			return nil
		case batch, ok := <-batches:
			if !ok {
				return nil
			}
			_ = processBatch(ctx, batch, pOpts)
		}
	}
}

// processOptions holds the dependencies for processing a single batch.
type processOptions struct {
	root           string
	matcher        *ignore.Matcher
	maxFileSizeKB  int
	parsers        *scan.ParserCache
	idx            *sqlite.Adapter
	log            func(format string, args ...any)
	runIncremental func(ctx context.Context, opts scan.IncrementalOptions) (*scan.Result, error)
	cancelEmbed    func()
	startEmbed     func()
}

// processBatch handles a single batch of file changes: cancels any running
// embed, runs incremental scan, logs the result, and restarts embed.
func processBatch(ctx context.Context, batch Batch, opts processOptions) error {
	total := len(batch.Changed) + len(batch.Removed)
	if total == 0 {
		return nil
	}
	opts.cancelEmbed()
	res, err := opts.runIncremental(ctx, scan.IncrementalOptions{
		Root:          opts.root,
		Idx:           opts.idx,
		Matcher:       opts.matcher,
		MaxFileSizeKB: opts.maxFileSizeKB,
		Output:        io.Discard,
		Warnings:      os.Stderr,
		Changed:       batch.Changed,
		Removed:       batch.Removed,
		Parsers:       opts.parsers,
	})
	if err != nil {
		opts.log("sense: re-index error: %v", err)
		return err
	}
	opts.log("sense: re-indexed %d files (%d changed, %d removed, %d symbols) in %s",
		total, res.Changed, res.Removed, res.Symbols, res.Duration)
	opts.startEmbed()
	return nil
}

// embedController manages the lifecycle of a background embed goroutine.
type embedController struct {
	enabled   bool
	ctx       context.Context
	log       func(format string, args ...any)
	checkDebt func(ctx context.Context) (int, error)
	runEmbed  func(ctx context.Context) (int, error)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// Start begins a background embed if embeddings are enabled, the parent
// context is still valid, and there is embedding debt to clear.
func (ec *embedController) Start() {
	if !ec.enabled || ec.ctx.Err() != nil {
		return
	}
	debt, derr := ec.checkDebt(ec.ctx)
	if derr != nil {
		ec.log("sense: check embedding debt: %v", derr)
		return
	}
	if debt == 0 {
		return
	}

	ec.mu.Lock()
	defer ec.mu.Unlock()

	// Don't double-start.
	if ec.cancel != nil {
		return
	}

	var embedCtx context.Context
	embedCtx, ec.cancel = context.WithCancel(ec.ctx)
	ec.done = make(chan struct{})
	done := ec.done
	go func() {
		defer close(done)
		n, err := ec.runEmbed(embedCtx)
		if err != nil {
			if embedCtx.Err() == nil {
				ec.log("sense: background embed error: %v", err)
			}
			return
		}
		if n > 0 {
			ec.log("sense: background embed complete (%d symbols)", n)
		}
	}()
}

// Cancel stops the background embed goroutine and waits for it to exit.
// It is safe to call multiple times (idempotent).
func (ec *embedController) Cancel() {
	ec.mu.Lock()
	cancel := ec.cancel
	done := ec.done
	ec.cancel = nil
	ec.done = nil
	ec.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
}

