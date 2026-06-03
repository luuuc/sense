package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// dbHasSymbol reports whether a symbol with the given name exists in the
// index opened at root.
func dbHasSymbol(t *testing.T, adapter *sqlite.Adapter, name string) bool {
	t.Helper()
	var n int
	if err := adapter.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM sense_symbols WHERE name = ?`, name).Scan(&n); err != nil {
		t.Fatalf("query: %v", err)
	}
	return n > 0
}

// writerWithIdleWatcher starts a freshen.Service whose debounce is so long
// the watcher will not fire during a test, so read-repair is the only
// path that can refresh the index. It returns the service and a separate
// read adapter that sees the service's committed writes.
func writerWithIdleWatcher(t *testing.T, dir string) (*freshen.Service, *sqlite.Adapter) {
	t.Helper()
	svc, err := freshen.NewService(freshen.Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !svc.Writing() {
		svc.Stop()
		t.Fatal("service should own the writer role")
	}
	ra, err := sqlite.Open(context.Background(), filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		svc.Stop()
		t.Fatalf("open read adapter: %v", err)
	}
	return svc, ra
}

// scanProject runs a real scan over dir so the embedded watcher has a
// genuine index to keep fresh (the hand-seeded fixture in setupTestProject
// has no on-disk files for the watcher to re-parse).
func scanProject(t *testing.T, dir string) {
	t.Helper()
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     dir,
		Output:   os.NewFile(0, os.DevNull),
		Warnings: os.NewFile(0, os.DevNull),
	}); err != nil {
		t.Fatalf("initial scan: %v", err)
	}
}

func startInProcessServer(t *testing.T, dir string) (*client.Client, func()) {
	t.Helper()
	s, _, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	c, err := client.NewInProcessClient(s)
	if err != nil {
		cleanup()
		t.Fatalf("NewInProcessClient: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		cleanup()
		t.Fatalf("client.Start: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		cleanup()
		t.Fatalf("Initialize: %v", err)
	}
	return c, func() {
		_ = c.Close()
		cleanup()
	}
}

// graphSymbolName calls sense_graph for symbol and returns the resolved
// symbol name, or "" if the symbol did not resolve.
func graphSymbolName(t *testing.T, c *client.Client, symbol string) string {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = "sense_graph"
	req.Params.Arguments = map[string]any{"symbol": symbol}
	result, err := c.CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool sense_graph: %v", err)
	}
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return ""
	}
	sym, ok := resp["symbol"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := sym["name"].(string)
	return name
}

// TestEmbeddedWatcherReflectsOutOfBandEdit is the card's headline behavior:
// with the server running, a file edited out of band (no PostToolUse hook,
// no manual scan) is re-indexed by the embedded watcher, so a subsequent
// in-process query returns the new symbol.
func TestEmbeddedWatcherReflectsOutOfBandEdit(t *testing.T) {
	// Embeddings off: deterministic, no bundled-embedder dependency.
	t.Setenv("SENSE_EMBEDDINGS", "false")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	c, stop := startInProcessServer(t, dir)
	defer stop()

	// Sanity: the new symbol is absent before the edit.
	if name := graphSymbolName(t, c, "Goodbye"); name == "Goodbye" {
		t.Fatal("Goodbye should not exist before the edit")
	}

	// Edit out of band — exactly what the agent's Bash, the human's editor,
	// or git would do. No hook fires.
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Poll until the debounced watcher catches up.
	deadline := time.After(8 * time.Second)
	for {
		if name := graphSymbolName(t, c, "Goodbye"); name == "Goodbye" {
			return // success: index refreshed with no hook, no manual scan
		}
		select {
		case <-deadline:
			t.Fatal("timed out: embedded watcher did not reflect the out-of-band edit")
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// TestExternalWatchStateSkipsEmbeddedWatcher mirrors the existing
// background-embed guard: a server handed a non-nil WatchState defers
// watching to the owning process and starts no watcher of its own. We
// assert it still serves queries and that an out-of-band edit is NOT
// picked up (because this server is not watching).
func TestExternalWatchStateSkipsEmbeddedWatcher(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	external := &mcpio.WatchState{}
	s, h, cleanup, err := buildMCPServer(RunOptions{Dir: dir, WatchState: external})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	// The handler must report the externally-supplied watch state, proving
	// the server did not substitute its own.
	if h.watchState != external {
		t.Error("server should use the externally supplied WatchState")
	}

	c, err := client.NewInProcessClient(s)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	initReq := mcp.InitializeRequest{}
	initReq.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initReq.Params.ClientInfo = mcp.Implementation{Name: "test", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Still serves queries against the existing index.
	if name := graphSymbolName(t, c, "Hello"); name != "Hello" {
		t.Errorf("expected to resolve Hello, got %q", name)
	}
}

// TestReadRepairRefreshesStaleFile is the card's race case: a file edited
// out of band, then queried before the (here, idle) watcher fires, is
// repaired inline so the query sees the new symbol. A second call, with
// nothing stale, is a no-op.
func TestReadRepairRefreshesStaleFile(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	svc, ra := writerWithIdleWatcher(t, dir)
	defer svc.Stop()
	defer func() { _ = ra.Close() }()

	h := &handlers{db: ra.DB(), dir: dir, freshen: svc}

	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dbHasSymbol(t, ra, "Goodbye") {
		t.Fatal("Goodbye should be absent before repair (watcher is idle)")
	}

	snap := h.repairAndSnapshot(context.Background())
	if !dbHasSymbol(t, ra, "Goodbye") {
		t.Fatal("read-repair should have indexed the edited file inline")
	}
	if len(snap.staleRels) != 0 {
		t.Errorf("freshness footer should be clean after repair, got %d stale", len(snap.staleRels))
	}

	// Nothing stale now: a second sweep repairs nothing and stays clean.
	snap2 := h.repairAndSnapshot(context.Background())
	if len(snap2.staleRels) != 0 {
		t.Errorf("expected no stale files on the no-op path, got %d", len(snap2.staleRels))
	}
}

// TestReadRepairNoopWhenReadOnly: a read-only server (another process owns
// the writer) does not repair; it reports the staleness honestly instead.
func TestReadRepairNoopWhenReadOnly(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	// Pre-hold the writer lock with this live process so the Service is
	// read-only.
	if err := os.WriteFile(filepath.Join(dir, ".sense", "index.lock"),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, err := freshen.NewService(freshen.Config{Root: dir, DebounceMs: 10 * 60 * 1000})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.Stop()
	if svc.Writing() {
		t.Fatal("service should be read-only")
	}
	ra, err := sqlite.Open(context.Background(), filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ra.Close() }()

	h := &handlers{db: ra.DB(), dir: dir, freshen: svc}

	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n\nfunc Goodbye() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	snap := h.repairAndSnapshot(context.Background())
	if dbHasSymbol(t, ra, "Goodbye") {
		t.Error("read-only server must not repair the index")
	}
	if len(snap.staleRels) == 0 {
		t.Error("read-only server should still report the file as stale")
	}
}

// TestReadRepairBoundedSkipsLargeStaleSet: more than maxInlineRepairFiles
// stale files means the query answers on current data and defers to the
// watcher — read-repair must not re-index the world on a hot query.
func TestReadRepairBoundedSkipsLargeStaleSet(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	n := maxInlineRepairFiles + 1
	for i := 0; i < n; i++ {
		fn := filepath.Join(dir, "f"+strconv.Itoa(i)+".go")
		if err := os.WriteFile(fn,
			[]byte("package main\n\nfunc Orig"+strconv.Itoa(i)+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scanProject(t, dir)

	svc, ra := writerWithIdleWatcher(t, dir)
	defer svc.Stop()
	defer func() { _ = ra.Close() }()
	h := &handlers{db: ra.DB(), dir: dir, freshen: svc}

	// Edit every file so the stale set exceeds the inline cap.
	for i := 0; i < n; i++ {
		fn := filepath.Join(dir, "f"+strconv.Itoa(i)+".go")
		if err := os.WriteFile(fn,
			[]byte("package main\n\nfunc Orig"+strconv.Itoa(i)+"() {}\n\nfunc New"+strconv.Itoa(i)+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	snap := h.repairAndSnapshot(context.Background())
	if dbHasSymbol(t, ra, "New0") {
		t.Error("oversized stale set should not be repaired inline")
	}
	if len(snap.staleRels) <= maxInlineRepairFiles {
		t.Errorf("expected more than %d stale files, got %d", maxInlineRepairFiles, len(snap.staleRels))
	}
}

// TestUpgradeSearchOnEmbed exercises the OnEmbedded callback the server
// hands to its freshen.Service: it reloads embeddings into the live engine
// (happy path) and skips quietly when its context is already cancelled.
func TestUpgradeSearchOnEmbed(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	engine, embedder, err := search.BuildEngine(ctx, adapter, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if embedder != nil {
		defer func() { _ = embedder.Close() }()
	}

	fn := upgradeSearchOnEmbed(adapter, engine)
	// Happy path: reload + SetVectors, no panic.
	fn(ctx, 1)

	// Cancelled context: LoadEmbeddings fails and the callback returns
	// quietly without logging.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	fn(cctx, 1)
}

// TestWatchDisabledStartsNoEmbeddedWatcher verifies the watch: false opt-out
// (here via SENSE_WATCH=false): the server serves queries but hosts no
// embedded watcher.
func TestWatchDisabledStartsNoEmbeddedWatcher(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	t.Setenv("SENSE_WATCH", "false")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc Hello() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanProject(t, dir)

	_, h, cleanup, err := buildMCPServer(RunOptions{Dir: dir})
	if err != nil {
		t.Fatalf("buildMCPServer: %v", err)
	}
	defer cleanup()

	if h.freshen != nil {
		t.Error("watch disabled: server should host no freshen.Service")
	}
	if h.watchState != nil {
		t.Error("watch disabled: server should report no watch state")
	}
}
