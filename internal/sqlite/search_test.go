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

func seedSearchIndex(t *testing.T, ctx context.Context, a *sqlite.Adapter) {
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

	seedSearchIndex(t, ctx, a)

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

	seedSearchIndex(t, ctx, a)

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

	seedSearchIndex(t, ctx, a)

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

	seedSearchIndex(t, ctx, a)

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
