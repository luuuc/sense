package scan

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestImplSibling pins the test-association naming conventions for
// every supported language. Internal test because `implSibling` is
// unexported and its per-language branches are the kind of thing
// that can silently rot when someone adds a new suffix or language
// without updating the matcher.
func TestImplSibling(t *testing.T) {
	cases := []struct {
		path     string
		language string
		wantImpl string
		wantOK   bool
	}{
		// Ruby: _test.rb and _spec.rb both map to the .rb sibling.
		{"app/user_test.rb", "ruby", "app/user.rb", true},
		{"app/user_spec.rb", "ruby", "app/user.rb", true},
		{"app/user.rb", "ruby", "", false},

		// Python: test_ prefix maps to the stripped .py.
		{"pkg/test_user.py", "python", "pkg/user.py", true},
		{"pkg/user_test.py", "python", "", false}, // pytest uses prefix, not suffix
		{"pkg/user.py", "python", "", false},

		// Go: _test.go is the sole convention.
		{"widget/widget_test.go", "go", "widget/widget.go", true},
		{"widget/widget.go", "go", "", false},

		// TypeScript: .test.ts and .spec.ts map to .ts. Note the
		// typescript extractor's scope — .tsx files arrive as
		// language "tsx", not "typescript".
		{"src/foo.test.ts", "typescript", "src/foo.ts", true},
		{"src/foo.spec.ts", "typescript", "src/foo.ts", true},
		{"src/foo.ts", "typescript", "", false},

		// TSX: language "tsx" has its own branch.
		{"src/foo.test.tsx", "tsx", "src/foo.tsx", true},
		{"src/foo.spec.tsx", "tsx", "src/foo.tsx", true},

		// JavaScript: .test.js / .spec.js → .js, plus jsx variants.
		{"src/foo.test.js", "javascript", "src/foo.js", true},
		{"src/foo.spec.js", "javascript", "src/foo.js", true},
		{"src/foo.test.jsx", "javascript", "src/foo.jsx", true},

		// Negative: unknown language, untracked file kind.
		{"notes.txt", "plaintext", "", false},
		{"app/user_test.rb", "python", "", false}, // convention-matches-suffix but language mismatches
	}

	for _, c := range cases {
		gotImpl, gotOK := implSibling(c.path, c.language)
		if gotOK != c.wantOK {
			t.Errorf("implSibling(%q, %q) ok = %v, want %v", c.path, c.language, gotOK, c.wantOK)
			continue
		}
		if gotImpl != c.wantImpl {
			t.Errorf("implSibling(%q, %q) impl = %q, want %q", c.path, c.language, gotImpl, c.wantImpl)
		}
	}
}

func TestMirrorImpl(t *testing.T) {
	cases := []struct {
		path     string
		language string
		want     []string
	}{
		{"spec/models/user_spec.rb", "ruby", []string{"app/models/user.rb"}},
		{"spec/controllers/users_controller_spec.rb", "ruby", []string{"app/controllers/users_controller.rb"}},
		{"test/models/user_test.rb", "ruby", []string{"app/models/user.rb"}},
		{"spec/user_spec.rb", "ruby", []string{"app/user.rb"}},
		{"test/user_test.rb", "ruby", []string{"app/user.rb"}},

		// Not under spec/ or test/ — no mirror.
		{"app/user_spec.rb", "ruby", nil},
		// Not Ruby — no mirror.
		{"spec/models/user_spec.rb", "go", nil},
		// Not a test file.
		{"spec/models/user.rb", "ruby", nil},
	}

	for _, c := range cases {
		got := mirrorImpl(c.path, c.language)
		if len(got) != len(c.want) {
			t.Errorf("mirrorImpl(%q, %q) = %v, want %v", c.path, c.language, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("mirrorImpl(%q, %q)[%d] = %q, want %q", c.path, c.language, i, got[i], c.want[i])
			}
		}
	}
}

func TestMigrateEmbeddingModel(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "index.db")

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	senseDir := filepath.Join(tmp, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	h := &harness{ctx: ctx, idx: adapter, root: tmp, out: io.Discard, warn: io.Discard}

	// Case 1: No stored model → no migration
	migrated, err := h.migrateEmbeddingModel()
	if err != nil {
		t.Fatalf("migrate with no stored model: %v", err)
	}
	if migrated {
		t.Error("expected no migration when no stored model")
	}

	// Case 2: Stored model matches current → no migration
	if err := adapter.WriteMeta(ctx, "embedding_model", embed.ModelID); err != nil {
		t.Fatalf("write meta: %v", err)
	}
	migrated, err = h.migrateEmbeddingModel()
	if err != nil {
		t.Fatalf("migrate with matching model: %v", err)
	}
	if migrated {
		t.Error("expected no migration when stored model matches current")
	}

	// Case 3: Stored model differs → migration occurs
	// Insert a fake embedding so we can verify it's cleared
	if err := adapter.WriteMeta(ctx, "embedding_model", "old-model-v1"); err != nil {
		t.Fatalf("write meta: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Insert a fake symbol first (required by FK constraint)
	_, err = db.Exec("INSERT INTO sense_files (path, language, hash, symbols, indexed_at) VALUES (?, ?, ?, ?, ?)",
		"test.go", "go", "abc", 1, "2024-01-01")
	if err != nil {
		t.Fatalf("insert file: %v", err)
	}

	// Insert a fake embedding
	_, err = db.Exec("INSERT INTO sense_embeddings (symbol_id, vector) VALUES (?, ?)", 1, []byte{0x01, 0x02})
	if err != nil {
		t.Fatalf("insert embedding: %v", err)
	}

	migrated, err = h.migrateEmbeddingModel()
	if err != nil {
		t.Fatalf("migrate with mismatched model: %v", err)
	}
	if !migrated {
		t.Fatal("expected migration when stored model differs")
	}

	// Verify embeddings cleared
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sense_embeddings").Scan(&count); err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	if count != 0 {
		t.Errorf("embeddings count = %d, want 0", count)
	}

	// Verify meta deleted
	var meta string
	err = db.QueryRow("SELECT value FROM sense_meta WHERE key = ?", "embedding_model").Scan(&meta)
	if err == nil {
		t.Error("expected embedding_model meta to be deleted")
	}
}

func TestRepresentativeTestSymbol(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		_, ok := representativeTestSymbol(nil)
		if ok {
			t.Error("expected false for nil slice")
		}
	})

	t.Run("picks earliest line", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 10, Name: "TestB", LineStart: 20},
			{ID: 5, Name: "TestA", LineStart: 5},
			{ID: 8, Name: "TestC", LineStart: 15},
		}
		id, ok := representativeTestSymbol(symbols)
		if !ok {
			t.Fatal("expected true")
		}
		if id != 5 {
			t.Errorf("got ID %d, want 5 (TestA at line 5)", id)
		}
	})

	t.Run("ties broken by ID", func(t *testing.T) {
		symbols := []model.Symbol{
			{ID: 10, Name: "TestB", LineStart: 1},
			{ID: 3, Name: "TestA", LineStart: 1},
		}
		id, ok := representativeTestSymbol(symbols)
		if !ok {
			t.Fatal("expected true")
		}
		if id != 3 {
			t.Errorf("got ID %d, want 3 (lower ID tie-break)", id)
		}
	})
}
