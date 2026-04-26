package benchmark

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"

	_ "modernc.org/sqlite"
)

type Options struct {
	Iterations int
	Dir        string
	Binary     string // path to sense binary for cold start measurement
	SkipScan   bool
	SkipSearch bool
}

type Latency struct {
	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
}

type ScanMetrics struct {
	FullScan          time.Duration
	FilesPerSec       float64
	SymbolsPerSec     float64
	IncrementalClean  time.Duration
	IncrementalDirty  time.Duration
}

type IndexMetrics struct {
	DatabaseBytes  int64
	EmbeddingBytes int64
	SymbolCount    int
	BytesPerSymbol float64
}

type MemoryMetrics struct {
	QueryAllocBytes uint64
}

type Report struct {
	Dir         string
	SymbolCount int
	EdgeCount   int
	FileCount   int

	Scan   ScanMetrics
	Index  IndexMetrics
	Memory MemoryMetrics

	GraphLatency       Latency
	SearchKeyword      Latency
	SearchSemantic     Latency
	SearchHybrid       Latency
	BlastShallow       Latency
	BlastDeep          Latency
	ConventionsLatency Latency
	StatusLatency      Latency

	ColdStartLatency time.Duration

	Iterations int
}

type symbolTier struct {
	id    int64
	name  string
	fanIn int
}

func Run(ctx context.Context, dir string, opts Options) (*Report, error) {
	if opts.Iterations <= 0 {
		opts.Iterations = 100
	}
	if dir == "" {
		dir = "."
	}

	dbPath := filepath.Join(dir, ".sense", "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = adapter.Close() }()

	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open read-only db: %w", err)
	}
	defer func() { _ = db.Close() }()

	report := &Report{
		Dir:        dir,
		Iterations: opts.Iterations,
	}

	if err := queryIndexStats(ctx, db, report); err != nil {
		return nil, err
	}

	hub, mid, leaf, err := selectSymbols(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("select symbols: %w", err)
	}

	runtime.GC()
	var memBefore runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	report.GraphLatency = benchGraph(ctx, adapter, hub, mid, leaf, opts.Iterations)
	report.BlastShallow = benchBlast(ctx, db, hub, mid, leaf, 1, opts.Iterations)
	report.BlastDeep = benchBlast(ctx, db, hub, mid, leaf, 3, opts.Iterations)
	report.ConventionsLatency = benchConventions(ctx, db, opts.Iterations)
	report.StatusLatency = benchStatus(ctx, db, opts.Iterations)

	if !opts.SkipSearch {
		searchEngine, embedder, cleanup := buildSearchEngine(ctx, adapter, dir)
		defer cleanup()
		if searchEngine != nil {
			report.SearchKeyword = benchSearch(ctx, searchEngine, hub, false, opts.Iterations)
			if embedder != nil {
				report.SearchSemantic = benchSearch(ctx, searchEngine, hub, true, opts.Iterations)
				report.SearchHybrid = benchSearchHybrid(ctx, searchEngine, hub, opts.Iterations)
			}
		}
	}

	report.Index = measureIndex(dir, report.SymbolCount)

	if opts.Binary != "" {
		report.ColdStartLatency = measureColdStart(ctx, opts.Binary, dir)
	}

	if !opts.SkipScan {
		report.Scan = measureScan(ctx, dir)
	}

	var memAfter runtime.MemStats
	runtime.ReadMemStats(&memAfter)
	report.Memory.QueryAllocBytes = memAfter.TotalAlloc - memBefore.TotalAlloc

	return report, nil
}

func queryIndexStats(ctx context.Context, db *sql.DB, r *Report) error {
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_files").Scan(&r.FileCount); err != nil {
		return fmt.Errorf("count files: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_symbols").Scan(&r.SymbolCount); err != nil {
		return fmt.Errorf("count symbols: %w", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_edges").Scan(&r.EdgeCount); err != nil {
		return fmt.Errorf("count edges: %w", err)
	}
	return nil
}

func selectSymbols(ctx context.Context, db *sql.DB) (hub, mid, leaf symbolTier, err error) {
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_symbols").Scan(&total); err != nil {
		return hub, mid, leaf, fmt.Errorf("count symbols: %w", err)
	}
	if total == 0 {
		return hub, mid, leaf, fmt.Errorf("no symbols in index")
	}

	const ranked = `
		SELECT s.id, s.qualified,
		       (SELECT COUNT(*) FROM sense_edges WHERE target_id = s.id) AS fan_in
		FROM sense_symbols s
		ORDER BY fan_in DESC
		LIMIT 1 OFFSET ?`

	scan := func(offset int) (symbolTier, error) {
		var st symbolTier
		if err := db.QueryRowContext(ctx, ranked, offset).Scan(&st.id, &st.name, &st.fanIn); err != nil {
			return st, fmt.Errorf("select symbol at offset %d: %w", offset, err)
		}
		return st, nil
	}

	hub, err = scan(0)
	if err != nil {
		return hub, mid, leaf, err
	}

	if total == 1 {
		return hub, hub, hub, nil
	}
	if total == 2 {
		leaf, err = scan(1)
		return hub, hub, leaf, err
	}

	mid, err = scan(total / 2)
	if err != nil {
		return hub, mid, leaf, err
	}
	leaf, err = scan(total - 1)
	if err != nil {
		return hub, mid, leaf, err
	}

	return hub, mid, leaf, nil
}

func benchGraph(ctx context.Context, adapter *sqlite.Adapter, hub, mid, leaf symbolTier, n int) Latency {
	db := adapter.DB()
	symbols := []symbolTier{hub, mid, leaf}
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		sym := symbols[i%len(symbols)]
		start := time.Now()
		sc, err := adapter.ReadSymbol(ctx, sym.id)
		if err == nil {
			paths := loadEdgeFilePaths(ctx, db, sc)
			lookup := func(id int64) (string, bool) {
				p, ok := paths[id]
				return p, ok
			}
			_ = mcpio.BuildGraphResponse(sc, lookup, mcpio.BuildGraphRequest{})
		}
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func loadEdgeFilePaths(ctx context.Context, db *sql.DB, sc *model.SymbolContext) map[int64]string {
	seen := map[int64]struct{}{sc.File.ID: {}}
	ids := []int64{sc.File.ID}
	for _, e := range sc.Outbound {
		if _, ok := seen[e.Target.FileID]; !ok {
			seen[e.Target.FileID] = struct{}{}
			ids = append(ids, e.Target.FileID)
		}
	}
	for _, e := range sc.Inbound {
		if _, ok := seen[e.Target.FileID]; !ok {
			seen[e.Target.FileID] = struct{}{}
			ids = append(ids, e.Target.FileID)
		}
	}
	out := make(map[int64]string, len(ids))
	for start := 0; start < len(ids); start += 500 {
		end := start + 500
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		q := `SELECT id, path FROM sense_files WHERE id IN (` + placeholders + `)`
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			continue
		}
		for rows.Next() {
			var id int64
			var path string
			if err := rows.Scan(&id, &path); err != nil {
				break
			}
			out[id] = path
		}
		_ = rows.Close()
	}
	return out
}

func benchBlast(ctx context.Context, db *sql.DB, hub, mid, leaf symbolTier, maxHops, n int) Latency {
	symbols := []symbolTier{hub, mid, leaf}
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		sym := symbols[i%len(symbols)]
		start := time.Now()
		_, _ = blast.Compute(ctx, db, []int64{sym.id}, blast.Options{
			MaxHops:      maxHops,
			IncludeTests: true,
		})
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func benchConventions(ctx context.Context, db *sql.DB, n int) Latency {
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		_, _, _ = conventions.Detect(ctx, db, conventions.Options{})
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func benchStatus(ctx context.Context, db *sql.DB, n int) Latency {
	durations := make([]time.Duration, 0, n)
	var dummy int
	for i := 0; i < n; i++ {
		start := time.Now()
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_files").Scan(&dummy)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_symbols").Scan(&dummy)
		_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_edges").Scan(&dummy)
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func buildSearchEngine(ctx context.Context, adapter *sqlite.Adapter, dir string) (*search.Engine, embed.Embedder, func()) {
	var vectorIdx search.VectorIndex
	var embedder embed.Embedder

	hnswPath := filepath.Join(dir, ".sense", "hnsw.bin")
	idx, loadErr := search.LoadHNSWIndex(hnswPath)
	if loadErr == nil && idx != nil {
		vectorIdx = idx
	} else {
		embeddings, err := adapter.LoadEmbeddings(ctx)
		if err == nil && len(embeddings) > 0 {
			vectorIdx = search.BuildHNSWIndex(embeddings)
		}
	}

	if vectorIdx != nil && vectorIdx.Len() > 0 {
		e, err := embed.NewBundledEmbedder(0)
		if err == nil {
			embedder = e
		}
	}

	engine := search.NewEngine(adapter, vectorIdx, embedder)
	return engine, embedder, func() {
		if embedder != nil {
			_ = embedder.Close()
		}
	}
}

func benchSearch(ctx context.Context, engine *search.Engine, sym symbolTier, semantic bool, n int) Latency {
	query := sym.name
	if !semantic {
		parts := splitQualified(sym.name)
		if len(parts) > 0 {
			query = parts[len(parts)-1]
		}
	}

	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		_, _, _, _ = engine.Search(ctx, search.Options{
			Query: query,
			Limit: 10,
		})
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func benchSearchHybrid(ctx context.Context, engine *search.Engine, sym symbolTier, n int) Latency {
	query := "how does " + sym.name + " work"
	durations := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		start := time.Now()
		_, _, _, _ = engine.Search(ctx, search.Options{
			Query: query,
			Limit: 10,
		})
		durations = append(durations, time.Since(start))
	}
	return percentiles(durations)
}

func measureIndex(dir string, symbolCount int) IndexMetrics {
	var m IndexMetrics
	m.SymbolCount = symbolCount
	dbPath := filepath.Join(dir, ".sense", "index.db")
	if info, err := os.Stat(dbPath); err == nil {
		m.DatabaseBytes = info.Size()
	}
	walPath := dbPath + "-wal"
	if info, err := os.Stat(walPath); err == nil {
		m.DatabaseBytes += info.Size()
	}

	embPath := filepath.Join(dir, ".sense", "hnsw.bin")
	if info, err := os.Stat(embPath); err == nil {
		m.EmbeddingBytes = info.Size()
	}

	if m.DatabaseBytes > 0 && m.SymbolCount > 0 {
		m.BytesPerSymbol = float64(m.DatabaseBytes) / float64(m.SymbolCount)
	}
	return m
}

func measureColdStart(ctx context.Context, binary, dir string) time.Duration {
	start := time.Now()
	cmd := exec.CommandContext(ctx, binary, "status")
	cmd.Dir = dir
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return 0
	}
	return time.Since(start)
}

func measureScan(ctx context.Context, dir string) ScanMetrics {
	var m ScanMetrics

	tmpDir, err := os.MkdirTemp("", "sense-bench-scan-*")
	if err != nil {
		return m
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	start := time.Now()
	result, err := scan.Run(ctx, scan.Options{
		Root:   dir,
		Sense:  filepath.Join(tmpDir, ".sense"),
		Output: nil,
	})
	m.FullScan = time.Since(start)

	if err == nil && result != nil {
		if m.FullScan > 0 {
			m.FilesPerSec = float64(result.Indexed) / m.FullScan.Seconds()
			m.SymbolsPerSec = float64(result.Symbols) / m.FullScan.Seconds()
		}

		start = time.Now()
		_, _ = scan.Run(ctx, scan.Options{
			Root:   dir,
			Sense:  filepath.Join(tmpDir, ".sense"),
			Output: nil,
		})
		m.IncrementalClean = time.Since(start)

		restore := touchFiles(dir, 5)
		start = time.Now()
		_, _ = scan.Run(ctx, scan.Options{
			Root:   dir,
			Sense:  filepath.Join(tmpDir, ".sense"),
			Output: nil,
		})
		m.IncrementalDirty = time.Since(start)
		restore()
	}

	return m
}

func touchFiles(dir string, count int) func() {
	type saved struct {
		path  string
		mtime time.Time
	}
	var originals []saved
	now := time.Now()
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || len(originals) >= count {
			if len(originals) >= count {
				return filepath.SkipAll
			}
			return nil
		}
		ext := filepath.Ext(path)
		switch ext {
		case ".go", ".rb", ".py", ".ts", ".js":
			info, infoErr := d.Info()
			if infoErr == nil {
				originals = append(originals, saved{path: path, mtime: info.ModTime()})
				_ = os.Chtimes(path, now, now)
			}
		}
		return nil
	})
	return func() {
		for _, o := range originals {
			_ = os.Chtimes(o.path, o.mtime, o.mtime)
		}
	}
}

func percentiles(durations []time.Duration) Latency {
	if len(durations) == 0 {
		return Latency{}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	return Latency{
		P50: pct(durations, 50),
		P95: pct(durations, 95),
		P99: pct(durations, 99),
	}
}

func pct(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

func splitQualified(name string) []string {
	for _, sep := range []string{"::", ".", "#"} {
		if i := strings.LastIndex(name, sep); i >= 0 {
			return []string{name[:i], name[i+len(sep):]}
		}
	}
	return []string{name}
}
