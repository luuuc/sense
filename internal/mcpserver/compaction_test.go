package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupCompactionFixture(t *testing.T) *handlers {
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
	t.Cleanup(func() { _ = adapter.Close() })

	now := time.Now()

	files := []model.File{
		{Path: "internal/service/service.go", Language: "go", Hash: "a1", Symbols: 20, IndexedAt: now},
	}
	fileIDs := make([]int64, len(files))
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[i] = id
	}

	targetID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "Target", Qualified: "service.Target",
		Kind: "function", LineStart: 1, LineEnd: 30,
		Snippet: "func Target() {}",
	})

	// Create 15 callers to test compaction
	for i := 0; i < 15; i++ {
		callerName := fmt.Sprintf("Caller%d", i)
		callerID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fileIDs[0], Name: callerName, Qualified: fmt.Sprintf("service.%s", callerName),
			Kind: "function", LineStart: i + 1, LineEnd: i + 10,
		})
		_, err := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerID), TargetID: targetID, Kind: model.EdgeCalls,
			FileID: fileIDs[0], Line: intPtr(i + 1), Confidence: 1.0,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	tracker := metrics.NewTracker(adapter.DB())
	t.Cleanup(func() { tracker.Close() })

	return &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
}

func TestCompactCallEdges(t *testing.T) {
	edges := make([]mcpio.CallEdgeRef, 15)
	for i := range edges {
		edges[i] = mcpio.CallEdgeRef{
			Symbol:    fmt.Sprintf("Caller%d", i),
			LineStart: i + 1,
			LineEnd:   i + 5,
		}
	}

	compactCallEdges(edges)

	for i := 0; i < 10; i++ {
		if edges[i].LineStart == 0 {
			t.Errorf("entry %d: expected line_start preserved, got 0", i)
		}
		if edges[i].LineEnd == 0 {
			t.Errorf("entry %d: expected line_end preserved, got 0", i)
		}
	}
	for i := 10; i < 15; i++ {
		if edges[i].LineStart != 0 {
			t.Errorf("entry %d: expected line_start stripped, got %d", i, edges[i].LineStart)
		}
		if edges[i].LineEnd != 0 {
			t.Errorf("entry %d: expected line_end stripped, got %d", i, edges[i].LineEnd)
		}
	}
}

func TestCompactDispatchInferred(t *testing.T) {
	edges := make([]mcpio.DispatchInferredRef, 15)
	for i := range edges {
		edges[i] = mcpio.DispatchInferredRef{
			Symbol:    fmt.Sprintf("Caller%d", i),
			LineStart: i + 1,
			LineEnd:   i + 5,
		}
	}

	compactDispatchInferred(edges)

	for i := 0; i < 10; i++ {
		if edges[i].LineStart == 0 {
			t.Errorf("entry %d: expected line_start preserved, got 0", i)
		}
		if edges[i].LineEnd == 0 {
			t.Errorf("entry %d: expected line_end preserved, got 0", i)
		}
	}
	for i := 10; i < 15; i++ {
		if edges[i].LineStart != 0 {
			t.Errorf("entry %d: expected line_start stripped, got %d", i, edges[i].LineStart)
		}
		if edges[i].LineEnd != 0 {
			t.Errorf("entry %d: expected line_end stripped, got %d", i, edges[i].LineEnd)
		}
	}
}

func TestHandleGraphCompactsEdges(t *testing.T) {
	h := setupCompactionFixture(t)
	ctx := context.Background()

	result, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "service.Target",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Edges.CalledBy) < 15 {
		t.Fatalf("expected at least 15 callers, got %d", len(resp.Edges.CalledBy))
	}

	for i, edge := range resp.Edges.CalledBy {
		if i < 10 && edge.LineStart == 0 {
			t.Errorf("called_by entry %d: expected line_start preserved", i)
		}
		if i >= 10 && edge.LineStart != 0 {
			t.Errorf("called_by entry %d: expected line_start stripped", i)
		}
	}
}

func TestHandleSearchStripsSnippets(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleSearch(ctx, toolReq(map[string]any{
		"query": "auth",
		"limit": 10,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for i, result := range resp.Results {
		if i < 5 && result.Snippet == "" {
			t.Errorf("result %d: expected snippet for top 5, got empty", i)
		}
		if i >= 5 && result.Snippet != "" {
			t.Errorf("result %d: expected no snippet for results 6+, got %q", i, result.Snippet)
		}
	}
}

// TestHandleSearchSourceIsHonest pins the wire-layer contract: the
// handler must emit the engine's real provenance, never the old
// hardcoded "structural" placeholder. Guards against a future refactor
// silently re-hardcoding the field.
func TestHandleSearchSourceIsHonest(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query": "Verify",
		"limit": 10,
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results for seeded query")
	}
	honest := map[string]bool{"keyword": true, "vector": true, "hybrid": true, "graph": true, "text": true}
	for _, r := range resp.Results {
		if r.Source == "structural" {
			t.Errorf("result %s still carries hardcoded 'structural' source", r.Symbol)
		}
		if !honest[r.Source] {
			t.Errorf("result %s has unrecognized source %q", r.Symbol, r.Source)
		}
	}
}

func TestHandleSearchSessionDedup(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	// First search
	result1, err := h.handleSearch(ctx, toolReq(map[string]any{
		"query": "Verify",
		"limit": 10,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result1.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result1))
	}

	var resp1 mcpio.SearchResponse
	if err := json.Unmarshal([]byte(resultText(t, result1)), &resp1); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Second search with overlapping results
	result2, err := h.handleSearch(ctx, toolReq(map[string]any{
		"query": "auth",
		"limit": 10,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result2.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result2))
	}

	var resp2 mcpio.SearchResponse
	if err := json.Unmarshal([]byte(resultText(t, result2)), &resp2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Find symbols that appeared in both searches
	seenSymbols := make(map[string]bool)
	for _, r := range resp1.Results {
		seenSymbols[r.Symbol] = true
	}

	var foundDedup bool
	for _, r := range resp2.Results {
		if seenSymbols[r.Symbol] {
			if !r.Seen {
				t.Errorf("symbol %q appeared in first search but not marked seen", r.Symbol)
			}
			if r.Snippet != "" {
				t.Errorf("symbol %q should have snippet stripped when seen", r.Symbol)
			}
			foundDedup = true
		}
	}

	if !foundDedup {
		t.Skip("no overlapping symbols between searches — dedup not testable with this fixture")
	}
}

func TestHandleGraphTracksSeenSymbols(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result1, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "auth.Verify",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result1.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result1))
	}

	// auth.Verify should now be in seenSymbols
	if !h.seenSymbols[ts.symbols["auth.Verify"]] {
		t.Error("expected auth.Verify to be tracked in seenSymbols after graph call")
	}
}

func TestConventionsKeySymbolsLimit(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleConventions(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.KeySymbols) > 8 {
		t.Errorf("expected at most 8 key symbols, got %d", len(resp.KeySymbols))
	}
}

func TestHandleGraphWithDispatchInferred(t *testing.T) {
	// Setup interface dispatch fixture similar to dispatch_test.go
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
	files := []model.File{
		{Path: "iface.go", Language: "go", Hash: "abc", Symbols: 3, IndexedAt: now},
		{Path: "caller.go", Language: "go", Hash: "def", Symbols: 2, IndexedAt: now},
	}
	fileIDs := make([]int64, len(files))
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[i] = id
	}

	// Interface I with method M
	ifaceID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "I", Qualified: "pkg.I",
		Kind: "interface", LineStart: 1, LineEnd: 10,
	})
	methodID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "M", Qualified: "pkg.I.M",
		Kind: "method", LineStart: 2, LineEnd: 5, ParentID: &ifaceID,
	})

	// Struct S implementing I, with method M
	structID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "S", Qualified: "pkg.S",
		Kind: "class", LineStart: 20, LineEnd: 40,
	})
	structMethodID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "M", Qualified: "pkg.S.M",
		Kind: "method", LineStart: 22, LineEnd: 30, ParentID: &structID,
	})

	// S inherits I
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(structID), TargetID: ifaceID, Kind: model.EdgeInherits,
		FileID: fileIDs[0], Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// F calls S.M (caller of the struct method)
	funcID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[1], Name: "F", Qualified: "pkg.F",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(funcID), TargetID: structMethodID, Kind: model.EdgeCalls,
		FileID: fileIDs[1], Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	// G calls I.M (caller through the interface)
	func2ID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[1], Name: "G", Qualified: "pkg.G",
		Kind: "function", LineStart: 10, LineEnd: 15,
	})
	_, err = adapter.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(func2ID), TargetID: methodID, Kind: model.EdgeCalls,
		FileID: fileIDs[1], Confidence: 1.0,
	})
	if err != nil {
		t.Fatal(err)
	}

	tracker := metrics.NewTracker(adapter.DB())
	defer tracker.Close()

	h := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}

	result, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "pkg.I.M",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}

	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.DispatchInferred) == 0 {
		t.Error("expected dispatch_inferred to be populated")
	}
}
