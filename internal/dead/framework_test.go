package dead

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestQueryHelpersErrorOnClosedDB drives the fact-gathering query helpers
// against a closed database so their error-return paths run. These feed
// buildFacts; a swallowed error there would corrupt the arbiter's inputs.
func TestQueryHelpersErrorOnClosedDB(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = db.Close() // close immediately so queries fail

	queries := []struct {
		name string
		run  func() error
	}{
		{"queryControllerConcernModuleIDs", func() error { _, e := queryControllerConcernModuleIDs(ctx, db); return e }},
		{"queryIncludedModuleIDs", func() error { _, e := queryIncludedModuleIDs(ctx, db); return e }},
		{"queryValueObjectClassIDs", func() error { _, e := queryValueObjectClassIDs(ctx, db); return e }},
		{"queryTestsTargets", func() error { _, e := queryTestsTargets(ctx, db); return e }},
	}
	for _, q := range queries {
		if err := q.run(); err == nil {
			t.Errorf("%s should error on closed DB", q.name)
		}
	}
}
