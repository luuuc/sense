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

func TestBestLevenshtein(t *testing.T) {
	cases := []struct {
		query, name, qualified string
		want                   int
	}{
		{"create", "create", "Discourse::TopicCreator#create", 0},
		{"TopicCreator#create", "create", "Discourse::TopicCreator#create", 0},
		{"TopicCreater#create", "create", "Discourse::TopicCreator#create", 1},
		{"CheckoutService", "CheckoutService", "App::Services::CheckoutService", 0},
		{"Services::CheckoutService", "CheckoutService", "App::Services::CheckoutService", 0},
		{"B.C", "C", "A::B.C#d", 2},
		{"d", "d", "A::B.C#d", 0},
		{"xyz", "create", "Discourse::TopicCreator#create", 6},
		{"A::B.C#d", "d", "A::B.C#d", 0},
	}
	for _, tc := range cases {
		if got := bestLevenshtein(tc.query, tc.name, tc.qualified); got != tc.want {
			t.Errorf("bestLevenshtein(%q, %q, %q) = %d, want %d",
				tc.query, tc.name, tc.qualified, got, tc.want)
		}
	}
}

func TestEscapeLike(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"foo", "foo"},
		{"foo%bar", `foo\%bar`},
		{"foo_bar", `foo\_bar`},
		{`foo\bar`, `foo\\bar`},
		{`%_\`, `\%\_\\`},
		{"TopicCreator#create", "TopicCreator#create"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := escapeLike(tc.in); got != tc.want {
			t.Errorf("escapeLike(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// seedFuzzyResolutionDB builds a fixture modeled after the pitch's
// motivating examples: Discourse-style Ruby symbols with deep
// qualification, multiple "create" methods for disambiguation, and
// edges to rank by connectedness.
func seedFuzzyResolutionDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "index.db")
	ctx := context.Background()

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })

	files := []model.File{
		{Path: "app/models/topic_creator.rb", Language: "ruby", Hash: "a1", IndexedAt: time.Now()},
		{Path: "app/models/post_creator.rb", Language: "ruby", Hash: "a2", IndexedAt: time.Now()},
		{Path: "app/models/category_creator.rb", Language: "ruby", Hash: "a3", IndexedAt: time.Now()},
		{Path: "app/services/checkout.rb", Language: "ruby", Hash: "a4", IndexedAt: time.Now()},
		{Path: "app/controllers/topics.rb", Language: "ruby", Hash: "a5", IndexedAt: time.Now()},
		{Path: "lib/helpers.rb", Language: "ruby", Hash: "a6", IndexedAt: time.Now()},
	}
	fids := make([]int64, len(files))
	for i := range files {
		id, werr := adapter.WriteFile(ctx, &files[i])
		if werr != nil {
			t.Fatalf("WriteFile: %v", werr)
		}
		fids[i] = id
	}

	symbols := []model.Symbol{
		// Discourse-style deep qualification
		{FileID: fids[0], Name: "create", Qualified: "Discourse::TopicCreator#create", Kind: "method", LineStart: 10, LineEnd: 40},
		{FileID: fids[0], Name: "new", Qualified: "Discourse::TopicCreator#new", Kind: "method", LineStart: 5, LineEnd: 8},
		{FileID: fids[0], Name: "TopicCreator", Qualified: "Discourse::TopicCreator", Kind: "class", LineStart: 1, LineEnd: 50},
		// Multiple "create" methods for disambiguation
		{FileID: fids[1], Name: "create", Qualified: "Discourse::PostCreator#create", Kind: "method", LineStart: 15, LineEnd: 45},
		{FileID: fids[2], Name: "create", Qualified: "Discourse::CategoryCreator#create", Kind: "method", LineStart: 8, LineEnd: 20},
		// Deep service qualification
		{FileID: fids[3], Name: "CheckoutService", Qualified: "App::Services::CheckoutService", Kind: "class", LineStart: 1, LineEnd: 100},
		{FileID: fids[3], Name: "process", Qualified: "App::Services::CheckoutService#process", Kind: "method", LineStart: 10, LineEnd: 30},
		// Controller
		{FileID: fids[4], Name: "index", Qualified: "TopicsController#index", Kind: "method", LineStart: 5, LineEnd: 15},
		// Top-level helper
		{FileID: fids[5], Name: "format_date", Qualified: "format_date", Kind: "function", LineStart: 1, LineEnd: 5},
	}
	sids := make([]int64, len(symbols))
	for i := range symbols {
		id, werr := adapter.WriteSymbol(ctx, &symbols[i])
		if werr != nil {
			t.Fatalf("WriteSymbol: %v", werr)
		}
		sids[i] = id
	}

	// TopicCreator#create: most connected "create" — vary the source
	// so each edge is unique. We use all 9 symbols as sources.
	for i := 0; i < len(sids); i++ {
		sid := sids[i]
		if _, werr := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &sid, TargetID: sids[0], Kind: model.EdgeCalls,
			FileID: fids[4], Confidence: 0.9,
		}); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}

	// PostCreator#create: 8 edges
	for i := 0; i < 8; i++ {
		sid := sids[i%len(sids)]
		if _, werr := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &sid, TargetID: sids[3], Kind: model.EdgeCalls,
			FileID: fids[1], Confidence: 0.9,
		}); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}

	// CategoryCreator#create: 3 edges
	for i := 0; i < 3; i++ {
		sid := sids[i%len(sids)]
		if _, werr := adapter.WriteEdge(ctx, &model.Edge{
			SourceID: &sid, TargetID: sids[4], Kind: model.EdgeCalls,
			FileID: fids[2], Confidence: 0.9,
		}); werr != nil {
			t.Fatalf("WriteEdge: %v", werr)
		}
	}

	return adapter.DB()
}

func TestLookupSuffixResolution(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	matches, err := Lookup(ctx, db, "TopicCreator#create")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 suffix match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "Discourse::TopicCreator#create" {
		t.Errorf("qualified = %q, want Discourse::TopicCreator#create", matches[0].Qualified)
	}
	if matches[0].Resolution != ResSuffix {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResSuffix)
	}
}

func TestLookupSuffixCheckoutService(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	matches, err := Lookup(ctx, db, "CheckoutService")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		// "CheckoutService" matches via exact name (tier 2)
		t.Fatalf("want 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "App::Services::CheckoutService" {
		t.Errorf("qualified = %q", matches[0].Qualified)
	}
	if matches[0].Resolution != ResExactName {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResExactName)
	}
}

func TestLookupSuffixDeepQualification(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	matches, err := Lookup(ctx, db, "Services::CheckoutService")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 suffix match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "App::Services::CheckoutService" {
		t.Errorf("qualified = %q", matches[0].Qualified)
	}
	if matches[0].Resolution != ResSuffix {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResSuffix)
	}
}

func TestLookupContainmentMatch(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	matches, err := Lookup(ctx, db, "TopicCreator")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	// "TopicCreator" matches via exact name (tier 2) for the class symbol
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d: %+v", len(matches), matches)
	}
	if matches[0].Qualified != "Discourse::TopicCreator" {
		t.Errorf("qualified = %q", matches[0].Qualified)
	}
}

func TestLookupContainmentFallback(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// "Checkout" is not an exact name or suffix but is contained in
	// qualified names.
	matches, err := Lookup(ctx, db, "Checkout")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected containment matches for 'Checkout'")
	}
	if matches[0].Resolution != ResContainment {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResContainment)
	}
	found := false
	for _, m := range matches {
		if m.Qualified == "App::Services::CheckoutService" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("containment did not find CheckoutService: %+v", matches)
	}
}

func TestLookupFuzzySuggestions(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// "TopicCreater" (typo) has no exact/suffix/containment match but
	// is within Levenshtein distance 1 of "TopicCreator" via the
	// suffix of "Discourse::TopicCreator".
	matches, err := Lookup(ctx, db, "TopicCreater")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected fuzzy suggestions for 'TopicCreater'")
	}
	if matches[0].Resolution != ResFuzzy {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResFuzzy)
	}
	found := false
	for _, m := range matches {
		if m.Qualified == "Discourse::TopicCreator" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("fuzzy did not suggest Discourse::TopicCreator: %+v", matches)
	}
}

func TestLookupCreateDisambiguationByName(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// "create" is an exact name match for 3 symbols → disambiguation
	matches, err := Lookup(ctx, db, "create")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("want 3 matches for 'create', got %d: %+v", len(matches), matches)
	}
	if matches[0].Resolution != ResExactName {
		t.Errorf("resolution = %q, want %q", matches[0].Resolution, ResExactName)
	}
}

func TestLookupExactShortCircuitsSuffix(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// "format_date" is both an exact qualified match and would be
	// found by suffix. Exact must win.
	matches, err := Lookup(ctx, db, "format_date")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(matches))
	}
	if matches[0].Resolution != ResExactQualified {
		t.Errorf("resolution = %q, want %q (exact must short-circuit suffix)",
			matches[0].Resolution, ResExactQualified)
	}
}

func TestLookupEdgeCountRanking(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// All three "create" symbols match via exact name. Verify that
	// EdgeCounts returns different counts we can rank by.
	matches, err := Lookup(ctx, db, "create")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	ids := make([]int64, len(matches))
	for i := range matches {
		ids[i] = matches[i].ID
	}
	counts, err := EdgeCounts(ctx, db, ids)
	if err != nil {
		t.Fatalf("EdgeCounts: %v", err)
	}

	// TopicCreator#create should have the most edges
	var topicID int64
	for _, m := range matches {
		if m.Qualified == "Discourse::TopicCreator#create" {
			topicID = m.ID
			break
		}
	}
	if topicID == 0 {
		t.Fatal("TopicCreator#create not found in matches")
	}
	for _, m := range matches {
		if m.ID != topicID && counts[m.ID] > counts[topicID] {
			t.Errorf("%s has %d edges > TopicCreator#create's %d edges",
				m.Qualified, counts[m.ID], counts[topicID])
		}
	}
}

func TestLookupSuffixLIKEEscaping(t *testing.T) {
	db := seedFuzzyResolutionDB(t)
	ctx := context.Background()

	// "Topic%Creator" contains a LIKE wildcard — suffix and containment
	// tiers must escape it so it doesn't match "TopicCreator" etc.
	matches, err := Lookup(ctx, db, "Topic%Creator")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	for _, m := range matches {
		if m.Resolution == ResSuffix || m.Resolution == ResContainment {
			t.Errorf("LIKE %% wildcard leaked: matched %s via %s", m.Qualified, m.Resolution)
		}
	}

	// Same for underscore — must not act as single-char wildcard.
	matches, err = Lookup(ctx, db, "Topic_Creator")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	for _, m := range matches {
		if m.Resolution == ResSuffix || m.Resolution == ResContainment {
			t.Errorf("LIKE _ wildcard leaked: matched %s via %s", m.Qualified, m.Resolution)
		}
	}
}
