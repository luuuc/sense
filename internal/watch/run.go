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

// serviceRunner is the part of *freshen.Service that Run drives: start the
// background freshening, stop it on shutdown. It exists only so a test can
// substitute a fake and exercise process lifecycle (the co-host goroutine,
// the start-error return, clean shutdown) without a live index or watcher.
// Unexported: Run's public signature is unchanged and no test-only seam
// leaks into the package API.
type serviceRunner interface {
	Start(context.Context) error
	Stop()
}

// deps are Run's three live collaborators, injected so the lifecycle logic
// is reachable without spinning up the real subsystems. Production wires the
// real functions via defaultDeps; a test substitutes fakes. The struct is
// unexported and defaulted inside Run, so the public Run(ctx, RunOptions)
// signature carries no seam.
type deps struct {
	scan       func(context.Context, scan.Options) (*scan.Result, error)
	newService func(freshen.Config) (serviceRunner, error)
	runServer  func(mcpserver.RunOptions) error
}

// defaultDeps wires Run's collaborators to the real implementations.
func defaultDeps() deps {
	return deps{
		scan: scan.Run,
		newService: func(cfg freshen.Config) (serviceRunner, error) {
			// NewService returns a nil *Service only alongside a non-nil
			// error, and run checks the error first, so this never hands back
			// a non-nil interface wrapping a typed-nil pointer.
			return freshen.NewService(cfg)
		},
		runServer: mcpserver.RunWithOptions,
	}
}

// Run performs an initial full scan, then starts a freshen.Service to keep
// the index current. When MCP is set it also co-hosts an MCP server,
// sharing the watch state so the server reports watching status and skips
// starting its own watcher. Blocks until SIGINT/SIGTERM or ctx cancel.
func Run(ctx context.Context, opts RunOptions) error {
	return run(ctx, opts, defaultDeps())
}

// run is Run's testable core: identical lifecycle, but every live
// collaborator arrives through d so a test can drive the co-host goroutine,
// the initial-scan-error return, and signal-driven shutdown with fakes.
func run(ctx context.Context, opts RunOptions, d deps) error {
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
	initialRes, err := d.scan(ctx, scan.Options{
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

	svc, err := d.newService(freshen.Config{
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
			if err := d.runServer(mcpserver.RunOptions{
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
