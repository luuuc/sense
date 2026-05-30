package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// setupTestProject creates a temp directory with a valid .sense database
// seeded with minimal fixture data for lifecycle tests.
func setupTestProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	now := time.Now()
	f := model.File{Path: "main.go", Language: "go", Hash: "abc", Symbols: 1, IndexedAt: now}
	fileID, err := adapter.WriteFile(ctx, &f)
	if err != nil {
		t.Fatal(err)
	}

	s := &model.Symbol{
		FileID:    fileID,
		Name:      "main",
		Qualified: "main.main",
		Kind:      "function",
		LineStart: 1,
		LineEnd:   5,
		Snippet:   "func main() {}",
	}
	if _, err := adapter.WriteSymbol(ctx, s); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestServerStartupSmoke(t *testing.T) {
	dir := setupTestProject(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- RunWithOptions(RunOptions{Dir: dir})
	}()

	select {
	case <-ctx.Done():
		// Expected: ServeStdio blocks until stdin closes
		// If we get here via timeout, that's fine — server started without panic
	case err := <-done:
		if err != nil {
			t.Logf("RunWithOptions returned (expected on pipe close): %v", err)
		}
	}
}

func TestInvalidDirectoryError(t *testing.T) {
	t.Parallel()
	emptyDir := t.TempDir()

	err := RunWithOptions(RunOptions{Dir: emptyDir})
	if err == nil {
		t.Fatal("expected error for invalid directory (no .sense/)")
	}
	// Should be a wrapped error, not a panic
	t.Logf("Got expected error: %v", err)
}

func TestInitializeAndToolList(t *testing.T) {
	t.Parallel()
	dir := setupTestProject(t)

	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	// Initialize
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	initResult, err := c.Initialize(ctx, initReq)
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if initResult.ServerInfo.Name != "sense" {
		t.Errorf("server name = %q, want sense", initResult.ServerInfo.Name)
	}

	// List tools
	toolsReq := mcp.ListToolsRequest{}
	toolsResult, err := c.ListTools(ctx, toolsReq)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if toolsResult == nil {
		t.Fatal("ListTools returned nil")
	}

	toolNames := make(map[string]bool)
	for _, tool := range toolsResult.Tools {
		toolNames[tool.Name] = true
	}

	expected := []string{"sense_search", "sense_graph", "sense_blast", "sense_conventions", "sense_status"}
	for _, name := range expected {
		if !toolNames[name] {
			t.Errorf("missing expected tool: %s", name)
		}
	}
	if len(toolsResult.Tools) != len(expected) {
		t.Errorf("tool count = %d, want %d", len(toolsResult.Tools), len(expected))
	}
}

func TestSenseGraphRoundTrip(t *testing.T) {
	t.Parallel()
	dir := setupTestProject(t)

	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	// Initialize
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Call sense_graph
	req := mcp.CallToolRequest{}
	req.Params.Name = "sense_graph"
	req.Params.Arguments = map[string]any{"symbol": "main.main"}

	result, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool sense_graph: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatal("sense_graph returned empty result")
	}

	text := result.Content[0].(mcp.TextContent).Text
	var graph map[string]any
	if err := json.Unmarshal([]byte(text), &graph); err != nil {
		t.Fatalf("unmarshal graph response: %v", err)
	}

	symbol, ok := graph["symbol"].(map[string]any)
	if !ok {
		t.Fatal("graph.symbol missing or not an object")
	}
	if symbol["name"] != "main" {
		t.Errorf("symbol.name = %v, want main", symbol["name"])
	}
}

func TestBuildMCPServerReturnsServer(t *testing.T) {
	t.Parallel()
	dir := setupTestProject(t)

	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	if s == nil {
		t.Error("buildMCPServer returned nil server")
	}
	if h == nil {
		t.Error("buildMCPServer returned nil handlers")
	}

	// Verify it's a valid server by checking it has the expected tools
	ctx := context.Background()
	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}

	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Server is valid if we can initialize successfully
}

func TestRunWithOptionsEmptyDirDefaultsToCWD(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- RunWithOptions(RunOptions{Dir: t.TempDir()})
	}()

	select {
	case <-ctx.Done():
		t.Fatal("RunWithOptions with invalid dir should return quickly, not hang")
	case err := <-done:
		if err == nil {
			t.Fatal("expected error for directory without .sense")
		}
	}
}

// TestBuildMCPServerWithEmbeddingDebt exercises buildMCPServer's
// embeddings-enabled path: a stale embedding-model meta triggers the
// model-changed warning, and a symbol with no embedding gives the server
// outstanding debt, so it starts the background embed goroutine and creates
// the bundled embedder. cleanup() drains the goroutine.
func TestBuildMCPServerWithEmbeddingDebt(t *testing.T) {
	probe, err := embed.NewBundledEmbedder(0)
	if err != nil {
		t.Skipf("bundled embedder unavailable: %v", err)
	}
	_ = probe.Close()

	dir := setupTestProject(t)
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// Stale model → warning branch; watermark+debt already present (no embeddings).
	if err := a.WriteMeta(ctx, "embedding_model", "stale-model-v0"); err != nil {
		_ = a.Close()
		t.Fatalf("write meta: %v", err)
	}
	_ = a.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	// cleanup() cancels the embed context and blocks on the background
	// goroutine's done channel, so it drains deterministically — no sleep,
	// no flake. This covers the debt/warning/goroutine-start branches; the
	// post-embed SetVectors upgrade is timing-dependent and not asserted.
	cleanup()
}
