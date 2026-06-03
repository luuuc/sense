// Package watch is the headless CLI entry point for `sense scan --watch`:
// it runs an initial full scan, then hands ongoing freshening to a
// freshen.Service (the same engine the `sense mcp` server hosts). It owns
// process lifecycle — signal handling and, optionally, a co-hosted MCP
// server — while the Service owns watching, re-index, and embedding.
package watch

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/mcpserver"
	"github.com/luuuc/sense/internal/scan"
)

// RunOptions configures the watch mode.
type RunOptions struct {
	Root              string
	EmbeddingsEnabled bool
	MCP               bool // co-host an MCP server alongside the watcher
}

// Run performs an initial full scan, then starts a freshen.Service to keep
// the index current. When MCP is set it also co-hosts an MCP server,
// sharing the watch state so the server reports watching status and skips
// starting its own watcher. Blocks until SIGINT/SIGTERM or ctx cancel.
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

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Shared watch state — reported by a co-hosted MCP server and the
	// signal the server uses to defer watching to this process.
	ws := &mcpio.WatchState{}

	svc, err := freshen.NewService(freshen.Config{
		Root:              root,
		EmbeddingsEnabled: opts.EmbeddingsEnabled,
		WatchState:        ws,
		Log:               log,
	})
	if err != nil {
		return err
	}
	if err := svc.Start(ctx); err != nil {
		return err
	}
	defer svc.Stop()

	// Co-host the MCP server if requested. It receives the shared watch
	// state, so its WatchState != nil guard makes it skip its own watcher.
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

	<-ctx.Done()
	log("sense: shutting down")
	return nil
}
