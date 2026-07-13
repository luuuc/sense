package cli

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedCaptureDB is the G-9 collision fixture at the Lookup level: a frontend
// TypeScript type whose qualified name IS its bare name ("Issue") shadowing
// same-named backend symbols, plus "Widget" as the unique-name control.
func seedCaptureDB(t *testing.T) *sql.DB {
	t.Helper()
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	files := []model.File{
		{Path: "web_src/js/types.ts", Language: "typescript", Hash: "a", IndexedAt: time.Now()},
		{Path: "models/issues/issue.go", Language: "go", Hash: "b", IndexedAt: time.Now()},
		{Path: "modules/migration/issue.go", Language: "go", Hash: "c", IndexedAt: time.Now()},
	}
	fids := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fids[i] = id
	}
	syms := []model.Symbol{
		{FileID: fids[0], Name: "Issue", Qualified: "Issue", Kind: "type", LineStart: 38, LineEnd: 55},
		{FileID: fids[0], Name: "Widget", Qualified: "Widget", Kind: "type", LineStart: 60, LineEnd: 70},
		{FileID: fids[1], Name: "Issue", Qualified: "issues.Issue", Kind: "class", LineStart: 54, LineEnd: 120},
		{FileID: fids[2], Name: "Issue", Qualified: "migration.Issue", Kind: "class", LineStart: 10, LineEnd: 40},
	}
	for i := range syms {
		if _, werr := adapter.WriteSymbol(ctx, &syms[i]); werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
	}
	return adapter.DB()
}

// The G-9 defect: a bare query exact-matching a no-package symbol at tier 1
// silently shadowed every same-named qualified symbol — blast then answered
// "0 affected, risk low" for a repo's central hub. A bare query with
// same-named siblings must surface the whole collision for disambiguation.
func TestLookupBareNameNotCapturedByTopLevelSymbol(t *testing.T) {
	db := seedCaptureDB(t)
	matches, err := Lookup(context.Background(), db, "Issue")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("want all 3 same-named candidates, got %d: %+v", len(matches), matches)
	}
	// The exact-qualified hit ranks first; the set is stamped uniformly
	// with the tier that made it ambiguous (callers read matches[0].Resolution).
	if matches[0].Qualified != "Issue" {
		t.Errorf("matches[0] = %q, want the exact-qualified hit first", matches[0].Qualified)
	}
	for _, m := range matches {
		if m.Resolution != ResExactName {
			t.Errorf("%s Resolution = %q, want uniform %q", m.Qualified, m.Resolution, ResExactName)
		}
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m.Qualified] = true
	}
	if !seen["issues.Issue"] || !seen["migration.Issue"] {
		t.Errorf("union must include the shadowed qualified symbols, got %+v", matches)
	}
}

// Unique-name control: when the name tier adds nothing beyond the tier-1
// hit, the query resolves exactly as before — one match, ResExactQualified.
func TestLookupBareUniqueNameKeepsExactQualified(t *testing.T) {
	db := seedCaptureDB(t)
	matches, err := Lookup(context.Background(), db, "Widget")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Resolution != ResExactQualified {
		t.Errorf("Resolution = %q, want %q (tier 2 added nothing)", matches[0].Resolution, ResExactQualified)
	}
}

// A query with a qualifier separator is untouched by the bare-query rule.
func TestLookupSeparatorQueryUntouchedByCaptureFix(t *testing.T) {
	db := seedCaptureDB(t)
	matches, err := Lookup(context.Background(), db, "issues.Issue")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 || matches[0].Qualified != "issues.Issue" {
		t.Fatalf("want the single exact-qualified match, got %+v", matches)
	}
	if matches[0].Resolution != ResExactQualified {
		t.Errorf("Resolution = %q, want %q", matches[0].Resolution, ResExactQualified)
	}
}

// The file pin still suppresses cross-file additions: pinned to the TS file
// the bare query resolves to the TS symbol alone, pinned to the Go file it
// resolves to the Go hub alone — no union across the pin.
func TestLookupInFileBareQueryHonorsPin(t *testing.T) {
	db := seedCaptureDB(t)
	ctx := context.Background()

	matches, err := LookupInFile(ctx, db, "Issue", "types.ts")
	if err != nil {
		t.Fatalf("LookupInFile: %v", err)
	}
	if len(matches) != 1 || matches[0].Qualified != "Issue" {
		t.Fatalf("pin types.ts: want the TS Issue alone, got %+v", matches)
	}

	matches, err = LookupInFile(ctx, db, "Issue", "models/issues/issue.go")
	if err != nil {
		t.Fatalf("LookupInFile: %v", err)
	}
	if len(matches) != 1 || matches[0].Qualified != "issues.Issue" {
		t.Fatalf("pin issue.go: want issues.Issue alone, got %+v", matches)
	}
}

// Synthetic qualified names (route:orders_path, django-related:addons) carry
// a single colon, which is NOT a qualifier separator — they must stay bare
// and keep resolving through their tier-1 exact-qualified hit (the name
// column holds the same string, so the merge adds nothing). Pins the
// separator set against a future "helpful" addition of ':'.
func TestLookupSyntheticColonNameKeepsTierOne(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	fid, err := adapter.WriteFile(ctx, &model.File{Path: "config/routes.rb", Language: "ruby", Hash: "a", IndexedAt: time.Now()})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "orders_path", Qualified: "route:orders_path", Kind: "method", LineStart: 3, LineEnd: 3,
	}); err != nil {
		t.Fatalf("WriteSymbol: %v", err)
	}

	matches, err := Lookup(ctx, adapter.DB(), "route:orders_path")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 || matches[0].Qualified != "route:orders_path" {
		t.Fatalf("want the synthetic tier-1 match, got %+v", matches)
	}
	if matches[0].Resolution != ResExactQualified {
		t.Errorf("Resolution = %q, want %q", matches[0].Resolution, ResExactQualified)
	}
}
