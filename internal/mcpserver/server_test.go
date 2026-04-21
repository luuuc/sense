package mcpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/scan"
)

// TestMCPIntegration spawns `sense mcp` as a subprocess, sends
// initialize + one tools/call per tool, and asserts valid responses.
// The test scans the Sense repo itself into a tempdir so there is
// real data behind the handlers.
func TestMCPIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("MCP integration: builds binary + scans repo; run without -short")
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	// Build the binary.
	binPath := filepath.Join(t.TempDir(), "sense")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/sense")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}

	// Scan into a temp project dir.
	projectDir := t.TempDir()
	senseDir := filepath.Join(projectDir, ".sense")
	ctx := context.Background()
	res, err := scan.Run(ctx, scan.Options{
		Root:     repoRoot,
		Sense:    senseDir,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatal("scan produced no symbols")
	}
	t.Logf("scanned %d symbols, %d edges", res.Symbols, res.Edges)

	// Start the MCP server subprocess.
	cmd := exec.Command(binPath, "mcp", "--dir", projectDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	reader := bufio.NewReader(stdout)
	id := 0

	send := func(method string, params any) map[string]any {
		t.Helper()
		id++
		msg := map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
		}
		if params != nil {
			msg["params"] = params
		}
		raw, _ := json.Marshal(msg)
		if _, err := fmt.Fprintf(stdin, "%s\n", raw); err != nil {
			t.Fatalf("write %s: %v", method, err)
		}
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read response for %s: %v", method, err)
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("unmarshal %s response: %v\nraw: %s", method, err, line)
		}
		return resp
	}

	// 1. Initialize
	resp := send("initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "test",
			"version": "0.0.1",
		},
	})
	if resp["error"] != nil {
		t.Fatalf("initialize error: %v", resp["error"])
	}

	// Send initialized notification (no response expected for notifications).
	notif, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	_, _ = fmt.Fprintf(stdin, "%s\n", notif)

	// 2. tools/call: sense.graph
	t.Run("sense.graph", func(t *testing.T) {
		resp := send("tools/call", map[string]any{
			"name":      "sense.graph",
			"arguments": map[string]any{"symbol": "extract.Register"},
		})
		assertToolResult(t, resp, "sense.graph")
		text := extractToolText(t, resp)

		var graph struct {
			Symbol struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
				File string `json:"file"`
			} `json:"symbol"`
			Edges struct {
				CalledBy []any `json:"called_by"`
			} `json:"edges"`
			SenseMetrics struct {
				SymbolsReturned *int `json:"symbols_returned"`
			} `json:"sense_metrics"`
			Freshness *struct {
				IndexAgeSeconds *int64 `json:"index_age_seconds"`
				StaleFilesSeen  *int   `json:"stale_files_seen"`
			} `json:"freshness"`
		}
		if err := json.Unmarshal([]byte(text), &graph); err != nil {
			t.Fatalf("parse graph JSON: %v\n%s", err, text)
		}
		if graph.Symbol.Name != "Register" {
			t.Errorf("symbol.name = %q, want Register", graph.Symbol.Name)
		}
		if graph.Symbol.Kind != "function" {
			t.Errorf("symbol.kind = %q, want function", graph.Symbol.Kind)
		}
		if !strings.Contains(graph.Symbol.File, "internal/extract") {
			t.Errorf("symbol.file = %q, want path containing internal/extract", graph.Symbol.File)
		}
		if len(graph.Edges.CalledBy) < 5 {
			t.Errorf("called_by has %d entries, want >= 5 (one per extractor)", len(graph.Edges.CalledBy))
		}
		if graph.Freshness == nil {
			t.Error("freshness block missing")
		} else if graph.Freshness.IndexAgeSeconds == nil {
			t.Error("freshness.index_age_seconds missing")
		}
	})

	// 3. tools/call: sense.blast
	t.Run("sense.blast", func(t *testing.T) {
		resp := send("tools/call", map[string]any{
			"name":      "sense.blast",
			"arguments": map[string]any{"symbol": "extract.Register"},
		})
		assertToolResult(t, resp, "sense.blast")
		text := extractToolText(t, resp)

		var blastResp struct {
			Symbol        string `json:"symbol"`
			Risk          string `json:"risk"`
			DirectCallers []any  `json:"direct_callers"`
			TotalAffected int    `json:"total_affected"`
			SenseMetrics  struct {
				SymbolsTraversed          int `json:"symbols_traversed"`
				EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
				EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
			} `json:"sense_metrics"`
		}
		if err := json.Unmarshal([]byte(text), &blastResp); err != nil {
			t.Fatalf("parse blast JSON: %v\n%s", err, text)
		}
		if blastResp.Symbol == "" {
			t.Error("symbol field empty")
		}
		switch blastResp.Risk {
		case "low", "medium", "high":
		default:
			t.Errorf("risk = %q, want low/medium/high", blastResp.Risk)
		}
		if len(blastResp.DirectCallers) < 5 {
			t.Errorf("direct_callers = %d, want >= 5", len(blastResp.DirectCallers))
		}
		if blastResp.SenseMetrics.EstimatedFileReadsAvoided == 0 {
			t.Error("expected non-zero estimated_file_reads_avoided")
		}
		if blastResp.SenseMetrics.EstimatedTokensSaved == 0 {
			t.Error("expected non-zero estimated_tokens_saved")
		}
	})

	// 4. tools/call: sense.search (no min_score — regression for B2 blocker)
	t.Run("sense.search_default_min_score", func(t *testing.T) {
		resp := send("tools/call", map[string]any{
			"name":      "sense.search",
			"arguments": map[string]any{"query": "extract register"},
		})
		assertToolResult(t, resp, "sense.search")
		text := extractToolText(t, resp)

		var searchResp struct {
			Results []struct {
				Symbol string  `json:"symbol"`
				Score  float64 `json:"score"`
			} `json:"results"`
		}
		if err := json.Unmarshal([]byte(text), &searchResp); err != nil {
			t.Fatalf("parse search JSON: %v\n%s", err, text)
		}
		if len(searchResp.Results) == 0 {
			t.Fatal("search returned 0 results with default min_score (was 0.5, should be 0.0)")
		}
	})

	// 5. tools/call: sense.status
	t.Run("sense.status", func(t *testing.T) {
		resp := send("tools/call", map[string]any{
			"name":      "sense.status",
			"arguments": map[string]any{},
		})
		assertToolResult(t, resp, "sense.status")
		text := extractToolText(t, resp)

		var status struct {
			Index struct {
				Files   int `json:"files"`
				Symbols int `json:"symbols"`
				Edges   int `json:"edges"`
			} `json:"index"`
			Languages map[string]struct {
				Tier string `json:"tier"`
			} `json:"languages"`
			Freshness struct {
				LastScan              *string `json:"last_scan"`
				IndexAgeSeconds       *int64  `json:"index_age_seconds"`
				StaleFilesSeen        *int    `json:"stale_files_seen"`
				MaxFileMtimeSinceScan *string `json:"max_file_mtime_since_scan"`
			} `json:"freshness"`
		}
		if err := json.Unmarshal([]byte(text), &status); err != nil {
			t.Fatalf("parse status JSON: %v\n%s", err, text)
		}
		if status.Index.Files == 0 {
			t.Error("index.files == 0")
		}
		if status.Index.Symbols == 0 {
			t.Error("index.symbols == 0")
		}
		goLang, ok := status.Languages["go"]
		if !ok {
			t.Error("languages missing 'go' entry")
		} else if goLang.Tier != "full" {
			t.Errorf("go tier = %q, want full", goLang.Tier)
		}
		if status.Freshness.LastScan == nil {
			t.Error("freshness.last_scan missing")
		}
		// max_file_mtime_since_scan requires source files at dir/path;
		// our test scans repoRoot but indexes into a separate tempdir,
		// so stat calls fail and the field is absent. In real usage
		// (--dir = project root) it is always populated. We verify
		// the other freshness fields instead.
		if status.Freshness.StaleFilesSeen == nil {
			t.Error("freshness.stale_files_seen missing")
		}
	})
}

func assertToolResult(t *testing.T, resp map[string]any, tool string) {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("%s returned error: %v", tool, resp["error"])
	}
	result, ok := resp["result"]
	if !ok {
		t.Fatalf("%s: no result in response", tool)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("%s: result is not an object", tool)
	}
	if resultMap["isError"] == true {
		t.Fatalf("%s: tool returned isError=true: %v", tool, resultMap["content"])
	}
}

func extractToolText(t *testing.T, resp map[string]any) string {
	t.Helper()
	result := resp["result"].(map[string]any)
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content in tool result")
	}
	first := content[0].(map[string]any)
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("first content block has no text")
	}
	return text
}
