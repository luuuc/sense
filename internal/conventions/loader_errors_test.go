package conventions

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

// closedDB opens a real sqlite adapter and immediately closes it, so any query
// against the returned handle fails at QueryContext, the cheapest way to drive
// the loaders' error branches without a fault-injecting driver.
func closedDB(t *testing.T) *sqlite.Adapter {
	t.Helper()
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	return adapter
}

// TestLoadersSurfaceQueryErrors asserts every loader propagates a query failure
// rather than swallowing it. Both the unfiltered query and the chunked
// file-filter path are exercised.
func TestLoadersSurfaceQueryErrors(t *testing.T) {
	ctx := context.Background()
	db := closedDB(t).DB()

	if _, err := resolveFileFilter(ctx, db, "models"); err == nil {
		t.Error("resolveFileFilter: expected error on closed DB")
	}

	filter := []int64{1, 2, 3}
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"loadSymbols/unfiltered", func() error { _, e := loadSymbols(ctx, db, nil); return e }},
		{"loadSymbols/filtered", func() error { _, e := loadSymbols(ctx, db, filter); return e }},
		{"loadEdges/unfiltered", func() error { _, e := loadEdges(ctx, db, nil); return e }},
		{"loadEdges/filtered", func() error { _, e := loadEdges(ctx, db, filter); return e }},
		{"loadFiles/unfiltered", func() error { _, e := loadFiles(ctx, db, nil); return e }},
		{"loadFiles/filtered", func() error { _, e := loadFiles(ctx, db, filter); return e }},
	} {
		if err := tc.call(); err == nil {
			t.Errorf("%s: expected error on closed DB", tc.name)
		}
	}
}

// TestDetectSurfacesLoadError confirms Detect returns the loader error instead
// of a partial result.
func TestDetectSurfacesLoadError(t *testing.T) {
	db := closedDB(t).DB()
	if _, _, err := Detect(context.Background(), db, Options{}); err == nil {
		t.Error("Detect: expected error on closed DB")
	}
}
