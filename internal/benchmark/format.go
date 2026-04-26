package benchmark

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type JSONReport struct {
	Project    JSONProject     `json:"project"`
	Scan       JSONScan        `json:"scan"`
	Query      JSONQueryReport `json:"query"`
	ColdStart  *JSONColdStart  `json:"cold_start,omitempty"`
	Index      JSONIndex       `json:"index"`
	Memory     JSONMemory      `json:"memory"`
	Iterations int             `json:"iterations"`
}

type JSONProject struct {
	Dir     string `json:"dir"`
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
	Edges   int    `json:"edges"`
}

type JSONScan struct {
	FullMs             float64 `json:"full_ms"`
	FilesPerSec        float64 `json:"files_per_sec"`
	SymbolsPerSec      float64 `json:"symbols_per_sec"`
	IncrementalCleanMs float64 `json:"incremental_clean_ms"`
	IncrementalDirtyMs float64 `json:"incremental_dirty_ms"`
}

type JSONLatency struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
}

type JSONQueryReport struct {
	Graph       JSONLatency  `json:"graph"`
	Keyword     JSONLatency  `json:"search_keyword"`
	Semantic    *JSONLatency `json:"search_semantic,omitempty"`
	Hybrid      *JSONLatency `json:"search_hybrid,omitempty"`
	BlastShort  JSONLatency  `json:"blast_1hop"`
	BlastDeep   JSONLatency  `json:"blast_3hop"`
	Conventions JSONLatency  `json:"conventions"`
	Status      JSONLatency  `json:"status"`
}

type JSONColdStart struct {
	Ms float64 `json:"ms"`
}

type JSONIndex struct {
	DatabaseBytes  int64   `json:"database_bytes"`
	EmbeddingBytes int64   `json:"embedding_bytes"`
	BytesPerSymbol float64 `json:"bytes_per_symbol"`
}

type JSONMemory struct {
	QueryAllocBytes uint64 `json:"query_alloc_bytes"`
}

func MarshalJSON(r *Report) ([]byte, error) {
	jr := JSONReport{
		Project: JSONProject{
			Dir:     r.Dir,
			Files:   r.FileCount,
			Symbols: r.SymbolCount,
			Edges:   r.EdgeCount,
		},
		Scan: JSONScan{
			FullMs:             msFromDuration(r.Scan.FullScan),
			FilesPerSec:        r.Scan.FilesPerSec,
			SymbolsPerSec:      r.Scan.SymbolsPerSec,
			IncrementalCleanMs: msFromDuration(r.Scan.IncrementalClean),
			IncrementalDirtyMs: msFromDuration(r.Scan.IncrementalDirty),
		},
		Query: JSONQueryReport{
			Graph:       latencyToJSON(r.GraphLatency),
			Keyword:     latencyToJSON(r.SearchKeyword),
			BlastShort:  latencyToJSON(r.BlastShallow),
			BlastDeep:   latencyToJSON(r.BlastDeep),
			Conventions: latencyToJSON(r.ConventionsLatency),
			Status:      latencyToJSON(r.StatusLatency),
		},
		Index: JSONIndex{
			DatabaseBytes:  r.Index.DatabaseBytes,
			EmbeddingBytes: r.Index.EmbeddingBytes,
			BytesPerSymbol: r.Index.BytesPerSymbol,
		},
		Memory: JSONMemory{
			QueryAllocBytes: r.Memory.QueryAllocBytes,
		},
		Iterations: r.Iterations,
	}

	if r.SearchSemantic.P50 > 0 {
		l := latencyToJSON(r.SearchSemantic)
		jr.Query.Semantic = &l
	}
	if r.SearchHybrid.P50 > 0 {
		l := latencyToJSON(r.SearchHybrid)
		jr.Query.Hybrid = &l
	}
	if r.ColdStartLatency > 0 {
		jr.ColdStart = &JSONColdStart{Ms: msFromDuration(r.ColdStartLatency)}
	}

	return json.MarshalIndent(jr, "", "  ")
}

func WriteHuman(w io.Writer, r *Report) {
	_, _ = fmt.Fprintf(w, "Benchmark: %s (%d symbols, %d edges, %d files)\n\n",
		r.Dir, r.SymbolCount, r.EdgeCount, r.FileCount)

	_, _ = fmt.Fprintf(w, "Scan\n")
	_, _ = fmt.Fprintf(w, "  full scan .............. %s (%.1f files/sec, %.0f symbols/sec)\n",
		fmtDuration(r.Scan.FullScan), r.Scan.FilesPerSec, r.Scan.SymbolsPerSec)
	_, _ = fmt.Fprintf(w, "  incremental (0 changed)  %s\n", fmtDuration(r.Scan.IncrementalClean))
	_, _ = fmt.Fprintf(w, "  incremental (5 changed)  %s\n", fmtDuration(r.Scan.IncrementalDirty))

	_, _ = fmt.Fprintf(w, "\nQuery latency (%d iterations, p50 / p95 / p99)\n", r.Iterations)
	printLatency(w, "graph", r.GraphLatency)
	printLatency(w, "search (keyword)", r.SearchKeyword)
	if r.SearchSemantic.P50 > 0 {
		printLatency(w, "search (semantic)", r.SearchSemantic)
	}
	if r.SearchHybrid.P50 > 0 {
		printLatency(w, "search (hybrid)", r.SearchHybrid)
	}
	printLatency(w, "blast (1 hop)", r.BlastShallow)
	printLatency(w, "blast (3 hops)", r.BlastDeep)
	printLatency(w, "conventions", r.ConventionsLatency)
	printLatency(w, "status", r.StatusLatency)

	if r.ColdStartLatency > 0 {
		_, _ = fmt.Fprintf(w, "\nCold start\n")
		_, _ = fmt.Fprintf(w, "  mcp launch to ready .... %s\n", fmtDuration(r.ColdStartLatency))
	}

	_, _ = fmt.Fprintf(w, "\nIndex\n")
	_, _ = fmt.Fprintf(w, "  database size .......... %s", fmtBytes(r.Index.DatabaseBytes))
	if r.Index.BytesPerSymbol > 0 {
		_, _ = fmt.Fprintf(w, " (%.1f KB/symbol)", r.Index.BytesPerSymbol/1024)
	}
	_, _ = fmt.Fprintln(w)
	if r.Index.EmbeddingBytes > 0 {
		_, _ = fmt.Fprintf(w, "  embeddings ............. %s\n", fmtBytes(r.Index.EmbeddingBytes))
	}

	_, _ = fmt.Fprintf(w, "\nMemory\n")
	_, _ = fmt.Fprintf(w, "  RSS (query serving) .... %s\n", fmtBytes(int64(r.Memory.QueryAllocBytes)))
}

func printLatency(w io.Writer, label string, l Latency) {
	padding := 25 - len(label)
	if padding < 1 {
		padding = 1
	}
	_, _ = fmt.Fprintf(w, "  %s %s %s / %s / %s\n",
		label, strings.Repeat(".", padding),
		fmtMs(l.P50), fmtMs(l.P95), fmtMs(l.P99))
}

func latencyToJSON(l Latency) JSONLatency {
	return JSONLatency{
		P50Ms: msFromDuration(l.P50),
		P95Ms: msFromDuration(l.P95),
		P99Ms: msFromDuration(l.P99),
	}
}

func msFromDuration(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

func fmtMs(d time.Duration) string {
	ms := msFromDuration(d)
	if ms < 1 {
		return fmt.Sprintf("%.2fms", ms)
	}
	if ms < 100 {
		return fmt.Sprintf("%.1fms", ms)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func fmtDuration(d time.Duration) string {
	if d < time.Millisecond {
		return fmt.Sprintf("%.2fms", float64(d.Microseconds())/1000.0)
	}
	if d < time.Second {
		return fmt.Sprintf("%.0fms", float64(d.Milliseconds()))
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func fmtBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
