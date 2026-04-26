package benchmark

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestReportFieldsPopulated(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sensePath := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(sensePath, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(sensePath, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := BuildFixture(ctx, adapter, 50); err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

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
	dir := t.TempDir()
	sensePath := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(sensePath, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(sensePath, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := BuildFixture(ctx, adapter, 50); err != nil {
		t.Fatal(err)
	}
	_ = adapter.Close()

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
