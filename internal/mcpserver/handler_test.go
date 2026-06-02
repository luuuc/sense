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
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

type testServer struct {
	handlers *handlers
	symbols  map[string]int64
	files    map[string]int64
}

func setupTestServer(t *testing.T) *testServer {
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

	fileData := []model.File{
		{Path: "internal/auth/auth.go", Language: "go", Hash: "h1", Symbols: 3, IndexedAt: now},
		{Path: "internal/auth/token.go", Language: "go", Hash: "h2", Symbols: 2, IndexedAt: now},
		{Path: "internal/handler/handler.go", Language: "go", Hash: "h3", Symbols: 2, IndexedAt: now},
		{Path: "internal/model/user.go", Language: "go", Hash: "h4", Symbols: 2, IndexedAt: now},
		{Path: "internal/model/order.go", Language: "go", Hash: "h5", Symbols: 1, IndexedAt: now},
		{Path: "cmd/main.go", Language: "go", Hash: "h6", Symbols: 1, IndexedAt: now},
		{Path: "internal/auth/auth_test.go", Language: "go", Hash: "h7", Symbols: 1, IndexedAt: now},
	}
	fileIDs := make(map[string]int64, len(fileData))
	for i := range fileData {
		id, err := adapter.WriteFile(ctx, &fileData[i])
		if err != nil {
			t.Fatal(err)
		}
		fileIDs[fileData[i].Path] = id
	}

	type symDef struct {
		file      string
		name      string
		qualified string
		kind      model.SymbolKind
		snippet   string
	}
	symDefs := []symDef{
		{"internal/auth/auth.go", "Authenticator", "auth.Authenticator", "interface", "type Authenticator interface {"},
		{"internal/auth/auth.go", "Verify", "auth.Verify", "function", "func Verify(token string) error {"},
		{"internal/auth/auth.go", "NewAuth", "auth.NewAuth", "function", "func NewAuth() *Auth {"},
		{"internal/auth/token.go", "Token", "auth.Token", "class", "type Token struct {"},
		{"internal/auth/token.go", "Parse", "auth.Parse", "function", "func Parse(raw string) (*Token, error) {"},
		{"internal/handler/handler.go", "HandleRequest", "handler.HandleRequest", "function", "func HandleRequest(w http.ResponseWriter, r *http.Request) {"},
		{"internal/handler/handler.go", "Handler", "handler.Handler", "class", "type Handler struct {"},
		{"internal/model/user.go", "User", "model.User", "class", "type User struct {"},
		{"internal/model/user.go", "FindUser", "model.FindUser", "function", "func FindUser(id int) (*User, error) {"},
		{"internal/model/order.go", "Order", "model.Order", "class", "type Order struct {"},
		{"cmd/main.go", "main", "main.main", "function", "func main() {"},
		{"internal/auth/auth_test.go", "TestVerify", "auth_test.TestVerify", "function", "func TestVerify(t *testing.T) {"},
	}

	symIDs := make(map[string]int64, len(symDefs))
	for _, sd := range symDefs {
		s := &model.Symbol{
			FileID:    fileIDs[sd.file],
			Name:      sd.name,
			Qualified: sd.qualified,
			Kind:      sd.kind,
			LineStart: 1,
			LineEnd:   20,
			Snippet:   sd.snippet,
		}
		id, err := adapter.WriteSymbol(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		symIDs[sd.qualified] = id
	}

	edges := []model.Edge{
		// handler.HandleRequest → auth.Verify
		{SourceID: model.Int64Ptr(symIDs["handler.HandleRequest"]), TargetID: symIDs["auth.Verify"], Kind: model.EdgeCalls, FileID: fileIDs["internal/handler/handler.go"], Line: intPtr(10), Confidence: 1.0},
		// handler.HandleRequest → model.FindUser
		{SourceID: model.Int64Ptr(symIDs["handler.HandleRequest"]), TargetID: symIDs["model.FindUser"], Kind: model.EdgeCalls, FileID: fileIDs["internal/handler/handler.go"], Line: intPtr(12), Confidence: 1.0},
		// auth.Verify → auth.Parse
		{SourceID: model.Int64Ptr(symIDs["auth.Verify"]), TargetID: symIDs["auth.Parse"], Kind: model.EdgeCalls, FileID: fileIDs["internal/auth/auth.go"], Line: intPtr(5), Confidence: 1.0},
		// auth.Parse → auth.Token
		{SourceID: model.Int64Ptr(symIDs["auth.Parse"]), TargetID: symIDs["auth.Token"], Kind: model.EdgeCalls, FileID: fileIDs["internal/auth/token.go"], Line: intPtr(8), Confidence: 1.0},
		// model.FindUser → model.User
		{SourceID: model.Int64Ptr(symIDs["model.FindUser"]), TargetID: symIDs["model.User"], Kind: model.EdgeCalls, FileID: fileIDs["internal/model/user.go"], Line: intPtr(3), Confidence: 1.0},
		// handler.Handler inherits auth.Authenticator
		{SourceID: model.Int64Ptr(symIDs["handler.Handler"]), TargetID: symIDs["auth.Authenticator"], Kind: model.EdgeInherits, FileID: fileIDs["internal/handler/handler.go"], Confidence: 1.0},
		// main → handler.HandleRequest
		{SourceID: model.Int64Ptr(symIDs["main.main"]), TargetID: symIDs["handler.HandleRequest"], Kind: model.EdgeCalls, FileID: fileIDs["cmd/main.go"], Line: intPtr(5), Confidence: 1.0},
		// auth_test.TestVerify → auth.Verify
		{SourceID: model.Int64Ptr(symIDs["auth_test.TestVerify"]), TargetID: symIDs["auth.Verify"], Kind: model.EdgeCalls, FileID: fileIDs["internal/auth/auth_test.go"], Line: intPtr(3), Confidence: 1.0},
	}
	for _, e := range edges {
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	engine := search.NewEngine(adapter, nil, nil)

	tracker := metrics.NewTracker(adapter.DB())
	t.Cleanup(func() { tracker.Close() })

	h := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      engine,
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}

	return &testServer{handlers: h, symbols: symIDs, files: fileIDs}
}

func toolReq(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	return req
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("empty content in result")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("first content block is not TextContent: %T", result.Content[0])
	}
	return tc.Text
}

func TestHandleGraphDepthTooHigh(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "auth.Verify",
		"direction": "callers",
		"depth":     20,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for depth exceeding max")
	}
	text := resultText(t, result)
	if !strings.Contains(text, "exceeds maximum") {
		t.Errorf("expected depth error, got: %s", text)
	}
}

func TestHandleStatusCoversStructure(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleStatus(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", resultText(t, result))
	}
	text := resultText(t, result)
	var resp mcpio.StatusResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Index.Files == 0 {
		t.Error("expected files > 0")
	}
	if resp.Structure == nil {
		t.Error("expected Structure block")
	}
}

func TestHandleBlastWithSymbol(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleBlast(ctx, toolReq(map[string]any{
		"symbol": "auth.Verify",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
}

func TestHandleBlastMissingParams(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleBlast(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing params")
	}
}

func TestHandleConventionsWithMinStrength(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleConventions(ctx, toolReq(map[string]any{
		"min_strength": 0.8,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
}

func TestHandleDeadCodeWithLanguage(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleDeadCode(ctx, toolReq(map[string]any{
		"dead_code": true,
		"language":  "go",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
}

func TestHandleGraphCallees(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	result, err := h.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "handler.HandleRequest",
		"direction": "callees",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Errorf("unexpected error: %s", resultText(t, result))
	}
	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Symbol.Name != "HandleRequest" {
		t.Errorf("symbol.name = %q, want HandleRequest", resp.Symbol.Name)
	}
	if len(resp.Edges.Calls) == 0 {
		t.Error("expected callees for handler.HandleRequest")
	}
}

// --- handleGraph tests ---

func TestHandleGraph(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]any
		isError   bool
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "known symbol returns edges",
			args: map[string]any{"symbol": "auth.Verify"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "Verify" {
					t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
				}
				if len(resp.Edges.CalledBy) == 0 {
					t.Error("expected callers for auth.Verify")
				}
			},
		},
		{
			name:    "empty symbol returns error",
			args:    map[string]any{},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "missing required parameter") {
					t.Errorf("expected missing parameter error, got %q", text)
				}
			},
		},
		{
			name:    "invalid direction returns error",
			args:    map[string]any{"symbol": "auth.Verify", "direction": "invalid"},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "direction must be") {
					t.Errorf("expected direction error, got %q", text)
				}
			},
		},
		{
			name: "unknown symbol returns not-found",
			args: map[string]any{"symbol": "nonexistent.Symbol"},
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "symbol not found") {
					t.Errorf("expected not-found, got %q", text)
				}
			},
		},
		{
			name: "depth 1 explicit",
			args: map[string]any{"symbol": "auth.Verify", "depth": 1},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "Verify" {
					t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
				}
			},
		},
		{
			name: "depth 2 with layers",
			args: map[string]any{"symbol": "handler.HandleRequest", "depth": 2},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "HandleRequest" {
					t.Errorf("symbol.name = %q, want HandleRequest", resp.Symbol.Name)
				}
			},
		},
		{
			name: "direction callers",
			args: map[string]any{"symbol": "auth.Verify", "direction": "callers"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "Verify" {
					t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
				}
			},
		},
		{
			name: "direction callees",
			args: map[string]any{"symbol": "auth.Verify", "direction": "callees"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if len(resp.Edges.Calls) == 0 {
					t.Error("expected callees for auth.Verify")
				}
			},
		},
		{
			name:    "min_confidence above 1 returns error",
			args:    map[string]any{"symbol": "auth.Verify", "min_confidence": 1.5},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "min_confidence must be between") {
					t.Errorf("expected min_confidence range error, got %q", text)
				}
			},
		},
		{
			name: "min_confidence within range is accepted",
			args: map[string]any{"symbol": "auth.Verify", "direction": "callers", "min_confidence": 0.3},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "Verify" {
					t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
				}
			},
		},
		{
			// An explicit 0.0 ("show everything") is clamped to MinGraphConfidence
			// so the builder's zero-means-default guard doesn't swallow it back to
			// the 0.5 floor.
			name: "min_confidence zero is clamped not defaulted",
			args: map[string]any{"symbol": "auth.Verify", "direction": "callers", "min_confidence": 0.0},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.GraphResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol.Name != "Verify" {
					t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleGraph(ctx, toolReq(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.isError && !result.IsError {
				t.Error("expected IsError=true")
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}

// --- handleSearch tests ---

func TestHandleSearch(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]any
		isError   bool
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "keyword query returns results",
			args: map[string]any{"query": "Verify"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.SearchResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if len(resp.Results) == 0 {
					t.Error("expected results for 'Verify' query")
				}
			},
		},
		{
			name:    "empty query returns error",
			args:    map[string]any{},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "missing required parameter") {
					t.Errorf("expected missing parameter error, got %q", text)
				}
			},
		},
		{
			name: "language filter go returns results",
			args: map[string]any{"query": "Verify", "language": "go"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.SearchResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if len(resp.Results) == 0 {
					t.Error("expected results for go language filter")
				}
			},
		},
		{
			name: "min score filter",
			args: map[string]any{"query": "Verify", "min_score": 0.5},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.SearchResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
			},
		},
		{
			name: "limit caps results",
			args: map[string]any{"query": "auth", "limit": 2},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.SearchResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if len(resp.Results) > 2 {
					t.Errorf("expected at most 2 results with limit=2, got %d", len(resp.Results))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleSearch(ctx, toolReq(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.isError && !result.IsError {
				t.Error("expected IsError=true")
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}

// --- handleBlast tests ---

func TestHandleBlast(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]any
		isError   bool
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "known symbol returns callers and risk",
			args: map[string]any{"symbol": "auth.Verify"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.BlastResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Symbol == "" {
					t.Error("expected non-empty symbol")
				}
				switch resp.Risk {
				case "low", "medium", "high":
				default:
					t.Errorf("risk = %q, want low/medium/high", resp.Risk)
				}
				if len(resp.DirectCallers) == 0 {
					t.Error("expected direct callers")
				}
				if resp.TotalAffected == 0 {
					t.Error("expected total_affected > 0")
				}
			},
		},
		{
			name:    "missing both symbol and diff",
			args:    map[string]any{},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "pass either") {
					t.Errorf("expected parameter error, got %q", text)
				}
			},
		},
		{
			name:    "both symbol and diff",
			args:    map[string]any{"symbol": "auth.Verify", "diff": "HEAD~1"},
			isError: true,
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "not both") {
					t.Errorf("expected mutual exclusion error, got %q", text)
				}
			},
		},
		{
			name: "unknown symbol returns not-found",
			args: map[string]any{"symbol": "nonexistent.Foo"},
			checkJSON: func(t *testing.T, text string) {
				if !strings.Contains(text, "symbol not found") {
					t.Errorf("expected not-found, got %q", text)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleBlast(ctx, toolReq(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.isError && !result.IsError {
				t.Error("expected IsError=true")
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}

// --- handleConventions tests ---

func TestHandleConventionsTableDriven(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]any
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "returns conventions",
			args: map[string]any{},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.ConventionsResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if len(resp.KeySymbols) == 0 {
					t.Error("expected key_symbols")
				}
			},
		},
		{
			name: "domain filter scopes results",
			args: map[string]any{"domain": "internal/auth"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.ConventionsResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				// Domain filter should return fewer key symbols than unfiltered
				var unfilteredResp mcpio.ConventionsResponse
				unfilteredResult, err := h.handleConventions(ctx, toolReq(map[string]any{}))
				if err != nil {
					t.Fatalf("unfiltered call: %v", err)
				}
				if err := json.Unmarshal([]byte(resultText(t, unfilteredResult)), &unfilteredResp); err != nil {
					t.Fatalf("unmarshal unfiltered: %v", err)
				}
				if len(resp.KeySymbols) >= len(unfilteredResp.KeySymbols) && len(unfilteredResp.KeySymbols) > 0 {
					t.Errorf("domain filter should scope results: filtered=%d, unfiltered=%d",
						len(resp.KeySymbols), len(unfilteredResp.KeySymbols))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleConventions(ctx, toolReq(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %s", resultText(t, result))
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}

// --- handleStatus tests ---

func TestHandleStatusTableDriven(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "returns index health and structure",
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.StatusResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Index.Files == 0 {
					t.Error("index.files == 0")
				}
				if resp.Index.Symbols == 0 {
					t.Error("index.symbols == 0")
				}
				if resp.Structure == nil {
					t.Error("expected structure block")
				}
			},
		},
		{
			name: "includes session metrics",
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.StatusResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				if resp.Session == nil {
					t.Error("expected session block")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleStatus(ctx, mcp.CallToolRequest{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %s", resultText(t, result))
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}

// --- handleDeadCode tests ---

func TestHandleDeadCode(t *testing.T) {
	ts := setupTestServer(t)
	h := ts.handlers
	ctx := context.Background()

	tests := []struct {
		name      string
		args      map[string]any
		checkJSON func(t *testing.T, text string)
	}{
		{
			name: "returns unreferenced symbols",
			args: map[string]any{"dead_code": true},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.UnreferencedResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				// Seeded data is Go, but this DB is hand-built and never scanned, so
				// no Go mention harvest ran. The per-language soundness gate fails
				// closed (core_no_harvest), keeping every unreferenced symbol
				// possibly_dead — never an earned dead off an absent harvest.
				if resp.DeadCount != 0 {
					t.Errorf("no Go mention harvest in seeded data; expected 0 earned dead, got %d", resp.DeadCount)
				}
				if resp.PossiblyDeadCount == 0 {
					t.Error("expected possibly_dead symbols (model.Order has zero incoming edges)")
				}
			},
		},
		{
			name: "language filter",
			args: map[string]any{"dead_code": true, "language": "go"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.UnreferencedResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				// All seeded files are go, so this should return some dead symbols
				if resp.TotalSymbols == 0 {
					t.Error("expected total_symbols > 0 with language=go")
				}
			},
		},
		{
			name: "domain filter",
			args: map[string]any{"dead_code": true, "domain": "internal/model"},
			checkJSON: func(t *testing.T, text string) {
				var resp mcpio.UnreferencedResponse
				if err := json.Unmarshal([]byte(text), &resp); err != nil {
					t.Fatalf("unmarshal: %v", err)
				}
				for _, g := range resp.Unreferenced.PossiblyDead {
					for _, s := range g.Symbols {
						if !strings.Contains(s.File, "internal/model") {
							t.Errorf("expected domain-scoped symbol, got file %q", s.File)
						}
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.handleGraph(ctx, toolReq(tt.args))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %s", resultText(t, result))
			}
			if tt.checkJSON != nil {
				tt.checkJSON(t, resultText(t, result))
			}
		})
	}
}
