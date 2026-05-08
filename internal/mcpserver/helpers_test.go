package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
)


func TestEdgeKindToRole(t *testing.T) {
	tests := []struct {
		edgeKind string
		want     string
	}{
		{"inherits", "base class"},
		{"includes", "mixin"},
		{"composes", "mixin"},
		{"calls", "hub"},
		{"tests", "hub"},
		{"imports", "hub"},
		{"", "hub"},
	}
	for _, tt := range tests {
		got := edgeKindToRole(tt.edgeKind)
		if got != tt.want {
			t.Errorf("edgeKindToRole(%q) = %q, want %q", tt.edgeKind, got, tt.want)
		}
	}
}

func TestCapitalizeFirst(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "Hello"},
		{"Hello", "Hello"},
		{"a", "A"},
		{"", ""},
		{"123abc", "123abc"},
	}
	for _, tt := range tests {
		got := capitalizeFirst(tt.input)
		if got != tt.want {
			t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToolErrorInterface(t *testing.T) {
	e := &toolError{msg: "test error"}
	if e.Error() != "test error" {
		t.Errorf("toolError.Error() = %q, want %q", e.Error(), "test error")
	}
}

func TestResolveErrorInterface(t *testing.T) {
	e := &resolveError{result: mcp.NewToolResultError("test")}
	if e.Error() != "resolve: unresolved symbol" {
		t.Errorf("resolveError.Error() = %q, want %q", e.Error(), "resolve: unresolved symbol")
	}
}

func TestSuggestionsResult(t *testing.T) {
	matches := []cli.Match{
		{Name: "Verify", Qualified: "auth.Verify", Kind: "function", File: "auth.go"},
		{Name: "Verify", Qualified: "Verify", Kind: "function", File: "other.go"},
	}
	result := suggestionsResult("Verify", matches)
	if result == nil {
		t.Fatal("suggestionsResult returned nil")
	}
	if len(result.Content) == 0 {
		t.Fatal("suggestionsResult returned empty content")
	}
	tc, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want mcp.TextContent", result.Content[0])
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(tc.Text), &resp); err != nil {
		t.Fatalf("unmarshal suggestions: %v", err)
	}
	suggestions, ok := resp["suggestions"].([]any)
	if !ok || len(suggestions) != 2 {
		t.Errorf("expected 2 suggestions, got %v", resp["suggestions"])
	}
}

func TestNotFoundResult(t *testing.T) {
	result := notFoundResult("SomeSymbol")
	if result == nil {
		t.Fatal("notFoundResult returned nil")
	}
	if len(result.Content) == 0 {
		t.Fatal("notFoundResult returned empty content")
	}
}

func TestLoadFrameworks(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// No frameworks written yet -> should return nil
	frameworks := loadFrameworks(ctx, ts.handlers.db)
	if frameworks != nil {
		t.Errorf("expected nil frameworks for empty db, got %v", frameworks)
	}
}

func TestLoadFrameworksInvalidJSON(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Write invalid JSON to the meta table
	_, _ = ts.handlers.db.ExecContext(ctx, `INSERT INTO sense_meta(key, value) VALUES('frameworks', '{{invalid}')`)
	frameworks := loadFrameworks(ctx, ts.handlers.db)
	if frameworks != nil {
		t.Errorf("expected nil frameworks for invalid JSON, got %v", frameworks)
	}
}

func TestLoadFrameworksValidJSON(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	_, _ = ts.handlers.db.ExecContext(ctx, `INSERT INTO sense_meta(key, value) VALUES('frameworks', '["rails","django"]')`)
	frameworks := loadFrameworks(ctx, ts.handlers.db)
	if len(frameworks) != 2 || frameworks[0] != "rails" || frameworks[1] != "django" {
		t.Errorf("expected [rails django], got %v", frameworks)
	}
}

func TestComputeFreshness(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, false, nil)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
	if f.LastScan == nil {
		t.Error("expected non-nil last_scan")
	}
}

func TestComputeFreshnessWithMaxMtime(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, true, nil)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
}

func TestCountStaleFiles(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	count, _ := countStaleFiles(ctx, ts.handlers.db, ts.handlers.dir)
	// Files in test dir probably all non-existent -> should be counted
	if count < 0 {
		t.Errorf("countStaleFiles returned negative: %d", count)
	}
}

func TestHandleSearchEmptyQuery(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query": "",
	}))
	if err != nil {
		t.Fatalf("handleSearch with empty query: %v", err)
	}
	if result == nil {
		t.Fatal("handleSearch returned nil result for empty query")
	}
}

func TestHandleSearchWithLimit(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query": "auth",
		"limit": float64(5),
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result == nil {
		t.Fatal("handleSearch returned nil result")
	}
}

func TestQualifiedOrNameRef(t *testing.T) {
	tests := []struct {
		sym  model.Symbol
		want string
	}{
		{model.Symbol{Name: "Verify", Qualified: "auth.Verify"}, "auth.Verify"},
		{model.Symbol{Name: "Verify", Qualified: ""}, "Verify"},
	}
	for _, tt := range tests {
		got := qualifiedOrNameRef(tt.sym)
		if got != tt.want {
			t.Errorf("qualifiedOrNameRef(%q, %q) = %q, want %q",
				tt.sym.Name, tt.sym.Qualified, got, tt.want)
		}
	}
}

func TestBuildFingerprintWithFrameworks(t *testing.T) {
	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Files: 10, Symbols: 100},
	}
	langs := map[string]mcpio.StatusLanguage{
		"go":     {Symbols: 80},
		"python": {Symbols: 20},
	}
	ns := []mcpio.StatusNamespace{
		{Name: "internal/auth", Symbols: 30},
		{Name: "internal/model", Symbols: 25},
	}
	hubs := []mcpio.StatusHub{
		{Name: "Verify", Kind: "function", Role: "hub", Callers: 5},
	}
	frameworks := []string{"gin", "gorm"}

	fp := buildFingerprint(resp, langs, ns, hubs, frameworks)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if !strings.Contains(fp, "gin, gorm") {
		t.Errorf("expected frameworks in fingerprint, got: %s", fp)
	}
}

func TestBuildFingerprintNoLangs(t *testing.T) {
	resp := mcpio.StatusResponse{}
	fp := buildFingerprint(resp, map[string]mcpio.StatusLanguage{}, nil, nil, nil)
	if fp != "" {
		t.Errorf("expected empty fingerprint for no languages, got %q", fp)
	}
}

func TestStatusHintsExistingSession(t *testing.T) {
	resp := mcpio.StatusResponse{}
	hints := statusHints(resp, 5)
	for _, h := range hints {
		if h.Tool == "sense_conventions" && strings.Contains(h.Reason, "start of session") {
			t.Error("should not suggest conventions for active session")
		}
	}
}

func TestHandleGraphWithDepth(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// handler.HandleRequest calls auth.Verify calls auth.Parse
	// So depth=2 on auth.Verify should show layers.
	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "auth.Verify",
		"depth":     float64(2),
		"direction": "both",
	}))
	if err != nil {
		t.Fatalf("handleGraph depth=2: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Layers are optional; the test just ensures no crash with depth > 1
	_ = resp.Layers
}

func TestHandleGraphDepthClamp(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "auth.Verify",
		"depth":  float64(0),
	}))
	if err != nil {
		t.Fatalf("handleGraph depth=0: %v", err)
	}
	// depth < 1 should be clamped to 1, not error
	if result.IsError {
		t.Error("depth=0 should be clamped, not error")
	}
}

func TestHandleBlastWithOptions(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"symbol":        "auth.Verify",
		"max_hops":      float64(1),
		"include_tests": false,
	}))
	if err != nil {
		t.Fatalf("handleBlast with options: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.BlastResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Symbol == "" {
		t.Error("expected non-empty symbol")
	}
}

func TestHandleSearchWithMinScore(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query":     "auth",
		"min_score": 0.9,
	}))
	if err != nil {
		t.Fatalf("handleSearch with min_score: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// All results should have score >= 0.9
	for _, r := range resp.Results {
		if float64(r.Score) < 0.9 {
			t.Errorf("result %q has score %.2f below min_score 0.9", r.Symbol, r.Score)
		}
	}
}

func TestHandleGraphCallersDefaultDepth(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "auth.Verify",
		"direction": "callers",
	}))
	if err != nil {
		t.Fatalf("handleGraph callers: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// callers direction defaults to depth=2, so we might see layers
	if resp.Symbol.Name != "Verify" {
		t.Errorf("symbol.name = %q, want Verify", resp.Symbol.Name)
	}
}

func TestReadMetaCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Insert a meta key
	_, _ = ts.handlers.db.ExecContext(ctx, `INSERT INTO sense_meta(key, value) VALUES('cov_test_key', 'cov_test_value')`)

	got := readMeta(ctx, ts.handlers.db, "cov_test_key")
	if got != "cov_test_value" {
		t.Errorf("readMeta = %q, want cov_test_value", got)
	}

	// Missing key
	got2 := readMeta(ctx, ts.handlers.db, "missing_cov_key")
	if got2 != "" {
		t.Errorf("readMeta(missing) = %q, want empty", got2)
	}
}

func TestBuildKeyEntriesWithDomainCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	entries, err := buildKeyEntries(ctx, ts.handlers.adapter, "auth", 8)
	if err != nil {
		t.Fatalf("buildKeyEntries: %v", err)
	}
	for _, e := range entries {
		if e.Name == "" {
			t.Error("buildKeyEntries returned entry with empty name")
		}
	}
}

func TestHandleGraphMaxDepthExceeded(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "auth.Verify",
		"depth":  float64(100),
	}))
	if err != nil {
		t.Fatalf("handleGraph: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for depth exceeding max")
	}
}

func TestHandleDeadCodeViaGraphCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"dead_code": true,
	}))
	if err != nil {
		t.Fatalf("handleGraph dead_code: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.DeadCodeResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal dead code: %v", err)
	}
	if resp.TotalSymbols == 0 {
		t.Error("expected non-zero total_symbols")
	}
}

func TestDisambiguationResultCapped(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()
	matches := make([]cli.Match, 15)
	for i := range matches {
		matches[i] = cli.Match{
			Name:      "Sym",
			Qualified: fmt.Sprintf("pkg%d.Sym", i),
			Kind:      "function",
			File:      fmt.Sprintf("f%d.go", i),
		}
	}
	result := ts.handlers.disambiguationResult(ctx, "Sym", matches)
	if result == nil {
		t.Fatal("disambiguationResult returned nil")
	}
	text := result.Content[0].(mcp.TextContent).Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	topMatches, ok := parsed["top_matches"].([]any)
	if !ok {
		t.Fatal("expected top_matches array")
	}
	if len(topMatches) > disambiguationCap {
		t.Errorf("top_matches count %d exceeds cap %d", len(topMatches), disambiguationCap)
	}
}

func TestHandleSearchLanguageFilter(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query":    "verify",
		"language": "go",
	}))
	if err != nil {
		t.Fatalf("handleSearch language filter: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Verify the response is valid
	if resp.SearchMode == "" {
		t.Error("expected non-empty search_mode")
	}
}

func TestHandleSearchMissingQuery(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result == nil {
		t.Fatal("expected error result for missing query")
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing query")
	}
}

func TestBuildStructureCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	resp := mcpio.StatusResponse{
		Index: mcpio.StatusIndex{Files: 3, Symbols: 10},
	}
	langs := map[string]mcpio.StatusLanguage{
		"go": {Symbols: 10},
	}
	structure, err := buildStructure(ctx, ts.handlers.db, resp, langs)
	if err != nil {
		t.Fatalf("buildStructure: %v", err)
	}
	if structure == nil {
		t.Fatal("buildStructure returned nil")
	}
	if structure.Fingerprint == "" {
		t.Error("expected non-empty fingerprint from seeded fixtures")
	}
}

func TestCountStaleFilesWithRealDir(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	count, maxMtime := countStaleFiles(ctx, ts.handlers.db, ts.handlers.dir)
	if count < 0 {
		t.Errorf("countStaleFiles returned negative count: %d", count)
	}
	if count > 0 && maxMtime == nil {
		t.Error("countStaleFiles returned stale files but nil maxMtime")
	}
}

func TestComputeFreshnessWithNoWatch(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, true, nil)
	if f != nil && f.Watching != nil && *f.Watching {
		t.Error("freshness should not report watching when no WatchState is provided")
	}
}

func TestHandleBlastMissingSymbol(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleBlast: %v", err)
	}
	if result == nil {
		t.Fatal("expected error result for missing symbol")
	}
	if !result.IsError {
		t.Error("expected IsError=true for missing symbol")
	}
}

func TestHandleConventionsCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleConventions(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleConventions: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.ConventionsResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHandleConventionsWithDomain(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleConventions(ctx, toolReq(map[string]any{
		"domain": "auth",
	}))
	if err != nil {
		t.Fatalf("handleConventions with domain: %v", err)
	}
	if result == nil {
		t.Fatal("handleConventions returned nil")
	}
}

func TestHandleStatusCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleStatus(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.StatusResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestResolveDispatchCallersCoverage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleGraph(ctx, toolReq(map[string]any{
		"symbol":    "auth.Verify",
		"direction": "callers",
		"depth":     float64(1),
	}))
	if err != nil {
		t.Fatalf("handleGraph callers: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// auth.Verify is called by handler.HandleRequest
	if len(resp.Edges.CalledBy) == 0 {
		t.Error("expected callers for auth.Verify")
	}
}

func TestDominantMatchSingle(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()
	matches := []cli.Match{
		{Name: "Verify", Qualified: "auth.Verify", Kind: "function"},
	}
	_, ok := ts.handlers.dominantMatch(ctx, matches)
	if ok {
		t.Error("expected no dominant match with single result (needs >=2)")
	}
}

func TestDominantMatchAmbiguous(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()
	matches := []cli.Match{
		{Name: "Process", Qualified: "a.Process", Kind: "function", File: "a.go", LineStart: 1},
		{Name: "Process", Qualified: "b.Process", Kind: "function", File: "b.go", LineStart: 1},
	}
	_, ok := ts.handlers.dominantMatch(ctx, matches)
	if ok {
		t.Error("expected no dominant match for ambiguous results")
	}
}

func TestRunWithOptionsBadDir(t *testing.T) {
	err := RunWithOptions(RunOptions{Dir: "/nonexistent/path/that/does/not/exist"})
	if err == nil {
		t.Fatal("expected error for non-existent directory")
	}
}
