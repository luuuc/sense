package freshen

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

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
	// onEmbedded, if set, is called after a successful embed that wrote
	// n>0 new embeddings. It lets a co-hosted reader (the MCP search
	// engine) refresh its in-memory vectors off the query path. It runs
	// on the embed context, so a subsequent batch cancels it harmlessly.
	onEmbedded func(ctx context.Context, n int)

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
			if ec.onEmbedded != nil {
				ec.onEmbedded(embedCtx, n)
			}
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
