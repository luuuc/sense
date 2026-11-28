package mcpserver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/scan/scantest"
	"github.com/luuuc/sense/internal/sqlite"
)

// An agent can start `sense mcp` before the first scan has written the index
// (a fresh worktree whose provisioning is still running). The server must
// still come up, say the index is being built, and serve real answers as soon
// as a scan finishes, without a restart.
func TestServerStartsBeforeFirstScanAndPicksUpIndex(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n\nfunc main() { Hello() }\n"})

	s, cleanup, err := startServer(RunOptions{Dir: repo.Root}, defaultDeps())
	if err != nil {
		t.Fatalf("startServer without an index: %v", err)
	}
	defer cleanup()
	c := connect(t, s)

	tools, err := c.ListTools(context.Background(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools.Tools) != 5 {
		t.Errorf("tools = %d, want all 5 listed while the index is built", len(tools.Tools))
	}

	res := call(t, c, "sense_graph", map[string]any{"symbol": "Hello"})
	if !res.IsError || !strings.Contains(resultText(t, res), "being built") {
		t.Fatalf("before the scan: IsError=%v text=%q, want a 'being built' error", res.IsError, resultText(t, res))
	}

	repo.Scan(scan.Options{})

	res = call(t, c, "sense_graph", map[string]any{"symbol": "Hello"})
	if res.IsError || !strings.Contains(resultText(t, res), "Hello") {
		t.Fatalf("after the scan: IsError=%v text=%q, want Hello's graph", res.IsError, resultText(t, res))
	}
}

// A scan creates index.db when it starts, long before it finishes. The server
// must not serve that half-built index: it waits for the stamp a scan writes
// only once it completes.
func TestServerWaitsForFirstScanToFinish(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n"})

	s, cleanup, err := startServer(RunOptions{Dir: repo.Root}, defaultDeps())
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	defer cleanup()
	c := connect(t, s)

	// What a scan in progress leaves on disk: the file, schema applied, no stamp.
	mkdirIndex(t, repo.Root)
	a, err := sqlite.Open(context.Background(), filepath.Join(repo.Root, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	res := call(t, c, "sense_status", nil)
	if !res.IsError || !strings.Contains(resultText(t, res), "being built") {
		t.Errorf("mid-scan: IsError=%v text=%q, want a 'being built' error", res.IsError, resultText(t, res))
	}
}

// If the index is complete but the server still can't open it, the tool says
// why instead of claiming the index is being built.
func TestServerReportsIndexOpenFailureAfterScan(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n"})

	base := defaultDeps()
	d := base
	opens := 0
	d.openIndex = func(ctx context.Context, dir string) (*sqlite.Adapter, error) {
		opens++
		if opens == 1 {
			return base.openIndex(ctx, dir) // startup: the real open reports the missing index
		}
		return nil, errors.New("open boom")
	}

	s, cleanup, err := startServer(RunOptions{Dir: repo.Root}, d)
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	defer cleanup()
	c := connect(t, s)

	repo.Scan(scan.Options{})
	res := call(t, c, "sense_status", nil)
	if !res.IsError || !strings.Contains(resultText(t, res), "open boom") {
		t.Errorf("IsError=%v text=%q, want the open error", res.IsError, resultText(t, res))
	}
}

// The index is complete and opens, but building the handlers on it fails: the
// tool reports that error, and a later call can still succeed.
func TestServerReportsBuildFailureAfterScan(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n"})

	base := defaultDeps()
	d := base
	opens := 0
	d.openIndex = func(ctx context.Context, dir string) (*sqlite.Adapter, error) {
		opens++
		if opens == 3 { // startup, readiness probe, then the build itself
			return nil, errors.New("build boom")
		}
		return base.openIndex(ctx, dir)
	}

	s, cleanup, err := startServer(RunOptions{Dir: repo.Root}, d)
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	defer cleanup()
	c := connect(t, s)

	repo.Scan(scan.Options{})
	res := call(t, c, "sense_graph", map[string]any{"symbol": "Hello"})
	if !res.IsError || !strings.Contains(resultText(t, res), "build boom") {
		t.Fatalf("IsError=%v text=%q, want the build error", res.IsError, resultText(t, res))
	}
	for i := 0; i < 2; i++ { // the retry builds the handlers; the next call reuses them
		res = call(t, c, "sense_graph", map[string]any{"symbol": "Hello"})
		if res.IsError {
			t.Errorf("call %d after a failed build: %q", i+1, resultText(t, res))
		}
	}
	if opens != 5 {
		t.Errorf("index opened %d times, want 5 (startup, probe, failed build, retry probe, retry build; the last call reuses)", opens)
	}
}

// Without --dir the server resolves the working directory once; a failure
// there is a startup error.
func TestStartServerGetwdFault(t *testing.T) {
	d := defaultDeps()
	d.getwd = func() (string, error) { return "", errors.New("boom getwd") }
	if _, _, err := startServer(RunOptions{}, d); err == nil || !strings.Contains(err.Error(), "getwd") {
		t.Errorf("startServer = %v, want the getwd error", err)
	}
}

// A --dir that doesn't exist is a mistake, not a scan in progress: fail fast.
func TestStartServerRejectsMissingDir(t *testing.T) {
	_, _, err := startServer(RunOptions{Dir: filepath.Join(t.TempDir(), "nope")}, defaultDeps())
	if err == nil {
		t.Fatal("startServer on a missing dir: want an error")
	}
}

// With the index already there, startup is unchanged: the full server, with
// errors other than a missing index still failing at start.
func TestStartServerWithIndexBuildsDirectly(t *testing.T) {
	hermeticEnv(t)
	repo := scantest.NewRepo(t, map[string]string{"main.go": "package main\n\nfunc Hello() {}\n"})
	repo.Scan(scan.Options{})

	s, cleanup, err := startServer(RunOptions{Dir: repo.Root}, defaultDeps())
	if err != nil {
		t.Fatalf("startServer: %v", err)
	}
	defer cleanup()
	res := call(t, connect(t, s), "sense_graph", map[string]any{"symbol": "Hello"})
	if res.IsError {
		t.Errorf("sense_graph: %q", resultText(t, res))
	}

	d := defaultDeps()
	d.openIndex = func(context.Context, string) (*sqlite.Adapter, error) { return nil, errors.New("corrupt boom") }
	if _, _, err := startServer(RunOptions{Dir: repo.Root}, d); err == nil || !strings.Contains(err.Error(), "corrupt boom") {
		t.Errorf("startServer with a failing open = %v, want the open error", err)
	}
}

func connect(t *testing.T, s *server.MCPServer) *client.Client {
	t.Helper()
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	init := mcp.InitializeRequest{}
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, init); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c
}

func call(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return res
}

func mkdirIndex(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
}
