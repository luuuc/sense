package blast_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/benchmark"
	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// BenchmarkBlast gates the acceptance criterion from pitch 01-03:
// blast.Compute on a 30K-symbol fan-in tree must stay under ~100ms
// at MaxHops=3. Two sub-benchmarks parameterise the graph scale so
// the cost curve is visible when `go test -bench` runs with -count
// or -benchtime:
//
//   small_graph (~1K symbols, branching 8)  — sanity check that the
//                                              hot path isn't doing
//                                              something O(N²).
//   large_graph (~30K symbols, branching 30) — the pitch's explicit
//                                              acceptance target.
//
// The graph shape is a uniform fan-in tree: symbol 1 is the root,
// symbols at depth k have branching-factor callers in depth k+1,
// so a three-hop BFS from the root reaches roughly 1 + b + b² + b³
// symbols — 585 for small, 27,931 for large. That's a realistic
// "popular symbol" workload and exercises the covering index from
// Card 9, the bulk-symbol hydration from Card 10, and the sort/
// result-assembly paths at scale.
func BenchmarkBlast(b *testing.B) {
	b.Run("small_graph", func(b *testing.B) { runBlastBench(b, 1024, 8) })
	b.Run("large_graph", func(b *testing.B) { runBlastBench(b, 30000, 30) })
}

func BenchmarkBlastHops(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "hops-bench.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = adapter.Close() })

	fix, err := benchmark.BuildFixture(ctx, adapter, 500)
	if err != nil {
		b.Fatalf("BuildFixture: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	subjectID := fix.SymbolIDs[0]

	for _, hops := range []int{1, 2, 3} {
		b.Run(fmt.Sprintf("hops_%d", hops), func(b *testing.B) {
			for b.Loop() {
				_, err := blast.Compute(ctx, db, []int64{subjectID}, blast.Options{MaxHops: hops})
				if err != nil {
					b.Fatalf("Compute: %v", err)
				}
			}
		})
	}
}

func runBlastBench(b *testing.B, n, branching int) {
	b.Helper()
	ctx := context.Background()

	dbPath := filepath.Join(b.TempDir(), "blast-bench.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = adapter.Close() })

	if err := buildFanInGraph(ctx, adapter, n, branching); err != nil {
		b.Fatalf("build graph: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	// Subject is the root — id=1 because SQLite's autoincrement
	// starts at 1. MaxHops=3 matches the pitch's acceptance call.
	const subjectID int64 = 1

	for b.Loop() {
		res, err := blast.Compute(ctx, db, []int64{subjectID}, blast.Options{MaxHops: 3})
		if err != nil {
			b.Fatalf("Compute: %v", err)
		}
		if res.TotalAffected == 0 {
			b.Fatalf("TotalAffected = 0 — graph build failed to wire up callers")
		}
	}
}

// buildFanInGraph writes n symbols into a fresh index in a single
// transaction, linked as a uniform-branching fan-in tree so blast
// from symbol 1 produces predictable depth. The whole graph lives
// in one synthetic file to keep the test data minimal.
//
// Edge topology: for each i in 2..n, symbol i calls symbol
// floor((i-2)/branching) + 1. At branching=30 and n=30000, symbols
// 2..31 call symbol 1, symbols 32..61 call symbol 2, and so on —
// three full tree levels plus a partial fourth.
func buildFanInGraph(ctx context.Context, a *sqlite.Adapter, n, branching int) error {
	return a.InTx(ctx, func() error {
		fileID, err := a.WriteFile(ctx, &model.File{
			Path:      "bench/graph.go",
			Language:  "go",
			Hash:      "bench",
			Symbols:   n,
			IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}

		// Symbols are written in ascending insertion order so their
		// assigned ids match their 1-based index — the fan-in
		// formula below depends on that identity.
		ids := make([]int64, n+1) // ids[1..n] used; 0 is unused
		for i := 1; i <= n; i++ {
			id, err := a.WriteSymbol(ctx, &model.Symbol{
				FileID:    fileID,
				Name:      fmt.Sprintf("sym%d", i),
				Qualified: fmt.Sprintf("bench.sym%d", i),
				Kind:      model.KindFunction,
				LineStart: i,
				LineEnd:   i,
			})
			if err != nil {
				return fmt.Errorf("write symbol %d: %w", i, err)
			}
			ids[i] = id
		}

		for i := 2; i <= n; i++ {
			targetIdx := (i-2)/branching + 1
			if _, err := a.WriteEdge(ctx, &model.Edge{
				SourceID:   &ids[i],
				TargetID:   ids[targetIdx],
				Kind:       model.EdgeCalls,
				FileID:     fileID,
				Confidence: 1.0,
			}); err != nil {
				return fmt.Errorf("write edge %d→%d: %w", i, targetIdx, err)
			}
		}
		return nil
	})
}
