package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestHandleSessionStartQueryErrors(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Drop sense_symbols to trigger the first query error.
	if _, err := adapter.DB().ExecContext(ctx, `DROP TABLE sense_symbols`); err != nil {
		t.Fatal(err)
	}
	result, err := handleSessionStart(ctx, nil, adapter, dir)
	if err == nil {
		t.Errorf("expected error when sense_symbols table missing, got result: %v", result)
	}
}

func TestHandleSessionStartEdgeQueryError(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Drop sense_edges to trigger the second query error.
	if _, err := adapter.DB().ExecContext(ctx, `DROP TABLE sense_edges`); err != nil {
		t.Fatal(err)
	}
	result, err := handleSessionStart(ctx, nil, adapter, dir)
	if err == nil {
		t.Errorf("expected error when sense_edges table missing, got result: %v", result)
	}
}

func TestHandleSessionStartLangQueryError(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Rename sense_files to trigger the language query error
	// (symbols still exist so we get past the symbolCount check).
	if _, err := adapter.DB().ExecContext(ctx, `ALTER TABLE sense_files RENAME TO sense_files_bak`); err != nil {
		t.Fatal(err)
	}
	result, err := handleSessionStart(ctx, nil, adapter, dir)
	if err == nil {
		t.Errorf("expected error when sense_files table missing, got result: %v", result)
	}
}

func TestCheckFreshnessCurrent(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	result := checkFreshness(ctx, adapter.DB(), dir)
	if result != "Index is current." {
		t.Errorf("expected fresh index, got %q", result)
	}
}

func TestCheckFreshnessStale(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Touch the source file to make it newer than the scan.
	goFile := filepath.Join(dir, "main.go")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(goFile, future, future); err != nil {
		t.Fatal(err)
	}

	result := checkFreshness(ctx, adapter.DB(), dir)
	if !strings.Contains(result, "stale") {
		t.Errorf("expected stale result, got %q", result)
	}
	if !strings.Contains(result, "1 modified") {
		t.Errorf("expected 1 modified file, got %q", result)
	}
}

func TestCheckFreshnessDeletedFile(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	if err := os.Remove(filepath.Join(dir, "main.go")); err != nil {
		t.Fatal(err)
	}

	result := checkFreshness(ctx, adapter.DB(), dir)
	if !strings.Contains(result, "stale") {
		t.Errorf("expected stale result for deleted file, got %q", result)
	}
	if !strings.Contains(result, "removed") {
		t.Errorf("expected 'removed' in result, got %q", result)
	}
}

func TestCheckFreshnessMissingIndex(t *testing.T) {
	dir := indexedDir(t)
	dbPath := filepath.Join(dir, ".sense", "index.db")
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	// Drop sense_files to simulate missing table.
	if _, err := adapter.DB().ExecContext(ctx, `DROP TABLE sense_files`); err != nil {
		t.Fatal(err)
	}

	result := checkFreshness(ctx, adapter.DB(), dir)
	if result != "" {
		t.Errorf("expected empty string on error, got %q", result)
	}
}

func TestSessionStartIncludesFreshness(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if !strings.Contains(msg, "Index is current.") && !strings.Contains(msg, "Index is stale") {
		t.Errorf("message should contain freshness info, got: %q", msg)
	}
}

func TestSessionStartNoSenseStatusInstruction(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if strings.Contains(msg, "sense_status\"") {
		t.Error("ToolSearch command should not include sense_status")
	}
	if !strings.Contains(msg, "Do NOT call sense_status") {
		t.Error("message should instruct LLM not to call sense_status")
	}
}

func TestSessionStartEmptyIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".sense", "index.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() != "{}\n" {
		t.Errorf("empty index should produce {}, got %q", buf.String())
	}
}

func TestFormatScanAge(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		input string
		want  string
	}{
		{now.Add(-30 * time.Second).Format(time.RFC3339Nano), "just now"},
		{now.Add(-5 * time.Minute).Format(time.RFC3339Nano), "5m0s ago"},
		{"", "unknown"},
		{"not-a-date", "unknown"},
	}
	for _, tc := range cases {
		got := formatScanAge(tc.input, now)
		if got != tc.want {
			t.Errorf("formatScanAge(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSessionStartPopulated(t *testing.T) {
	dir := indexedDir(t)
	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if buf.String() == "{}\n" {
		t.Error("populated index should produce a message, not {}")
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if msg := resp["message"]; msg == "" {
		t.Error("message field is empty")
	} else if !strings.Contains(msg, "symbols") {
		t.Errorf("message should mention symbols: %q", msg)
	}
}

func TestSessionStartWithSummary(t *testing.T) {
	dir := indexedDir(t)
	summaryPath := filepath.Join(dir, ".sense", "summary.md")
	if err := os.WriteFile(summaryPath, []byte("# Test Summary\nThis is a test project.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if !strings.Contains(msg, "--- Codebase Summary ---") {
		t.Error("message should contain summary start marker")
	}
	if !strings.Contains(msg, "--- End Summary ---") {
		t.Error("message should contain summary end marker")
	}
	if !strings.Contains(msg, "# Test Summary") {
		t.Error("message should contain summary content")
	}
}

func TestSessionStartWithoutSummary(t *testing.T) {
	dir := indexedDir(t)
	summaryPath := filepath.Join(dir, ".sense", "summary.md")
	_ = os.Remove(summaryPath)

	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if strings.Contains(msg, "--- Codebase Summary ---") {
		t.Error("message should not contain summary markers when file is absent")
	}
	if !strings.Contains(msg, "symbols") {
		t.Error("message should still contain index stats")
	}
}

func TestSessionStartWithSummaryNoTrailingNewline(t *testing.T) {
	dir := indexedDir(t)
	summaryPath := filepath.Join(dir, ".sense", "summary.md")
	if err := os.WriteFile(summaryPath, []byte("# Summary\nNo trailing newline"), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if !strings.Contains(msg, "No trailing newline\n--- End Summary ---") {
		t.Errorf("summary without trailing newline should get one added before end marker, got: %q", msg)
	}
}

func TestSessionStartWithEmptySummary(t *testing.T) {
	dir := indexedDir(t)
	summaryPath := filepath.Join(dir, ".sense", "summary.md")
	if err := os.WriteFile(summaryPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := Run("session-start", dir, strings.NewReader("{}"), &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var resp map[string]string
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	msg := resp["message"]
	if strings.Contains(msg, "--- Codebase Summary ---") {
		t.Error("message should not contain summary markers when file is empty")
	}
	if !strings.Contains(msg, "symbols") {
		t.Error("message should still contain index stats")
	}
}
