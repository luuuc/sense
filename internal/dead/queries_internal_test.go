package dead

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestQueryErrorPathsNoSchema drives the index-query helpers against a DB
// with no tables, exercising their error-return paths (the happy paths are
// covered by the scan-backed tests). Without this, a query that silently
// swallowed an error would still pass the integration tests.
func TestQueryErrorPathsNoSchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if _, err := queryValueObjectClassIDs(ctx, db); err == nil {
		t.Error("queryValueObjectClassIDs should error when sense_edges is missing")
	}
	// populateNameOccurrences fans out into accumulateCounts, so a missing
	// sense_symbols table surfaces through both.
	if err := populateNameOccurrences(ctx, db, []Symbol{{Name: "x"}}); err == nil {
		t.Error("populateNameOccurrences should error when sense_symbols is missing")
	}
	// Empty candidate set is a fast no-op, not an error.
	if err := populateNameOccurrences(ctx, db, nil); err != nil {
		t.Errorf("populateNameOccurrences(nil) = %v, want nil", err)
	}
}
