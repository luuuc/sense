package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestSelectSymbolsDistinctTiers(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "select.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	fix, err := BuildFixture(ctx, adapter, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(fix.SymbolIDs) < 3 {
		t.Fatalf("fixture has %d symbols, need at least 3", len(fix.SymbolIDs))
	}

	hub, mid, leaf, err := selectSymbols(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}

	if hub.id == mid.id || hub.id == leaf.id || mid.id == leaf.id {
		t.Errorf("expected 3 distinct symbols, got hub=%d mid=%d leaf=%d", hub.id, mid.id, leaf.id)
	}

	if hub.fanIn < mid.fanIn {
		t.Errorf("hub (%d fans) should have >= fan-in than mid (%d fans)", hub.fanIn, mid.fanIn)
	}
	if mid.fanIn < leaf.fanIn {
		t.Errorf("mid (%d fans) should have >= fan-in than leaf (%d fans)", mid.fanIn, leaf.fanIn)
	}
}

func setupBenchFixture(t *testing.T, symbols int) string {
	t.Helper()
	dir := t.TempDir()
	sensePath := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(sensePath, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(sensePath, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildFixture(ctx, adapter, symbols); err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()
	return dir
}

func TestReportFieldsPopulated(t *testing.T) {
	ctx := context.Background()
	dir := setupBenchFixture(t, 50)

	report, err := Run(ctx, dir, Options{Iterations: 3, SkipScan: true, SkipSearch: true})
	if err != nil {
		t.Fatal(err)
	}

	if report.SymbolCount == 0 {
		t.Error("SymbolCount should be > 0")
	}
	if report.EdgeCount == 0 {
		t.Error("EdgeCount should be > 0")
	}
	if report.FileCount == 0 {
		t.Error("FileCount should be > 0")
	}
	if report.Iterations != 3 {
		t.Errorf("Iterations = %d, want 3", report.Iterations)
	}
	if report.GraphLatency.P50 == 0 {
		t.Error("GraphLatency.P50 should be > 0")
	}
	if report.BlastShallow.P50 == 0 {
		t.Error("BlastShallow.P50 should be > 0")
	}
	if report.BlastDeep.P50 == 0 {
		t.Error("BlastDeep.P50 should be > 0")
	}
	if report.ConventionsLatency.P50 == 0 {
		t.Error("ConventionsLatency.P50 should be > 0")
	}
	if report.StatusLatency.P50 == 0 {
		t.Error("StatusLatency.P50 should be > 0")
	}
	if report.Index.DatabaseBytes == 0 {
		t.Error("Index.DatabaseBytes should be > 0")
	}
}

func TestMarshalJSONValid(t *testing.T) {
	ctx := context.Background()
	dir := setupBenchFixture(t, 50)

	report, err := Run(ctx, dir, Options{Iterations: 3, SkipScan: true, SkipSearch: true})
	if err != nil {
		t.Fatal(err)
	}

	data, err := MarshalJSON(report)
	if err != nil {
		t.Fatal(err)
	}

	var parsed JSONReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}

	if parsed.Project.Symbols == 0 {
		t.Error("JSON project.symbols should be > 0")
	}
	if parsed.Iterations != 3 {
		t.Errorf("JSON iterations = %d, want 3", parsed.Iterations)
	}
	if parsed.Query.Graph.P50Ms == 0 {
		t.Error("JSON query.graph.p50_ms should be > 0")
	}
	if parsed.Query.BlastShort.P50Ms == 0 {
		t.Error("JSON query.blast_1hop.p50_ms should be > 0")
	}
}

func TestPercentiles(t *testing.T) {
	tests := []struct {
		name string
		n    int
		wP50 int
		wP95 int
		wP99 int
	}{
		{"10 items", 10, 5, 10, 10},
		{"100 items", 100, 50, 95, 99},
		{"1 item", 1, 1, 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var durations []time.Duration
			for i := 1; i <= tt.n; i++ {
				durations = append(durations, time.Duration(i)*time.Millisecond)
			}
			l := percentiles(durations)
			p50ms := int(l.P50.Milliseconds())
			p95ms := int(l.P95.Milliseconds())
			p99ms := int(l.P99.Milliseconds())

			if p50ms != tt.wP50 {
				t.Errorf("P50 = %dms, want %dms", p50ms, tt.wP50)
			}
			if p95ms != tt.wP95 {
				t.Errorf("P95 = %dms, want %dms", p95ms, tt.wP95)
			}
			if p99ms != tt.wP99 {
				t.Errorf("P99 = %dms, want %dms", p99ms, tt.wP99)
			}
		})
	}
}

func TestPercentilesEmpty(t *testing.T) {
	l := percentiles(nil)
	if l.P50 != 0 || l.P95 != 0 || l.P99 != 0 {
		t.Errorf("expected all zero for empty slice, got %+v", l)
	}
}

func TestPctEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		vals []time.Duration
		p    float64
		want time.Duration
	}{
		{"empty", nil, 50, 0},
		{"single p50", []time.Duration{5 * time.Millisecond}, 50, 5 * time.Millisecond},
		{"single p99", []time.Duration{5 * time.Millisecond}, 99, 5 * time.Millisecond},
		{"two p50", []time.Duration{1 * time.Millisecond, 2 * time.Millisecond}, 50, 1 * time.Millisecond},
		{"two p100", []time.Duration{1 * time.Millisecond, 2 * time.Millisecond}, 100, 2 * time.Millisecond},
		{"three p0", []time.Duration{1, 2, 3}, 0, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pct(tt.vals, tt.p)
			if got != tt.want {
				t.Errorf("pct(_, %v) = %v, want %v", tt.p, got, tt.want)
			}
		})
	}
}

func TestPctOverflowClamp(t *testing.T) {
	// p > 100 pushes the computed index past the slice; pct must clamp it to
	// the last element rather than panic.
	vals := []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}
	if got := pct(vals, 150); got != 3*time.Millisecond {
		t.Errorf("pct(_, 150) = %v, want 3ms", got)
	}
}

func TestQueryIndexStatsMissingTables(t *testing.T) {
	tests := []struct {
		name string
		drop string
	}{
		{"no files table", "sense_files"},
		{"no symbols table", "sense_symbols"},
		{"no edges table", "sense_edges"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "stats.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = adapter.Close() }()

			dropTable(t, adapter, tt.drop)

			if err := queryIndexStats(ctx, adapter.DB(), &Report{}); err == nil {
				t.Fatalf("expected error when %s is missing", tt.drop)
			}
		})
	}
}

// TestSelectSymbolsRankedQueryError keeps the symbols (so total > 0) but drops
// the edges table the ranked fan-in subquery joins against, forcing the inner
// scan helper and the hub assignment to surface their error.
func TestSelectSymbolsRankedQueryError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "ranked.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	if _, err := BuildFixture(ctx, adapter, 50); err != nil {
		t.Fatal(err)
	}
	dropTable(t, adapter, "sense_edges")

	if _, _, _, err := selectSymbols(ctx, adapter.DB()); err == nil {
		t.Fatal("expected error when sense_edges is missing")
	}
}

func TestRunDefaultsIterations(t *testing.T) {
	ctx := context.Background()
	dir := setupBenchFixture(t, 20)

	// Iterations <= 0 must default to 100 internally while still reporting the
	// raw value back on the report.
	report, err := Run(ctx, dir, Options{Iterations: 0, SkipScan: true, SkipSearch: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Iterations != 100 {
		t.Errorf("Iterations = %d, want 100", report.Iterations)
	}
}

func TestRunWithScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scan measurement in short mode")
	}
	ctx := context.Background()
	dir := setupBenchFixture(t, 20)

	report, err := Run(ctx, dir, Options{Iterations: 2, SkipScan: false, SkipSearch: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.SymbolCount == 0 {
		t.Error("SymbolCount should be > 0")
	}
}

// TestRunEmptyIndex builds a schema-only index with no symbols so selectSymbols
// fails and Run propagates the error.
func TestRunEmptyIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sensePath := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(sensePath, 0o755); err != nil {
		t.Fatal(err)
	}
	adapter, err := sqlite.Open(ctx, filepath.Join(sensePath, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

	if _, err := Run(ctx, dir, Options{Iterations: 2, SkipScan: true, SkipSearch: true}); err == nil {
		t.Fatal("expected error for an index with no symbols")
	}
}

// TestRunAdapterOpenError points the index path at a directory so sqlite.Open
// cannot open it.
func TestRunAdapterOpenError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	// Make .sense/index.db a directory rather than a file.
	if err := os.MkdirAll(filepath.Join(dir, ".sense", "index.db"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(ctx, dir, Options{Iterations: 2, SkipScan: true, SkipSearch: true}); err == nil {
		t.Fatal("expected error when the index path is not a file")
	}
}

func TestSplitQualified(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"foo.bar", []string{"foo", "bar"}},
		{"foo::bar", []string{"foo", "bar"}},
		{"foo#bar", []string{"foo", "bar"}},
		{"foobar", []string{"foobar"}},
		{"a.b.c", []string{"a.b", "c"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitQualified(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitQualified(%q) = %v, want %v", tt.input, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitQualified(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMeasureIndex(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a dummy index.db
	dbPath := filepath.Join(senseDir, "index.db")
	if err := os.WriteFile(dbPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := measureIndex(dir, 10)
	if m.SymbolCount != 10 {
		t.Errorf("SymbolCount = %d, want 10", m.SymbolCount)
	}
	if m.DatabaseBytes == 0 {
		t.Error("DatabaseBytes should be > 0")
	}
	if m.BytesPerSymbol == 0 {
		t.Error("BytesPerSymbol should be > 0")
	}
}

func TestTouchFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("text"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Get original mtime
	info, err := os.Stat(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	origMtime := info.ModTime()

	restore := touchFiles(dir, 1)

	// Verify file was touched (mtime changed)
	info, err = os.Stat(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Equal(origMtime) {
		t.Error("touchFiles should have changed mtime")
	}

	// Restore should bring back original mtime
	restore()
	info, err = os.Stat(filepath.Join(dir, "a.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(origMtime) {
		t.Error("restore should have brought back original mtime")
	}
}

func TestQueryIndexStatsEmpty(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	report := &Report{}
	err = queryIndexStats(ctx, adapter.DB(), report)
	if err != nil {
		t.Fatalf("queryIndexStats: %v", err)
	}
	if report.FileCount != 0 || report.SymbolCount != 0 || report.EdgeCount != 0 {
		t.Errorf("expected all zeros for empty DB, got %+v", report)
	}
}

func TestSelectSymbolsEmptyDB(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	_, _, _, err = selectSymbols(ctx, adapter.DB())
	if err == nil {
		t.Error("expected error for empty DB")
	}
}

func TestSelectSymbolsOneSymbol(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "one.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	fid, err := adapter.WriteFile(ctx, &model.File{Path: "a.go", Language: "go", Hash: "h", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "A", Qualified: "a.A", Kind: "function", LineStart: 1, LineEnd: 1}); err != nil {
		t.Fatal(err)
	}

	hub, mid, leaf, err := selectSymbols(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}
	if hub.id != mid.id || hub.id != leaf.id {
		t.Error("expected same symbol for all tiers when only 1 symbol exists")
	}
}

func TestSelectSymbolsTwoSymbols(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "two.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	fid, err := adapter.WriteFile(ctx, &model.File{Path: "a.go", Language: "go", Hash: "h", Symbols: 2, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "A", Qualified: "a.A", Kind: "function", LineStart: 1, LineEnd: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "B", Qualified: "a.B", Kind: "function", LineStart: 2, LineEnd: 2}); err != nil {
		t.Fatal(err)
	}

	hub, mid, leaf, err := selectSymbols(ctx, adapter.DB())
	if err != nil {
		t.Fatal(err)
	}
	if hub.id != mid.id {
		t.Error("expected hub == mid when only 2 symbols exist")
	}
	if hub.id == leaf.id {
		t.Error("expected hub != leaf when 2 symbols exist")
	}
}

func TestMeasureColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping cold start measurement in short mode")
	}
	// Create a fake binary that exits successfully
	tmpDir := t.TempDir()
	binary := filepath.Join(tmpDir, "fake-sense")

	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	lat := measureColdStart(ctx, binary, tmpDir)
	if lat == 0 {
		t.Error("expected non-zero latency for successful cold start")
	}
}

func TestMeasureColdStartFailure(t *testing.T) {
	// Create a fake binary that exits with error
	tmpDir := t.TempDir()
	binary := filepath.Join(tmpDir, "fake-sense-fail")

	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	lat := measureColdStart(ctx, binary, tmpDir)
	if lat != 0 {
		t.Errorf("expected 0 latency for failed cold start, got %v", lat)
	}
}

func TestPrintLatency(t *testing.T) {
	var buf strings.Builder
	l := Latency{P50: 5 * time.Millisecond, P95: 10 * time.Millisecond, P99: 15 * time.Millisecond}
	printLatency(&buf, "graph", l)
	if buf.Len() == 0 {
		t.Error("printLatency should write output")
	}

	// Test with a very long label that triggers padding clamp
	buf.Reset()
	printLatency(&buf, "very-long-label-that-exceeds-padding", l)
	if buf.Len() == 0 {
		t.Error("printLatency should handle long labels")
	}
}

func TestMeasureScan(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping scan measurement in short mode")
	}
	ctx := context.Background()
	dir := t.TempDir()

	// Create some source files
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Run initial scan to create index
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// First scan creates the index
	_, err := scan.Run(ctx, scan.Options{
		Root:  dir,
		Sense: senseDir,
	})
	if err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	m := measureScan(ctx, dir)
	if m.FullScan == 0 {
		t.Error("expected FullScan to be measured")
	}
}

func TestRunWithSearch(t *testing.T) {
	ctx := context.Background()
	dir := setupBenchFixture(t, 50)

	report, err := Run(ctx, dir, Options{Iterations: 2, SkipScan: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.SymbolCount == 0 {
		t.Error("SymbolCount should be > 0")
	}
}

func TestRunWithColdStart(t *testing.T) {
	ctx := context.Background()
	dir := setupBenchFixture(t, 50)

	// Create a fake binary
	binary := filepath.Join(dir, "fake-sense")
	script := "#!/bin/sh\nexit 0\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	report, err := Run(ctx, dir, Options{Iterations: 2, SkipScan: true, SkipSearch: true, Binary: binary})
	if err != nil {
		t.Fatal(err)
	}
	if report.ColdStartLatency == 0 {
		t.Error("ColdStartLatency should be > 0")
	}
}

// TestBuildSearchEngineDegradesOnError exercises the keyword-only fallback:
// a closed adapter makes BuildEngine fail, and buildSearchEngine must still
// return a usable (keyword-only) engine with a no-op cleanup rather than nil.
func TestBuildSearchEngineDegradesOnError(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "true")
	ctx := context.Background()
	dir := t.TempDir()
	adapter, err := sqlite.Open(ctx, filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = adapter.Close() // closed → BuildEngine's LoadEmbeddings fails

	engine, embedder, cleanup := buildSearchEngine(ctx, adapter, dir)
	defer cleanup()
	if engine == nil {
		t.Fatal("expected non-nil keyword-only engine on construction failure")
	}
	if embedder != nil {
		t.Error("expected nil embedder on construction failure")
	}
}
