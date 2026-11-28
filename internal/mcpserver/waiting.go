package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/luuuc/sense/internal/cli"
)

// indexBuildingMessage is what every tool answers until the first scan
// finishes. It tells the agent the tools are coming, not gone.
const indexBuildingMessage = "The Sense index for this repo is still being built: the first `sense scan` has not finished. " +
	"Use grep and file reads for now and call this tool again in a minute. " +
	"If no scan is running, run `sense scan` in the project root."

// startServer builds the server `sense mcp` serves. With the index in place
// it is the full server. When the index does not exist yet (an agent started
// before the first scan, as in a freshly provisioned worktree), it starts a
// waiting server instead of exiting: an MCP client does not restart a server
// that died, so exiting would lose Sense for the whole session.
func startServer(opts RunOptions, d deps) (*server.MCPServer, func(), error) {
	dir, err := resolveDir(opts, d)
	if err != nil {
		return nil, nil, err
	}
	opts.Dir = dir
	s, _, cleanup, err := buildMCPServerWithDeps(opts, d)
	if !errors.Is(err, cli.ErrIndexMissing) {
		return s, cleanup, err
	}
	if _, serr := os.Stat(dir); serr != nil {
		return nil, nil, err
	}
	w := &waitingIndex{opts: opts, d: d, dir: dir}
	return w.server(), w.close, nil
}

// waitingIndex serves the tools before the index exists. Each call checks
// whether a scan has finished; the first one that finds a complete index
// builds the real handlers, and every later call goes straight to them.
type waitingIndex struct {
	opts RunOptions
	d    deps
	dir  string

	mu      sync.Mutex
	h       *handlers
	cleanup func()
}

func (w *waitingIndex) server() *server.MCPServer {
	s := newMCPServer()
	for _, e := range toolTable() {
		s.AddTool(e.tool, withAliasing(e.name, w.bind(e.handle)))
	}
	return s
}

// bind returns a tool handler that answers "being built" until the index is
// ready, then delegates to the real handler.
func (w *waitingIndex) bind(handle func(*handlers, context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		h, err := w.handlers(ctx)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		if h == nil {
			return mcp.NewToolResultError(indexBuildingMessage), nil
		}
		return handle(h, ctx, req)
	}
}

// handlers returns the real handlers once a scan has completed the index, or
// nil while it is still being built.
func (w *waitingIndex) handlers(ctx context.Context) (*handlers, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.h != nil {
		return w.h, nil
	}
	ready, err := w.scanFinished(ctx)
	if err != nil || !ready {
		return nil, err
	}
	h, cleanup, err := buildHandlers(w.opts, w.d)
	if err != nil {
		return nil, err
	}
	w.h, w.cleanup = h, cleanup
	return h, nil
}

// scanFinished reports whether a scan has completed the index. A scan creates
// index.db as it starts, so the file existing is not enough: the schema stamp
// is written only once a scan finishes.
func (w *waitingIndex) scanFinished(ctx context.Context) (bool, error) {
	if _, err := os.Stat(filepath.Join(w.dir, ".sense", "index.db")); err != nil {
		return false, nil
	}
	a, err := w.d.openIndex(ctx, w.dir)
	if err != nil {
		return false, fmt.Errorf("sense mcp: %w", err)
	}
	defer func() { _ = a.Close() }()
	return a.ScanComplete(ctx), nil
}

func (w *waitingIndex) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cleanup != nil {
		w.cleanup()
	}
}
