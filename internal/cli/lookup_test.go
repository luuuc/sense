package cli

import (
	"bytes"
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// seedLookupDB creates a fresh SQLite index in a tempdir and writes
// the fixture files + symbols the lookup tests need. Returns the
// underlying *sql.DB (for Lookup) and registers a t.Cleanup so the
// adapter closes at the end of the test.
func seedLookupDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	ctx := context.Background()

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	// Two User symbols in different namespaces exercise the
	// ambiguity path. CheckoutService is a unique qualified name
	// exercising the success path. "Ordr" is close enough to
	// "Order" for the fuzzy tier.
	files := []model.File{
		{Path: "app/models/user.rb", Language: "ruby", Hash: "aaa", IndexedAt: time.Now()},
		{Path: "app/models/admin/user.rb", Language: "ruby", Hash: "bbb", IndexedAt: time.Now()},
		{Path: "app/services/checkout_service.rb", Language: "ruby", Hash: "ccc", IndexedAt: time.Now()},
		{Path: "app/models/order.rb", Language: "ruby", Hash: "ddd", IndexedAt: time.Now()},
	}
	fileIDs := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fileIDs[i] = id
	}

	symbols := []model.Symbol{
		{FileID: fileIDs[0], Name: "User", Qualified: "App::User", Kind: "class", LineStart: 3, LineEnd: 20},
		{FileID: fileIDs[1], Name: "User", Qualified: "Admin::User", Kind: "class", LineStart: 2, LineEnd: 15},
		{FileID: fileIDs[2], Name: "CheckoutService", Qualified: "App::Services::CheckoutService", Kind: "class", LineStart: 12, LineEnd: 85},
		{FileID: fileIDs[3], Name: "Order", Qualified: "App::Order", Kind: "class", LineStart: 1, LineEnd: 30},
	}
	for i := range symbols {
		if _, werr := adapter.WriteSymbol(ctx, &symbols[i]); werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
	}
	return adapter.DB()
}

func TestLookupExactQualified(t *testing.T) {
	db := seedLookupDB(t)
	matches, err := Lookup(context.Background(), db, "App::Services::CheckoutService")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "App::Services::CheckoutService" {
		t.Errorf("got %q, want App::Services::CheckoutService", matches[0].Qualified)
	}
	if matches[0].Kind != "class" {
		t.Errorf("kind = %q, want class", matches[0].Kind)
	}
	if matches[0].File != "app/services/checkout_service.rb" {
		t.Errorf("file = %q", matches[0].File)
	}
}

func TestLookupExactUnqualifiedAmbiguous(t *testing.T) {
	db := seedLookupDB(t)
	matches, err := Lookup(context.Background(), db, "User")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("want 2 matches for ambiguous 'User', got %d: %+v", len(matches), matches)
	}
	// Alphabetically sorted by qualified: Admin::User before App::User.
	if matches[0].Qualified != "Admin::User" || matches[1].Qualified != "App::User" {
		t.Errorf("order = [%s, %s], want [Admin::User, App::User]",
			matches[0].Qualified, matches[1].Qualified)
	}
}

// When both exact tiers return nothing, fuzzy kicks in. "Ordr" is
// distance 1 from "Order".
func TestLookupFuzzy(t *testing.T) {
	db := seedLookupDB(t)
	matches, err := Lookup(context.Background(), db, "Ordr")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected fuzzy hit for 'Ordr'")
	}
	found := false
	for _, m := range matches {
		if m.Qualified == "App::Order" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fuzzy did not find App::Order: %+v", matches)
	}
}

// Exact qualified match must NOT fall through to fuzzy even when
// fuzzy would also fire. Pinning the tier order.
func TestLookupExactPreemptsFuzzy(t *testing.T) {
	db := seedLookupDB(t)
	matches, err := Lookup(context.Background(), db, "App::User")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want exactly 1 match for exact qualified 'App::User', got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "App::User" {
		t.Errorf("got %q, want App::User", matches[0].Qualified)
	}
}

func TestLookupNotFound(t *testing.T) {
	db := seedLookupDB(t)
	matches, err := Lookup(context.Background(), db, "NonExistentXYZ")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("expected no matches for 'NonExistentXYZ', got %+v", matches)
	}
}

func TestLookupShortQueryNoFuzzy(t *testing.T) {
	db := seedLookupDB(t)
	// "Us" is distance 2 from "User" but below fuzzyMinQueryLen. And
	// no exact tier matches "Us". So no results.
	matches, err := Lookup(context.Background(), db, "Us")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("short query 'Us' should not fuzzy-match, got %+v", matches)
	}
}

func TestPrintDisambiguation(t *testing.T) {
	matches := []Match{
		{Qualified: "Admin::User", Kind: "class", Language: "ruby", File: "app/models/admin/user.rb", LineStart: 2},
		{Qualified: "App::User", Kind: "class", Language: "ruby", File: "app/models/user.rb", LineStart: 3},
	}
	var buf bytes.Buffer
	PrintDisambiguation(&buf, "User", "sense graph", matches)
	got := buf.String()
	for _, want := range []string{
		`Multiple symbols match "User":`,
		`  1. Admin::User  (class, ruby)  app/models/admin/user.rb:2`,
		`  2. App::User  (class, ruby)  app/models/user.rb:3`,
		`Narrow with: sense graph "User" --file <path> or --language <lang>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("disambiguation missing %q\ngot:\n%s", want, got)
		}
	}
}

func Test_filterMatches(t *testing.T) {
	matches := []Match{
		{ID: 1, Qualified: "Project", Kind: "class", Language: "ruby", File: "app/models/project.rb"},
		{ID: 2, Qualified: "Project", Kind: "function", Language: "javascript", File: "src/Project.js"},
	}

	t.Run("no filter returns all", func(t *testing.T) {
		got := filterMatches(matches, "", "")
		if len(got) != 2 {
			t.Fatalf("want 2 matches, got %d", len(got))
		}
	})

	t.Run("filter by language", func(t *testing.T) {
		got := filterMatches(matches, "", "ruby")
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("want ruby match (id=1), got %+v", got)
		}
	})

	t.Run("filter by language case-insensitive", func(t *testing.T) {
		got := filterMatches(matches, "", "Ruby")
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("want ruby match (id=1), got %+v", got)
		}
	})

	t.Run("filter by file substring", func(t *testing.T) {
		got := filterMatches(matches, "Project.js", "")
		if len(got) != 1 || got[0].ID != 2 {
			t.Fatalf("want JS match (id=2), got %+v", got)
		}
	})

	t.Run("both filters narrow to zero", func(t *testing.T) {
		got := filterMatches(matches, "Project.js", "ruby")
		if len(got) != 0 {
			t.Fatalf("want 0 matches, got %d", len(got))
		}
	})
}

func TestLookupSQLInjectionPayloads(t *testing.T) {
	db := seedLookupDB(t)
	payloads := []string{
		"'; DROP TABLE sense_symbols; --",
		`" OR 1=1 --`,
		"User' UNION SELECT id,name,qualified,kind,path,language,line_start FROM sense_files--",
		"Robert'); DELETE FROM sense_symbols WHERE ('1'='1",
		"' OR ''='",
		`"; ATTACH DATABASE '/tmp/evil.db' AS evil; --`,
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			matches, err := Lookup(context.Background(), db, p)
			if err != nil {
				t.Fatalf("Lookup should not error on injection payload: %v", err)
			}
			if len(matches) != 0 {
				t.Errorf("injection payload should not match any symbols, got %d matches", len(matches))
			}
		})
	}

	// Verify the database is still intact after all payloads.
	matches, err := Lookup(context.Background(), db, "App::User")
	if err != nil {
		t.Fatalf("Lookup after injection attempts: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("database corrupted by injection: want 1 match for App::User, got %d", len(matches))
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"User", "user", 0}, // case-insensitive
		{"Ordr", "Order", 1},
		{"Checkout", "Checkotu", 2}, // transposition as two edits
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
