package sqlite_test

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func seedSearchIndex(ctx context.Context, t *testing.T, a *sqlite.Adapter) {
	t.Helper()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/services/payment.go", Language: "go",
		Hash: "aaa", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fid2, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/user.rb", Language: "ruby",
		Hash: "bbb", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, s := range []model.Symbol{
		{FileID: fid, Name: "ProcessPayment", Qualified: "payment.ProcessPayment", Kind: "function", LineStart: 10, LineEnd: 30, Docstring: "processes credit card payments"},
		{FileID: fid, Name: "RefundPayment", Qualified: "payment.RefundPayment", Kind: "function", LineStart: 35, LineEnd: 50, Docstring: "refunds a payment transaction"},
		{FileID: fid, Name: "PaymentGateway", Qualified: "payment.PaymentGateway", Kind: "type", LineStart: 1, LineEnd: 8},
		{FileID: fid2, Name: "User", Qualified: "User", Kind: "class", LineStart: 1, LineEnd: 100, Docstring: "represents a user account"},
	} {
		if _, err := a.WriteSymbol(ctx, &s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeywordSearch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedSearchIndex(ctx, t, a)

	tests := []struct {
		name     string
		query    string
		language string
		limit    int
		wantMin  int
	}{
		{"match by name", "payment", "", 10, 3},
		{"match by docstring", "credit card", "", 10, 1},
		{"language filter", "payment", "ruby", 10, 0},
		{"limit results", "payment", "", 1, 1},
		{"empty query", "", "", 10, 0},
		{"no match", "nonexistent_xyz", "", 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := a.KeywordSearch(ctx, tt.query, tt.language, tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) < tt.wantMin {
				t.Errorf("got %d results, want at least %d", len(results), tt.wantMin)
			}
			for _, r := range results {
				if r.Score <= 0 {
					t.Errorf("result %q has non-positive score %f", r.Qualified, r.Score)
				}
			}
		})
	}
}

func TestFTS5SyncOnUpdate(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "test.go", Language: "go",
		Hash: "xxx", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sym := &model.Symbol{
		FileID: fid, Name: "OldName", Qualified: "pkg.MyFunc",
		Kind: "function", LineStart: 1, LineEnd: 10,
		Docstring: "old docstring about cats",
	}
	if _, err := a.WriteSymbol(ctx, sym); err != nil {
		t.Fatal(err)
	}

	results, err := a.KeywordSearch(ctx, "cats", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'cats', got %d", len(results))
	}

	// Upsert same (file_id, qualified) with a new docstring.
	sym.Docstring = "new docstring about dogs"
	if _, err := a.WriteSymbol(ctx, sym); err != nil {
		t.Fatal(err)
	}

	results, err = a.KeywordSearch(ctx, "cats", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for 'cats' after update, got %d", len(results))
	}

	results, err = a.KeywordSearch(ctx, "dogs", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'dogs' after update, got %d", len(results))
	}
}

func TestFTS5SyncOnDelete(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "delete_me.go", Language: "go",
		Hash: "zzz", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Ephemeral", Qualified: "pkg.Ephemeral",
		Kind: "function", LineStart: 1, LineEnd: 5,
		Docstring: "temporary function for deletion test",
	}); err != nil {
		t.Fatal(err)
	}

	results, err := a.KeywordSearch(ctx, "Ephemeral", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result before delete, got %d", len(results))
	}

	// Deleting the file cascades to symbols, which fires the FTS delete trigger.
	if err := a.DeleteFile(ctx, "delete_me.go"); err != nil {
		t.Fatal(err)
	}

	results, err = a.KeywordSearch(ctx, "Ephemeral", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results after file delete, got %d", len(results))
	}
}

func TestKeywordSearchSanitization(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedSearchIndex(ctx, t, a)

	// These queries contain FTS5 syntax that would cause errors without sanitization.
	badQueries := []string{
		`payment OR error`,
		`"payment"`,
		`payment*`,
		`payment AND NOT refund`,
		`NEAR(payment refund)`,
	}
	for _, q := range badQueries {
		t.Run(q, func(t *testing.T) {
			_, err := a.KeywordSearch(ctx, q, "", 10)
			if err != nil {
				t.Errorf("query %q should not error after sanitization: %v", q, err)
			}
		})
	}
}

func TestKeywordSearchSQLInjectionPayloads(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedSearchIndex(ctx, t, a)

	payloads := []string{
		"'; DROP TABLE sense_symbols; --",
		`" OR 1=1 --`,
		"payment' UNION SELECT 1,2,3,4,5,6,7,8 FROM sense_files--",
		`"); DELETE FROM sense_symbols WHERE ("1"="1`,
		"' OR ''='",
	}
	for _, p := range payloads {
		t.Run(p, func(t *testing.T) {
			_, err := a.KeywordSearch(ctx, p, "", 10)
			if err != nil {
				t.Errorf("injection payload %q should not cause error: %v", p, err)
			}
		})
	}

	// Verify the database is still intact after all payloads.
	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount after injection attempts: %v", err)
	}
	if count != 4 {
		t.Fatalf("database corrupted by injection: want 4 symbols, got %d", count)
	}
}

func TestLoadEmbeddingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "embed.go", Language: "go",
		Hash: "eee", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	sid1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Foo", Qualified: "pkg.Foo",
		Kind: "function", LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	sid2, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Bar", Qualified: "pkg.Bar",
		Kind: "function", LineStart: 10, LineEnd: 15,
	})
	if err != nil {
		t.Fatal(err)
	}

	vec1 := vectorToBlob([]float32{0.1, 0.2, 0.3, 0.4})
	vec2 := vectorToBlob([]float32{-0.5, 0.6, -0.7, 0.8})

	if err := a.WriteEmbedding(ctx, sid1, vec1); err != nil {
		t.Fatal(err)
	}
	if err := a.WriteEmbedding(ctx, sid2, vec2); err != nil {
		t.Fatal(err)
	}

	loaded, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 embeddings, got %d", len(loaded))
	}

	assertVecEqual(t, loaded[sid1], []float32{0.1, 0.2, 0.3, 0.4})
	assertVecEqual(t, loaded[sid2], []float32{-0.5, 0.6, -0.7, 0.8})
}

func vectorToBlob(vec []float32) []byte {
	buf := make([]byte, len(vec)*4)
	for i, v := range vec {
		bits := math.Float32bits(v)
		buf[i*4] = byte(bits)
		buf[i*4+1] = byte(bits >> 8)
		buf[i*4+2] = byte(bits >> 16)
		buf[i*4+3] = byte(bits >> 24)
	}
	return buf
}

func assertVecEqual(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("vector length mismatch: got %d, want %d", len(got), len(want))
	}
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Errorf("vector[%d]: got %f, want %f", i, got[i], want[i])
		}
	}
}

func TestSymbolCount(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedSearchIndex(ctx, t, a)

	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Errorf("got count %d, want 4", count)
	}
}

func TestKeywordSearchMatchesSnippet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/post.rb", Language: "ruby",
		Hash: "snip1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Post", Qualified: "Post",
		Kind: "class", LineStart: 1, LineEnd: 50,
		Snippet: "class Post < ApplicationRecord\n  belongs_to :user\n  has_many :comments",
	}); err != nil {
		t.Fatal(err)
	}

	// "belongs_to" only appears in the snippet, not in name/qualified/docstring.
	results, err := a.KeywordSearch(ctx, "belongs_to", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'belongs_to' (from snippet), got %d", len(results))
	}
	if results[0].Qualified != "Post" {
		t.Errorf("expected Post, got %q", results[0].Qualified)
	}
}

func TestFTSMigrationAddsSnippet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open creates the current schema (with snippet in FTS).
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/user.rb", Language: "ruby",
		Hash: "m1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "User", Qualified: "User",
		Kind: "class", LineStart: 1, LineEnd: 30,
		Snippet: "class User < ApplicationRecord\n  validates :email",
	}); err != nil {
		t.Fatal(err)
	}

	// Verify snippet search works.
	results, err := a.KeywordSearch(ctx, "validates", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result for 'validates', got %d", len(results))
	}

	// Simulate a pre-migration database by dropping FTS and recreating
	// without the snippet column.
	db := a.DB()
	for _, name := range []string{
		"sense_symbols_fts_insert", "sense_symbols_fts_delete",
		"sense_symbols_fts_update", "sense_symbols_fts_update_after",
	} {
		if _, err := db.ExecContext(ctx, "DROP TRIGGER IF EXISTS "+name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS sense_symbols_fts"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE VIRTUAL TABLE sense_symbols_fts USING fts5(
		name, qualified, docstring, content='sense_symbols', content_rowid='id'
	)`); err != nil {
		t.Fatal(err)
	}
	_ = a.Close()

	// Reopen — should detect stale FTS and migrate.
	a2, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a2.Close() }()

	// After migration the FTS table is empty (drop+recreate loses content).
	// But new inserts should populate snippet in FTS.
	fid2, err := a2.WriteFile(ctx, &model.File{
		Path: "app/models/post.rb", Language: "ruby",
		Hash: "m2", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a2.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Post", Qualified: "Post",
		Kind: "class", LineStart: 1, LineEnd: 20,
		Snippet: "class Post < ApplicationRecord\n  belongs_to :author",
	}); err != nil {
		t.Fatal(err)
	}

	results, err = a2.KeywordSearch(ctx, "belongs_to", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'belongs_to' after migration, got %d", len(results))
	}
}

func TestMultiWordQueryUsesOR(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	seedSearchIndex(ctx, t, a)

	// "credit" appears only in ProcessPayment's docstring, "user" only in
	// the User symbol. No single document contains both. With OR, both
	// should be found; with AND, zero results.
	results, err := a.KeywordSearch(ctx, "credit user", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) < 2 {
		t.Fatalf("multi-word OR query: got %d results, want at least 2 (ProcessPayment + User)", len(results))
	}
	names := map[string]bool{}
	for _, r := range results {
		names[r.Name] = true
	}
	if !names["ProcessPayment"] {
		t.Error("expected ProcessPayment (matches 'credit') in results")
	}
	if !names["User"] {
		t.Error("expected User (matches 'user') in results")
	}
}

func TestNamePartsMatchesDecomposed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "components/payment.tsx", Language: "typescript",
		Hash: "np1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "CopyPaymentLink", Qualified: "CopyPaymentLink",
		Kind: "function", LineStart: 1, LineEnd: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "handleHTTPSError", Qualified: "handleHTTPSError",
		Kind: "function", LineStart: 25, LineEnd: 40,
	}); err != nil {
		t.Fatal(err)
	}

	results, err := a.KeywordSearch(ctx, "PaymentLink", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected PaymentLink to match CopyPaymentLink via name_parts, got 0 results")
	}
	if results[0].Name != "CopyPaymentLink" {
		t.Errorf("top result = %q, want CopyPaymentLink", results[0].Name)
	}

	results, err = a.KeywordSearch(ctx, "HTTPS", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected HTTPS to match handleHTTPSError via name_parts, got 0 results")
	}
}

func TestExactIdentifierRanking(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "internal/server/handler.go", Language: "go",
		Hash: "ident1", Symbols: 4, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	symbols := []model.Symbol{
		{FileID: fid, Name: "HandleRequest", Qualified: "server.HandleRequest", Kind: "function", LineStart: 10, LineEnd: 30},
		{FileID: fid, Name: "max_retries", Qualified: "server.max_retries", Kind: "constant", LineStart: 5, LineEnd: 5},
		{FileID: fid, Name: "HTTPSHandler", Qualified: "server.HTTPSHandler", Kind: "type", LineStart: 35, LineEnd: 50},
		{FileID: fid, Name: "parseJSON", Qualified: "server.parseJSON", Kind: "function", LineStart: 55, LineEnd: 70},
	}
	for _, s := range symbols {
		if _, err := a.WriteSymbol(ctx, &s); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		query string
		want  string
	}{
		{"HandleRequest", "HandleRequest"},
		{"max_retries", "max_retries"},
		{"HTTPSHandler", "HTTPSHandler"},
		{"parseJSON", "parseJSON"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			results, err := a.KeywordSearch(ctx, tt.query, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) == 0 {
				t.Fatalf("no results for %q", tt.query)
			}
			if results[0].Name != tt.want {
				t.Errorf("top result = %q, want %q", results[0].Name, tt.want)
			}
		})
	}
}

func TestLoadEmbeddingsEmpty(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	got, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatalf("LoadEmbeddings on empty table: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %d entries", len(got))
	}
}

func TestSubstringSearch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := sqlite.Open(ctx, filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	seedSearchIndex(ctx, t, a)

	results, err := a.SubstringSearch(ctx, "Payment", "", 10)
	if err != nil {
		t.Fatalf("SubstringSearch: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least 2 results for 'Payment', got %d", len(results))
	}
	for _, r := range results {
		if r.Score != 1.0 {
			t.Errorf("expected score 1.0, got %v", r.Score)
		}
	}

	// Language filter
	results, err = a.SubstringSearch(ctx, "User", "ruby", 10)
	if err != nil {
		t.Fatalf("SubstringSearch with language: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for 'User' in ruby, got %d", len(results))
	}

	// Empty query returns nil
	results, err = a.SubstringSearch(ctx, "", "", 10)
	if err != nil {
		t.Fatalf("SubstringSearch empty: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty query, got %d results", len(results))
	}

	// No match
	results, err = a.SubstringSearch(ctx, "xyznonexistent", "", 10)
	if err != nil {
		t.Fatalf("SubstringSearch no match: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results for nonexistent, got %d", len(results))
	}
}

// TestSearchQueriesErrorOnClosedAdapter drives the query-error branch of each
// search helper at once: a closed adapter makes every underlying query fail,
// so each function must surface an error rather than panic or return stale
// data. Inputs are non-empty so the early empty-input returns are skipped.
func TestSearchQueriesErrorOnClosedAdapter(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = a.Close()

	ids := []int64{1, 2}
	checks := []struct {
		name string
		call func() error
	}{
		{"SymbolCount", func() error { _, e := a.SymbolCount(ctx); return e }},
		{"DocumentFrequency", func() error { _, e := a.DocumentFrequency(ctx, []string{"x"}); return e }},
		{"LoadEmbeddings", func() error { _, e := a.LoadEmbeddings(ctx); return e }},
		{"SymbolsByIDs", func() error { _, e := a.SymbolsByIDs(ctx, ids); return e }},
		{"InboundEdgeCounts", func() error { _, e := a.InboundEdgeCounts(ctx, ids); return e }},
		{"CalleeIDs", func() error { _, e := a.CalleeIDs(ctx, ids); return e }},
		{"FilePathsByIDs", func() error { _, e := a.FilePathsByIDs(ctx, ids); return e }},
		{"ParentSymbols", func() error { _, e := a.ParentSymbols(ctx, ids); return e }},
		{"KeywordSearch", func() error { _, e := a.KeywordSearch(ctx, "x", "", 5); return e }},
		{"SubstringSearch", func() error { _, e := a.SubstringSearch(ctx, "x", "", 5); return e }},
	}
	for _, c := range checks {
		if err := c.call(); err == nil {
			t.Errorf("%s: expected error on closed adapter, got nil", c.name)
		}
	}
}

func TestSymbolsByIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	s2 := seedSymbol(t, a, fid, "Process", "pkg.Process", "function")

	// Query both
	result, err := a.SymbolsByIDs(ctx, []int64{s1, s2})
	if err != nil {
		t.Fatalf("SymbolsByIDs: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("SymbolsByIDs len = %d, want 2", len(result))
	}
	if result[s1].Name != "Order" {
		t.Errorf("result[s1].Name = %q, want Order", result[s1].Name)
	}
	if result[s2].Kind != "function" {
		t.Errorf("result[s2].Kind = %q, want function", result[s2].Kind)
	}

	// Empty input
	result, err = a.SymbolsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("SymbolsByIDs empty: %v", err)
	}
	if result != nil {
		t.Errorf("SymbolsByIDs empty = %v, want nil", result)
	}

	// Non-existent ID
	result, err = a.SymbolsByIDs(ctx, []int64{99999})
	if err != nil {
		t.Fatalf("SymbolsByIDs nonexistent: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("SymbolsByIDs nonexistent len = %d, want 0", len(result))
	}
}

func TestInboundEdgeCounts(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "edge.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "function")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "function")
	s3 := seedSymbol(t, a, fid, "C", "pkg.C", "function")

	line := 5
	// A calls B, A calls C, B calls C
	for _, e := range []struct{ src, tgt int64 }{
		{s1, s2}, {s1, s3}, {s2, s3},
	} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &e.src, TargetID: e.tgt, Kind: model.EdgeCalls,
			FileID: fid, Line: &line, Confidence: 1.0,
		}); err != nil {
			t.Fatalf("WriteEdge: %v", err)
		}
	}

	counts, err := a.InboundEdgeCounts(ctx, []int64{s1, s2, s3})
	if err != nil {
		t.Fatalf("InboundEdgeCounts: %v", err)
	}
	// s1 has 0 inbound, s2 has 1, s3 has 2
	if counts[s2] != 1 {
		t.Errorf("InboundEdgeCounts[s2] = %d, want 1", counts[s2])
	}
	if counts[s3] != 2 {
		t.Errorf("InboundEdgeCounts[s3] = %d, want 2", counts[s3])
	}
	if _, ok := counts[s1]; ok {
		t.Error("InboundEdgeCounts should not have entry for s1 (0 inbound)")
	}

	// Empty input
	counts, err = a.InboundEdgeCounts(ctx, nil)
	if err != nil {
		t.Fatalf("InboundEdgeCounts empty: %v", err)
	}
	if counts != nil {
		t.Errorf("InboundEdgeCounts empty = %v, want nil", counts)
	}
}

func TestCalleeIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "call.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "function")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "function")
	s3 := seedSymbol(t, a, fid, "C", "pkg.C", "function")

	line := 5
	// A calls B, A calls C
	for _, tgt := range []int64{s2, s3} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: &s1, TargetID: tgt, Kind: model.EdgeCalls,
			FileID: fid, Line: &line, Confidence: 1.0,
		}); err != nil {
			t.Fatalf("WriteEdge: %v", err)
		}
	}
	// Also add a non-calls edge to make sure it is filtered out
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: &s1, TargetID: s2, Kind: model.EdgeInherits,
		FileID: fid, Line: &line, Confidence: 1.0,
	}); err != nil {
		t.Fatalf("WriteEdge inherits: %v", err)
	}

	callees, err := a.CalleeIDs(ctx, []int64{s1})
	if err != nil {
		t.Fatalf("CalleeIDs: %v", err)
	}
	if len(callees[s1]) != 2 {
		t.Fatalf("CalleeIDs[s1] len = %d, want 2", len(callees[s1]))
	}

	// s2 has no outbound calls edges
	if len(callees[s2]) != 0 {
		t.Errorf("CalleeIDs[s2] len = %d, want 0", len(callees[s2]))
	}

	// Empty input
	callees, err = a.CalleeIDs(ctx, nil)
	if err != nil {
		t.Fatalf("CalleeIDs empty: %v", err)
	}
	if callees != nil {
		t.Errorf("CalleeIDs empty = %v, want nil", callees)
	}
}

func TestFilePathsByIDs(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid1 := seedFile(t, a, "src/main.go", "go", "h1")
	fid2 := seedFile(t, a, "src/util.go", "go", "h2")

	paths, err := a.FilePathsByIDs(ctx, []int64{fid1, fid2})
	if err != nil {
		t.Fatalf("FilePathsByIDs: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("FilePathsByIDs len = %d, want 2", len(paths))
	}
	if paths[fid1] != "src/main.go" {
		t.Errorf("paths[fid1] = %q, want src/main.go", paths[fid1])
	}
	if paths[fid2] != "src/util.go" {
		t.Errorf("paths[fid2] = %q, want src/util.go", paths[fid2])
	}

	// Empty input returns empty map (not nil)
	paths, err = a.FilePathsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("FilePathsByIDs empty: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("FilePathsByIDs empty len = %d, want 0", len(paths))
	}

	// Non-existent ID
	paths, err = a.FilePathsByIDs(ctx, []int64{99999})
	if err != nil {
		t.Fatalf("FilePathsByIDs nonexistent: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("FilePathsByIDs nonexistent len = %d, want 0", len(paths))
	}
}

func TestParentSymbols(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "model.go", "go", "h1")
	parentID := seedSymbol(t, a, fid, "Order", "pkg.Order", "class")
	childID := seedSymbolWithParent(t, a, fid, "Process", "pkg.Order.Process", "method", parentID)
	orphanID := seedSymbol(t, a, fid, "Helper", "pkg.Helper", "function")

	parents, err := a.ParentSymbols(ctx, []int64{childID, orphanID})
	if err != nil {
		t.Fatalf("ParentSymbols: %v", err)
	}
	// childID has a parent, orphanID does not
	pi, ok := parents[childID]
	if !ok {
		t.Fatal("ParentSymbols missing entry for childID")
	}
	if pi.Name != "Order" {
		t.Errorf("parent name = %q, want Order", pi.Name)
	}
	if pi.Qualified != "pkg.Order" {
		t.Errorf("parent qualified = %q, want pkg.Order", pi.Qualified)
	}
	if pi.Kind != "class" {
		t.Errorf("parent kind = %q, want class", pi.Kind)
	}
	if _, ok := parents[orphanID]; ok {
		t.Error("orphan should not have a parent entry")
	}

	// Empty input
	parents, err = a.ParentSymbols(ctx, nil)
	if err != nil {
		t.Fatalf("ParentSymbols empty: %v", err)
	}
	if parents != nil {
		t.Errorf("ParentSymbols empty = %v, want nil", parents)
	}
}

func TestLoadEmbeddings(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	fid := seedFile(t, a, "app.go", "go", "h1")
	s1 := seedSymbol(t, a, fid, "A", "pkg.A", "class")
	s2 := seedSymbol(t, a, fid, "B", "pkg.B", "class")

	// Write embeddings as float32 vectors encoded as bytes
	vec1 := floatVec(1.0, 2.0, 3.0)
	vec2 := floatVec(4.0, 5.0, 6.0)
	if err := a.WriteEmbedding(ctx, s1, vec1); err != nil {
		t.Fatalf("WriteEmbedding s1: %v", err)
	}
	if err := a.WriteEmbedding(ctx, s2, vec2); err != nil {
		t.Fatalf("WriteEmbedding s2: %v", err)
	}

	result, err := a.LoadEmbeddings(ctx)
	if err != nil {
		t.Fatalf("LoadEmbeddings: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("LoadEmbeddings len = %d, want 2", len(result))
	}
	if len(result[s1]) != 3 {
		t.Errorf("result[s1] len = %d, want 3", len(result[s1]))
	}
}

func TestSymbolCountEmptyAndPopulated(t *testing.T) {
	a := openTestDB(t)
	ctx := context.Background()

	count, err := a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount empty: %v", err)
	}
	if count != 0 {
		t.Errorf("SymbolCount empty = %d, want 0", count)
	}

	fid := seedFile(t, a, "app.go", "go", "h1")
	seedSymbol(t, a, fid, "A", "pkg.A", "class")
	seedSymbol(t, a, fid, "B", "pkg.B", "function")

	count, err = a.SymbolCount(ctx)
	if err != nil {
		t.Fatalf("SymbolCount: %v", err)
	}
	if count != 2 {
		t.Errorf("SymbolCount = %d, want 2", count)
	}
}
