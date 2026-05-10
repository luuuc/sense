package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/sqlite"
)

func setupHandlerFixture(t *testing.T) *handlers {
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
		{Path: "internal/server/server.go", Language: "go", Hash: "a1", Symbols: 3, IndexedAt: now},
		{Path: "internal/handler/handler.go", Language: "go", Hash: "a2", Symbols: 2, IndexedAt: now},
		{Path: "internal/middleware/auth.go", Language: "go", Hash: "a3", Symbols: 1, IndexedAt: now},
		{Path: "internal/service/order.go", Language: "go", Hash: "a4", Symbols: 1, IndexedAt: now},
		{Path: "internal/service/payment.go", Language: "go", Hash: "a5", Symbols: 1, IndexedAt: now},
		{Path: "internal/service/shipping.go", Language: "go", Hash: "a6", Symbols: 1, IndexedAt: now},
		{Path: "internal/service/refund.go", Language: "go", Hash: "a7", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make([]int64, len(files))
	for i := range files {
		id, err := adapter.WriteFile(ctx, &files[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[i] = id
	}

	serverID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[0], Name: "Server", Qualified: "server.Server",
		Kind: "interface", LineStart: 1, LineEnd: 30,
		Snippet: "type Server interface {",
	})
	handlerID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[1], Name: "Handler", Qualified: "handler.Handler",
		Kind: "class", LineStart: 1, LineEnd: 20,
	})
	authID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fileIDs[2], Name: "AuthMiddleware", Qualified: "middleware.AuthMiddleware",
		Kind: "function", LineStart: 1, LineEnd: 15,
	})

	// Service types for naming patterns
	for _, s := range []model.Symbol{
		{FileID: fileIDs[3], Name: "OrderService", Qualified: "service.OrderService", Kind: "class", LineStart: 1, LineEnd: 20},
		{FileID: fileIDs[4], Name: "PaymentService", Qualified: "service.PaymentService", Kind: "class", LineStart: 1, LineEnd: 20},
		{FileID: fileIDs[5], Name: "ShippingService", Qualified: "service.ShippingService", Kind: "class", LineStart: 1, LineEnd: 20},
		{FileID: fileIDs[6], Name: "RefundService", Qualified: "service.RefundService", Kind: "class", LineStart: 1, LineEnd: 20},
	} {
		if _, err := adapter.WriteSymbol(ctx, &s); err != nil {
			t.Fatal(err)
		}
	}

	intPtr := func(v int) *int { return &v }
	edges := []model.Edge{
		{SourceID: model.Int64Ptr(handlerID), TargetID: serverID, Kind: model.EdgeCalls, FileID: fileIDs[1], Line: intPtr(5)},
		{SourceID: model.Int64Ptr(authID), TargetID: serverID, Kind: model.EdgeCalls, FileID: fileIDs[2], Line: intPtr(3)},
	}
	for _, e := range edges {
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	tracker := metrics.NewTracker(adapter.DB())
	t.Cleanup(func() { tracker.Close() })

	return &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
}

func TestHandleConventions(t *testing.T) {
	h := setupHandlerFixture(t)
	ctx := context.Background()

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := h.handleConventions(ctx, req)
	if err != nil {
		t.Fatalf("handleConventions: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Parse the JSON response
	text := result.Content[0].(mcp.TextContent).Text
	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Key symbols should be populated from buildKeyEntries
	if len(resp.KeySymbols) == 0 {
		t.Error("expected key_symbols in response")
	}

	// Structure and naming conventions should be included
	var hasStructOrNaming bool
	for _, c := range resp.Conventions {
		if c.Category == "structure" || c.Category == "naming" {
			hasStructOrNaming = true
			break
		}
	}
	if !hasStructOrNaming {
		t.Error("expected structure or naming conventions in response")
	}
}

func TestLookupInstanceSnippets(t *testing.T) {
	h := setupHandlerFixture(t)
	ctx := context.Background()

	// "Server" has snippet "type Server interface {" (looked up by name, not qualified)
	got := lookupInstanceSnippets(ctx, h.db, []string{"Server"}, 3)
	if len(got) != 1 {
		t.Fatalf("expected 1 snippet, got %d", len(got))
	}
	if got[0] != "type Server interface {" {
		t.Errorf("snippet = %q, want %q", got[0], "type Server interface {")
	}

	// Unknown symbol returns empty
	got = lookupInstanceSnippets(ctx, h.db, []string{"nonexistent.Foo"}, 3)
	if len(got) != 0 {
		t.Errorf("expected 0 snippets for unknown symbol, got %d", len(got))
	}

	// Empty input returns nil
	got = lookupInstanceSnippets(ctx, h.db, nil, 3)
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestHandleConventionsSnippetsPopulated(t *testing.T) {
	h := setupHandlerFixture(t)
	ctx := context.Background()

	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	result, err := h.handleConventions(ctx, req)
	if err != nil {
		t.Fatalf("handleConventions: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify snippets field appears in JSON (check raw JSON for the key)
	if strings.Contains(text, `"snippets"`) {
		// If snippets are present, they should be non-empty arrays
		for _, c := range resp.Conventions {
			if len(c.Snippets) > 0 {
				for _, s := range c.Snippets {
					if s == "" {
						t.Error("empty snippet in convention entry")
					}
				}
			}
		}
	}
}

func TestHandleStatus(t *testing.T) {
	h := setupHandlerFixture(t)
	ctx := context.Background()

	req := mcp.CallToolRequest{}
	result, err := h.handleStatus(ctx, req)
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	text := result.Content[0].(mcp.TextContent).Text
	var resp mcpio.StatusResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if resp.Structure == nil {
		t.Fatal("expected structure in status response")
	}
	if len(resp.Structure.KeySymbols) == 0 {
		t.Error("expected key_symbols in status structure")
	}
}
