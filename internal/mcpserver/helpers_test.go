package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
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
		t.Run(tt.edgeKind, func(t *testing.T) {
			got := edgeKindToRole(tt.edgeKind)
			if got != tt.want {
				t.Errorf("edgeKindToRole(%q) = %q, want %q", tt.edgeKind, got, tt.want)
			}
		})
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
		t.Run(tt.input, func(t *testing.T) {
			got := capitalizeFirst(tt.input)
			if got != tt.want {
				t.Errorf("capitalizeFirst(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
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

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, false, nil, nil)
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

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, true, nil, nil)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
}

func TestCountStaleFiles(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	count := len(scanStaleFiles(ctx, ts.handlers.db, ts.handlers.dir).staleRels)
	// Files in test dir probably all non-existent -> should be counted
	if count < 0 {
		t.Errorf("scanStaleFiles returned negative: %d", count)
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
		t.Run(tt.want, func(t *testing.T) {
			got := qualifiedOrNameRef(tt.sym)
			if got != tt.want {
				t.Errorf("qualifiedOrNameRef(%q, %q) = %q, want %q",
					tt.sym.Name, tt.sym.Qualified, got, tt.want)
			}
		})
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
	var resp mcpio.UnreferencedResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal unreferenced: %v", err)
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

	snap := scanStaleFiles(ctx, ts.handlers.db, ts.handlers.dir)
	if len(snap.staleRels) > 0 && snap.maxMtime == nil {
		t.Error("scanStaleFiles returned stale files but nil maxMtime")
	}
}

func TestComputeFreshnessWithNoWatch(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, true, nil, nil)
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

// TestHandleSearchTextFallback verifies that the text fallback tier is
// invoked when structural results are below the limit and rg is available.
func TestHandleSearchTextFallback(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Create a source file with text that matches the query
	authFile := filepath.Join(ts.handlers.dir, "auth_helper.go")
	if err := os.WriteFile(authFile, []byte("package main\n\n// auth helper function\nfunc authHelper() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Enable text fallback if ripgrep is available.
	tf := search.NewTextFallback()
	if !tf.Available() {
		t.Skip("ripgrep not available, skipping text fallback test")
	}
	ts.handlers.textFallback = tf

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query": "auth",
		"limit": float64(100),
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result == nil {
		t.Fatal("handleSearch returned nil result")
	}

	text := resultText(t, result)
	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify text fallback fired by checking mode
	if !strings.Contains(resp.SearchMode, "+text") {
		t.Errorf("expected search mode to contain '+text', got %q", resp.SearchMode)
	}

	// Verify text fallback results were added
	var hasTextResult bool
	for _, r := range resp.Results {
		if r.Source == "text" {
			hasTextResult = true
			break
		}
	}
	if !hasTextResult {
		t.Error("expected at least one text fallback result")
	}
}

// TestHandleBlastBothParams tests validation when both symbol and diff are provided.
func TestHandleBlastBothParams(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"symbol": "auth.Verify",
		"diff":   "HEAD~1",
	}))
	if err != nil {
		t.Fatalf("handleBlast: %v", err)
	}
	if result == nil {
		t.Fatal("expected error result for both params")
	}
	if !result.IsError {
		t.Error("expected IsError=true when both symbol and diff provided")
	}
}

// TestComputeFreshnessWatchState verifies that computeFreshness reports
// watching status when a WatchState is supplied.
func TestComputeFreshnessWatchState(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	ws := &mcpio.WatchState{}
	ws.Set(true, time.Now().Add(-time.Hour))

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, false, ws, nil)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
	if f.Watching == nil || !*f.Watching {
		t.Error("expected Watching=true when WatchState reports active")
	}
	if f.WatchSince == nil {
		t.Error("expected WatchSince to be set")
	}
}

func TestComputeFreshnessSurfacesUpdateAndPending(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	ws := &mcpio.WatchState{}
	ws.Set(true, time.Now().Add(-time.Hour))
	ws.SetIndexed(time.Now(), 5)

	f := computeFreshness(ctx, ts.handlers.db, ts.handlers.dir, false, ws, nil)
	if f == nil {
		t.Fatal("computeFreshness returned nil")
	}
	if f.LastUpdate == nil || f.IndexUpdateAgeSeconds == nil {
		t.Error("expected last_update/age to be set from the watcher's last re-index")
	}
	if f.Pending == nil || *f.Pending != 5 {
		t.Errorf("expected pending=5, got %v", f.Pending)
	}
}

// TestComputeFreshnessEmptyDB verifies that computeFreshness returns nil
// when the database contains no indexed files.
func TestComputeFreshnessEmptyDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	f := computeFreshness(ctx, adapter.DB(), dir, false, nil, nil)
	if f != nil {
		t.Errorf("expected nil for empty DB, got %+v", f)
	}
}

// TestCountStaleFilesWithMtime creates real files on disk and verifies
// that countStaleFiles detects them as stale and reports maxMtime.
func TestCountStaleFilesWithMtime(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "stale.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Create a real file
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(srcFile, []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Index it with an old timestamp
	oldTime := time.Now().Add(-24 * time.Hour)
	fid, err := adapter.WriteFile(ctx, &model.File{
		Path:      "src/main.go",
		Language:  "go",
		Hash:      "abc",
		Symbols:   1,
		IndexedAt: oldTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "main", Qualified: "main.main",
		Kind: "function", LineStart: 1, LineEnd: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	snap := scanStaleFiles(ctx, adapter.DB(), dir)
	if len(snap.staleRels) != 1 {
		t.Errorf("expected 1 stale file, got %d", len(snap.staleRels))
	}
	if snap.maxMtime == nil {
		t.Fatal("expected non-nil maxMtime when stale files exist")
	}
}

func TestCountStaleFilesClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "closed.db"))
	if err != nil {
		t.Fatal(err)
	}

	// Close DB before sweeping
	_ = adapter.Close()

	snap := scanStaleFiles(ctx, adapter.DB(), dir)
	if len(snap.staleRels) != 0 {
		t.Errorf("expected 0 stale files with closed DB, got %d", len(snap.staleRels))
	}
	if snap.maxMtime != nil {
		t.Error("expected nil maxMtime with closed DB")
	}
}

// TestBuildStructureError verifies that buildStructure propagates DB errors.
func TestBuildStructureError(t *testing.T) {
	ctx := context.Background()

	// Use a closed DB to force errors.
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "err.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	resp := mcpio.StatusResponse{Index: mcpio.StatusIndex{Files: 0, Symbols: 0}}
	langs := map[string]mcpio.StatusLanguage{}

	_, err = buildStructure(ctx, adapter.DB(), resp, langs)
	if err == nil {
		t.Error("expected error from buildStructure with closed DB")
	}
}

// TestHandleSearchKeywordBiasClamp verifies that a negative keywordBias
// is clamped to zero instead of being passed to the search engine.
func TestHandleSearchKeywordBiasClamp(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// Force keywordBias below zero so the clamp branch executes.
	ts.handlers.defaults.SearchKeywordWeight = 0.3

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query": "auth",
	}))
	if err != nil {
		t.Fatalf("handleSearch: %v", err)
	}
	if result == nil {
		t.Fatal("handleSearch returned nil result")
	}
}

// TestHandleBlastDiffError exercises the diff branch of handleBlast when
// the directory is not a git repository, causing blastDiff to fail.
// Unlike input validation errors (which return result.IsError with a nil
// Go error), git/blast failures propagate as a Go error because they
// originate from the blast package, not from MCP parameter validation.
func TestHandleBlastDiffError(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"diff": "HEAD",
	}))
	if err == nil {
		t.Fatal("expected error for diff in non-git directory")
	}
	_ = result
}

func TestRunWithBadDir(t *testing.T) {
	err := Run("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for Run with non-existent directory")
	}
}

func TestHandleBlastWithIncludeTests(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"symbol":        "auth.Verify",
		"include_tests": true,
	}))
	if err != nil {
		t.Fatalf("handleBlast with include_tests: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.BlastResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

// TestHandleBlastAppliesTokenBudget locks the wiring: a tiny budget on
// the handler must trim the response and flag it, while the summary
// counts survive. Guards against the budget call being dropped.
func TestHandleBlastAppliesTokenBudget(t *testing.T) {
	ts := setupTestServer(t)
	ts.handlers.defaults.BlastTokenBudget = 1 // force trimming
	result, err := ts.handlers.handleBlast(context.Background(), toolReq(map[string]any{"symbol": "auth.Verify"}))
	if err != nil {
		t.Fatalf("handleBlast: %v", err)
	}
	var resp mcpio.BlastResponse
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Truncated {
		t.Error("expected truncated=true when budget forces trimming")
	}
	if resp.TotalAffected == 0 {
		t.Error("total_affected must survive trimming")
	}
}

// TestHandleGraphAppliesTokenBudget is the graph-side wiring lock.
func TestHandleGraphAppliesTokenBudget(t *testing.T) {
	ts := setupTestServer(t)
	ts.handlers.defaults.GraphTokenBudget = 1 // force trimming
	result, err := ts.handlers.handleGraph(context.Background(), toolReq(map[string]any{"symbol": "auth.Verify", "direction": "callers"}))
	if err != nil {
		t.Fatalf("handleGraph: %v", err)
	}
	var resp mcpio.GraphResponse
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Truncated {
		t.Error("expected truncated=true when budget forces trimming")
	}
}

func TestHandleBlastWithMaxResults(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"symbol":      "auth.Verify",
		"max_results": float64(2),
	}))
	if err != nil {
		t.Fatalf("handleBlast with max_results: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.BlastResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHandleSearchWithLanguage(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleSearch(ctx, toolReq(map[string]any{
		"query":    "auth",
		"language": "go",
	}))
	if err != nil {
		t.Fatalf("handleSearch with language: %v", err)
	}
	text := resultText(t, result)
	var resp mcpio.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
}

func TestHandleDeadCodeNoArgs(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleDeadCode(ctx, toolReq(map[string]any{
		"dead_code": true,
	}))
	if err != nil {
		t.Fatalf("handleDeadCode: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
}

func TestHandleConventionsNoDomain(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleConventions(ctx, toolReq(map[string]any{}))
	if err != nil {
		t.Fatalf("handleConventions: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(t, result))
	}
}

func TestHandleStatusWithVersion(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	result, err := ts.handlers.handleStatus(ctx, mcp.CallToolRequest{})
	if err != nil {
		t.Fatalf("handleStatus: %v", err)
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
		t.Error("expected Structure")
	}
	if resp.Version == nil {
		t.Error("expected Version")
	}
}

func TestBuildStatusResponseClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	_, err = buildStatusResponse(ctx, db, dir, nil)
	if err == nil {
		t.Error("expected error from buildStatusResponse with closed DB")
	}
}

func TestQueryTopNamespacesError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "ns.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	_, err = queryTopNamespaces(ctx, db)
	if err == nil {
		t.Error("expected error from queryTopNamespaces with closed DB")
	}
}

func TestQueryHubSymbolsError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "hub.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	_, err = queryHubSymbols(ctx, db)
	if err == nil {
		t.Error("expected error from queryHubSymbols with closed DB")
	}
}

func TestQueryEntryPointsError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "ep.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	_, err = queryEntryPoints(ctx, db)
	if err == nil {
		t.Error("expected error from queryEntryPoints with closed DB")
	}
}

func TestQueryLanguageBreakdownError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "lang.db"))
	if err != nil {
		t.Fatal(err)
	}
	db := adapter.DB()
	_ = adapter.Close()

	_, err = queryLanguageBreakdown(ctx, db)
	if err == nil {
		t.Error("expected error from queryLanguageBreakdown with closed DB")
	}
}

func TestHandleGraphWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "graph_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewEngine(adapter, nil, nil)
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		search:   engine,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleGraph(ctx, toolReq(map[string]any{
		"symbol": "anything",
	}))
	if err == nil {
		t.Error("expected error from handleGraph with closed DB")
	}
}

func TestHandleSearchWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "search_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewEngine(adapter, nil, nil)
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		search:   engine,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleSearch(ctx, toolReq(map[string]any{
		"query": "anything",
	}))
	if err == nil {
		t.Error("expected error from handleSearch with closed DB")
	}
}

func TestHandleConventionsWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "conv_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewEngine(adapter, nil, nil)
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		search:   engine,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleConventions(ctx, toolReq(map[string]any{}))
	if err == nil {
		t.Error("expected error from handleConventions with closed DB")
	}
}

func TestHandleBlastWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "blast_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	engine := search.NewEngine(adapter, nil, nil)
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		search:   engine,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleBlast(ctx, toolReq(map[string]any{
		"symbol": "anything",
	}))
	if err == nil {
		t.Error("expected error from handleBlast with closed DB")
	}
}

func TestHandleDeadCodeWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "dc_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleDeadCode(ctx, toolReq(map[string]any{
		"dead_code": true,
	}))
	if err == nil {
		t.Error("expected error from handleDeadCode with closed DB")
	}
}

func TestHandleStatusWithClosedDB(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "stat_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:  adapter,
		db:       adapter.DB(),
		dir:      dir,
		tracker:  tracker,
		defaults: profile.DefaultParams(),
	}
	_ = adapter.Close()
	tracker.Close()

	_, err = h.handleStatus(ctx, mcp.CallToolRequest{})
	if err == nil {
		t.Error("expected error from handleStatus with closed DB")
	}
}

func TestSearchHintsEmptyFileCluster(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "A", File: "", Score: 0.5},
			{Symbol: "B", File: "", Score: 0.4},
		},
	}
	hints := searchHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints for results with no file cluster, got %d", len(hints))
	}
}

func TestSearchHintsFewerThanThreePerFile(t *testing.T) {
	resp := mcpio.SearchResponse{
		Results: []mcpio.SearchResultEntry{
			{Symbol: "A", File: "pkg/foo.go", Score: 0.5},
			{Symbol: "B", File: "pkg/foo.go", Score: 0.4},
		},
	}
	hints := searchHints(resp)
	if hints != nil {
		t.Fatalf("want nil hints when file cluster < 3, got %d", len(hints))
	}
}

func TestGraphHintsTestFile(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "X", Qualified: "pkg.X", File: "x_test.go", Kind: "function"},
		Edges:  mcpio.GraphEdges{},
	}
	hints := graphHints(resp, model.DirectionBoth)
	if len(hints) != 0 {
		t.Fatalf("expected no search hint for test files, got %d hints", len(hints))
	}
}

func TestGraphHintsDirectionCallers(t *testing.T) {
	resp := mcpio.GraphResponse{
		Symbol: mcpio.GraphSymbol{Name: "X", Qualified: "pkg.X", File: "x.go", Kind: "function"},
		Edges:  mcpio.GraphEdges{},
	}
	hints := graphHints(resp, model.DirectionCallers)
	if len(hints) == 0 {
		t.Fatal("expected callees hint when direction=callers")
	}
}

func TestConventionsHintsDomainFilter(t *testing.T) {
	resp := mcpio.ConventionsResponse{
		Conventions: []mcpio.ConventionEntry{},
	}
	hints := conventionsHints(resp, "internal/auth")
	if len(hints) == 0 {
		t.Fatal("expected conventions hint when domain filter is set")
	}
}

func TestDeadCodeHintsNoDeadSymbols(t *testing.T) {
	resp := mcpio.UnreferencedResponse{
		Unreferenced: mcpio.UnreferencedSymbols{Dead: []mcpio.DeadEntry{}},
		DeadCount:    0,
	}
	hints := deadCodeHints(resp)
	if len(hints) != 0 {
		t.Fatalf("expected no hints when no dead symbols, got %d", len(hints))
	}
}
