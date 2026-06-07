package benchmark

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFmtMs(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0.00ms"},
		{"sub-ms", 500 * time.Microsecond, "0.50ms"},
		{"1ms", 1 * time.Millisecond, "1.0ms"},
		{"ms-range", 50 * time.Millisecond, "50.0ms"},
		{"near-100ms", 99*time.Millisecond + 900*time.Microsecond, "99.9ms"},
		{"100ms", 100 * time.Millisecond, "100ms"},
		{">=100ms", 150 * time.Millisecond, "150ms"},
		{"negative", -1 * time.Millisecond, "-1.00ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmtMs(tt.d)
			if got != tt.want {
				t.Errorf("fmtMs(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0.00ms"},
		{"sub-ms", 500 * time.Microsecond, "0.50ms"},
		{"1ms", 1 * time.Millisecond, "1ms"},
		{"ms-range", 500 * time.Millisecond, "500ms"},
		{"near-1s", 999 * time.Millisecond, "999ms"},
		{"1s", 1 * time.Second, "1.0s"},
		{"second-range", 1500 * time.Millisecond, "1.5s"},
		{"60s", 60 * time.Second, "60.0s"},
		{"negative-ms", -1 * time.Millisecond, "-1.00ms"},
		{"negative-s", -1 * time.Second, "-1000.00ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmtDuration(tt.d)
			if got != tt.want {
				t.Errorf("fmtDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFmtBytes(t *testing.T) {
	tests := []struct {
		name string
		b    int64
		want string
	}{
		{"zero", 0, "0 B"},
		{"1B", 1, "1 B"},
		{"1023B", 1023, "1023 B"},
		{"1KB", 1 << 10, "1.0 KB"},
		{"1.5KB", 1536, "1.5 KB"},
		{"1MB", 1 << 20, "1.0 MB"},
		{"1.5MB", 1572864, "1.5 MB"},
		{"1GB", 1 << 30, "1.0 GB"},
		{"1.5GB", 1610612736, "1.5 GB"},
		{"negative", -1, "-1 B"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fmtBytes(tt.b)
			if got != tt.want {
				t.Errorf("fmtBytes(%d) = %q, want %q", tt.b, got, tt.want)
			}
		})
	}
}

func TestMarshalJSONOptionalFields(t *testing.T) {
	base := func() *Report {
		return &Report{
			Dir:         "/tmp/test",
			FileCount:   10,
			SymbolCount: 100,
			EdgeCount:   200,
			Scan: ScanMetrics{
				FullScan: 1 * time.Second,
			},
			GraphLatency:       Latency{P50: 1 * time.Millisecond},
			SearchKeyword:      Latency{P50: 1 * time.Millisecond},
			BlastShallow:       Latency{P50: 1 * time.Millisecond},
			BlastDeep:          Latency{P50: 1 * time.Millisecond},
			ConventionsLatency: Latency{P50: 1 * time.Millisecond},
			StatusLatency:      Latency{P50: 1 * time.Millisecond},
		}
	}

	tests := []struct {
		name          string
		semantic      time.Duration
		hybrid        time.Duration
		coldStart     time.Duration
		wantSemantic  bool
		wantHybrid    bool
		wantColdStart bool
	}{
		{"none", 0, 0, 0, false, false, false},
		{"semantic-only", 1 * time.Millisecond, 0, 0, true, false, false},
		{"hybrid-only", 0, 1 * time.Millisecond, 0, false, true, false},
		{"coldstart-only", 0, 0, 1 * time.Second, false, false, true},
		{"semantic+hybrid", 1 * time.Millisecond, 1 * time.Millisecond, 0, true, true, false},
		{"semantic+coldstart", 1 * time.Millisecond, 0, 1 * time.Second, true, false, true},
		{"hybrid+coldstart", 0, 1 * time.Millisecond, 1 * time.Second, false, true, true},
		{"all", 1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Second, true, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := base()
			r.SearchSemantic = Latency{P50: tt.semantic}
			r.SearchHybrid = Latency{P50: tt.hybrid}
			r.ColdStartLatency = tt.coldStart

			data, err := MarshalJSON(r)
			if err != nil {
				t.Fatal(err)
			}

			var parsed JSONReport
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatal(err)
			}

			if got := parsed.Query.Semantic != nil; got != tt.wantSemantic {
				t.Errorf("Semantic present = %v, want %v", got, tt.wantSemantic)
			}
			if got := parsed.Query.Hybrid != nil; got != tt.wantHybrid {
				t.Errorf("Hybrid present = %v, want %v", got, tt.wantHybrid)
			}
			if got := parsed.ColdStart != nil; got != tt.wantColdStart {
				t.Errorf("ColdStart present = %v, want %v", got, tt.wantColdStart)
			}
		})
	}
}

func TestMarshalJSONMemory(t *testing.T) {
	r := &Report{
		Dir: "/tmp/test",
		Memory: MemoryMetrics{
			QueryLiveBytes:  1048576,
			QueryAllocBytes: 5242880,
		},
	}

	data, err := MarshalJSON(r)
	if err != nil {
		t.Fatal(err)
	}

	var parsed JSONReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}

	if parsed.Memory.QueryLiveBytes != 1048576 {
		t.Errorf("QueryLiveBytes = %d, want 1048576", parsed.Memory.QueryLiveBytes)
	}
	if parsed.Memory.QueryAllocBytes != 5242880 {
		t.Errorf("QueryAllocBytes = %d, want 5242880", parsed.Memory.QueryAllocBytes)
	}
}

func TestWriteHuman(t *testing.T) {
	r := &Report{
		Dir:         "/tmp/test",
		SymbolCount: 100,
		EdgeCount:   200,
		FileCount:   10,
		Iterations:  50,
		Scan: ScanMetrics{
			FullScan:         2 * time.Second,
			FilesPerSec:      500.5,
			SymbolsPerSec:    1000,
			IncrementalClean: 100 * time.Millisecond,
			IncrementalDirty: 200 * time.Millisecond,
		},
		GraphLatency:       Latency{P50: 1 * time.Millisecond, P95: 2 * time.Millisecond, P99: 3 * time.Millisecond},
		SearchKeyword:      Latency{P50: 4 * time.Millisecond, P95: 5 * time.Millisecond, P99: 6 * time.Millisecond},
		SearchSemantic:     Latency{P50: 7 * time.Millisecond, P95: 8 * time.Millisecond, P99: 9 * time.Millisecond},
		SearchHybrid:       Latency{P50: 10 * time.Millisecond, P95: 11 * time.Millisecond, P99: 12 * time.Millisecond},
		BlastShallow:       Latency{P50: 13 * time.Millisecond, P95: 14 * time.Millisecond, P99: 15 * time.Millisecond},
		BlastDeep:          Latency{P50: 16 * time.Millisecond, P95: 17 * time.Millisecond, P99: 18 * time.Millisecond},
		ConventionsLatency: Latency{P50: 19 * time.Millisecond, P95: 20 * time.Millisecond, P99: 21 * time.Millisecond},
		StatusLatency:      Latency{P50: 22 * time.Millisecond, P95: 23 * time.Millisecond, P99: 24 * time.Millisecond},
		ColdStartLatency:   500 * time.Millisecond,
		Index: IndexMetrics{
			DatabaseBytes:  10485760,
			BytesPerSymbol: 10240,
		},
		Memory: MemoryMetrics{
			QueryLiveBytes:  1048576,
			QueryAllocBytes: 5242880,
		},
	}

	var buf bytes.Buffer
	WriteHuman(&buf, r)
	out := buf.String()

	want := []string{
		"Benchmark: /tmp/test",
		"100 symbols",
		"200 edges",
		"10 files",
		"Scan",
		"full scan",
		"Query latency (50 iterations",
		"graph",
		"search (keyword)",
		"search (semantic)",
		"search (hybrid)",
		"blast (1 hop)",
		"blast (3 hops)",
		"conventions",
		"status",
		"Cold start",
		"mcp launch to ready",
		"Index",
		"database size",
		"Memory",
		"live heap (serving)",
		"alloc churn (queries)",
	}

	for _, s := range want {
		if !strings.Contains(out, s) {
			t.Errorf("output missing expected substring: %q", s)
		}
	}
}
