package conventions_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/benchmark"
	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/sqlite"
)

func BenchmarkConventionsDetect(b *testing.B) {
	b.ReportAllocs()
	ctx := context.Background()
	dbPath := filepath.Join(b.TempDir(), "conv-bench.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		b.Fatalf("sqlite.Open: %v", err)
	}
	b.Cleanup(func() { _ = adapter.Close() })

	if _, err := benchmark.BuildFixture(ctx, adapter, 500); err != nil {
		b.Fatalf("BuildFixture: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		b.Fatalf("sql.Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	b.Run("no_filter", func(b *testing.B) {
		for b.Loop() {
			_, _, _ = conventions.Detect(ctx, db, conventions.Options{})
		}
	})

	b.Run("domain_filter", func(b *testing.B) {
		for b.Loop() {
			_, _, _ = conventions.Detect(ctx, db, conventions.Options{Domain: "internal"})
		}
	})
}
