package summary

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// newAdapter opens a fresh adapter (full schema) on a temp DB and registers cleanup.
func newAdapter(t *testing.T) (*sqlite.Adapter, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	adapter, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = adapter.Close() })
	return adapter, dbPath
}

// emptyDB returns a *sql.DB pointing at a brand-new database with no sense_* schema.
func emptyDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "empty.db"))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type fileSpec struct {
	path string
	lang string
}

// seedFilesDB writes one symbol per file spec and returns a read-only *sql.DB.
func seedFilesDB(t *testing.T, specs []fileSpec) *sql.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	now := time.Now()
	for i, s := range specs {
		fid, err := adapter.WriteFile(ctx, &model.File{
			Path: s.path, Language: s.lang,
			Hash: string(rune('a' + i)), Symbols: 1, IndexedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adapter.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: "S", Qualified: "x.S",
			Kind: model.KindFunction, LineStart: 1, LineEnd: 2,
		}); err != nil {
			t.Fatal(err)
		}
	}
	_ = adapter.Close()

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// --- queryFrameworks success path + fingerprint frameworks branch ---

func TestRenderFingerprintWithFrameworks(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	if _, err := db.Exec(`INSERT INTO sense_meta(key, value) VALUES('frameworks', '["Rails","Sidekiq"]')`); err != nil {
		t.Fatalf("insert frameworks meta: %v", err)
	}

	got, err := renderFingerprint(context.Background(), db)
	if err != nil {
		t.Fatalf("renderFingerprint: %v", err)
	}
	if !strings.Contains(got, "(Rails, Sidekiq)") {
		t.Errorf("expected frameworks in fingerprint, got: %s", got)
	}
}

func TestQueryFrameworks(t *testing.T) {
	db, cleanup := seedTestDB(t)
	defer cleanup()

	// No frameworks meta yet -> nil.
	if got := queryFrameworks(context.Background(), db); got != nil {
		t.Errorf("expected nil frameworks, got: %v", got)
	}

	if _, err := db.Exec(`INSERT INTO sense_meta(key, value) VALUES('frameworks', '["Django"]')`); err != nil {
		t.Fatalf("insert: %v", err)
	}
	got := queryFrameworks(context.Background(), db)
	if len(got) != 1 || got[0] != "Django" {
		t.Errorf("expected [Django], got: %v", got)
	}

	// Invalid JSON -> nil.
	if _, err := db.Exec(`UPDATE sense_meta SET value = 'not-json' WHERE key = 'frameworks'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := queryFrameworks(context.Background(), db); got != nil {
		t.Errorf("expected nil for invalid json, got: %v", got)
	}
}

// --- renderProject edge cases ---

func TestRenderProjectBreaksOnHeadingAfterParagraph(t *testing.T) {
	root := t.TempDir()
	content := "# Title\n\nFirst para line.\n## Section heading\n\nMore text.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := renderProject(root); got != "First para line." {
		t.Errorf("renderProject() = %q, want %q", got, "First para line.")
	}
}

func TestRenderProjectCapsReadmeLines(t *testing.T) {
	root := t.TempDir()
	// Many lines (>maxReadmeLines) with the description near the top.
	content := "# Title\n\nThe description.\n" + strings.Repeat("\n", 250)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := renderProject(root); got != "The description." {
		t.Errorf("renderProject() = %q, want %q", got, "The description.")
	}
}

// --- structured description truncation paths ---

func TestReadStructuredDescriptionTruncates(t *testing.T) {
	long := strings.Repeat("x", 400)

	t.Run("package.json", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "package.json"),
			[]byte(`{"description":"`+long+`"}`), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readStructuredDescription(root)
		if !strings.HasSuffix(got, "...") || len(got) > maxDescBytes {
			t.Errorf("expected truncated package.json desc, got len %d: %q", len(got), got)
		}
	})

	t.Run("Cargo.toml", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "Cargo.toml"),
			[]byte("[package]\ndescription = \""+long+"\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readStructuredDescription(root)
		if !strings.HasSuffix(got, "...") || len(got) > maxDescBytes {
			t.Errorf("expected truncated Cargo.toml desc, got len %d: %q", len(got), got)
		}
	})

	t.Run("setup.cfg", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "setup.cfg"),
			[]byte("[metadata]\ndescription = "+long+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got := readStructuredDescription(root)
		if !strings.HasSuffix(got, "...") || len(got) > maxDescBytes {
			t.Errorf("expected truncated setup.cfg desc, got len %d: %q", len(got), got)
		}
	})
}

// --- renderMainAreas truncates to 8 areas ---

func TestRenderMainAreasCapsAtEight(t *testing.T) {
	specs := make([]fileSpec, 0, 12)
	for i := 0; i < 12; i++ {
		specs = append(specs, fileSpec{path: string(rune('a'+i)) + "dir/file.go", lang: "go"})
	}
	db := seedFilesDB(t, specs)

	got, err := renderMainAreas(context.Background(), db)
	if err != nil {
		t.Fatalf("renderMainAreas: %v", err)
	}
	// Header line + at most 8 bullets.
	bullets := 0
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.HasPrefix(line, "- `") {
			bullets++
		}
	}
	if bullets != 8 {
		t.Errorf("expected exactly 8 area bullets, got %d:\n%s", bullets, got)
	}
}

// --- renderKnownNoise dedup when two patterns share a prefix ---

func TestRenderKnownNoiseDedupsPrefix(t *testing.T) {
	// "fixtures" matches both %testdata% (no) — use a path matching two patterns:
	// "pkg/testdata/fixtures.go" matches %testdata% and %fixture%, same dir prefix.
	db := seedFilesDB(t, []fileSpec{
		{path: "pkg/testdata/fixtures.go", lang: "go"},
	})
	got, err := renderKnownNoise(context.Background(), db)
	if err != nil {
		t.Fatalf("renderKnownNoise: %v", err)
	}
	// The shared prefix must appear only once.
	if c := strings.Count(got, "`pkg/testdata/`"); c != 1 {
		t.Errorf("expected prefix listed once, got %d:\n%s", c, got)
	}
}

// --- longestNoisePrefix paths ---

func TestLongestNoisePrefix(t *testing.T) {
	t.Run("common prefix across multiple files", func(t *testing.T) {
		db := seedFilesDB(t, []fileSpec{
			{path: "a/testdata/x.go", lang: "go"},
			{path: "a/testdata/y.go", lang: "go"},
		})
		// commonPrefix strips the trailing path segment even for identical dirs,
		// so two files in a/testdata reduce to "a/".
		got := longestNoisePrefix(context.Background(), db, "%testdata%")
		if got != "a/" {
			t.Errorf("got %q, want %q", got, "a/")
		}
	})

	t.Run("no common prefix falls back to pattern", func(t *testing.T) {
		db := seedFilesDB(t, []fileSpec{
			{path: "x/testdata/a.go", lang: "go"},
			{path: "y/testdata/b.go", lang: "go"},
		})
		got := longestNoisePrefix(context.Background(), db, "%testdata%")
		if got != "testdata" {
			t.Errorf("got %q, want fallback %q", got, "testdata")
		}
	})

	t.Run("no matches falls back to pattern", func(t *testing.T) {
		db := seedFilesDB(t, []fileSpec{
			{path: "src/app.go", lang: "go"},
		})
		got := longestNoisePrefix(context.Background(), db, "%vendor%")
		if got != "vendor" {
			t.Errorf("got %q, want fallback %q", got, "vendor")
		}
	})

	t.Run("query error falls back to pattern", func(t *testing.T) {
		got := longestNoisePrefix(context.Background(), emptyDB(t), "%vendor%")
		if got != "vendor" {
			t.Errorf("got %q, want fallback %q", got, "vendor")
		}
	})
}

// --- renderTestStructure truncates to 3 dirs ---

func TestRenderTestStructureCapsDirs(t *testing.T) {
	db := seedFilesDB(t, []fileSpec{
		{path: "a/foo_test.go", lang: "go"},
		{path: "b/foo_test.go", lang: "go"},
		{path: "c/foo_test.go", lang: "go"},
		{path: "d/foo_test.go", lang: "go"},
	})
	got := renderTestStructure(context.Background(), db)
	if !strings.Contains(got, "4 test files") {
		t.Errorf("expected 4 test files, got: %s", got)
	}
	// Only 3 dirs should be listed (a, b, c after sort).
	if strings.Contains(got, "d") {
		t.Errorf("expected dirs capped at 3, got: %s", got)
	}
}

// --- degraded DB: missing schema drives query-error branches ---

func TestRenderFunctionsOnEmptyDB(t *testing.T) {
	ctx := context.Background()
	db := emptyDB(t)

	if _, err := renderFingerprint(ctx, db); err == nil {
		t.Error("renderFingerprint expected error on schemaless db")
	}
	if _, err := renderMainAreas(ctx, db); err == nil {
		t.Error("renderMainAreas expected error on schemaless db")
	}
	if _, err := renderReadingPath(ctx, db); err == nil {
		t.Error("renderReadingPath expected error on schemaless db")
	}
	if _, err := renderKnownNoise(ctx, db); err == nil {
		t.Error("renderKnownNoise expected error on schemaless db")
	}
	if _, err := queryLanguages(ctx, db); err == nil {
		t.Error("queryLanguages expected error on schemaless db")
	}
	// These swallow errors and return zero values.
	if got := queryStrings(ctx, db, "SELECT path FROM sense_files"); got != nil {
		t.Errorf("queryStrings expected nil on schemaless db, got: %v", got)
	}
	if got := queryFrameworks(ctx, db); got != nil {
		t.Errorf("queryFrameworks expected nil on schemaless db, got: %v", got)
	}
	if got := renderEntryPoints(ctx, db); got != "" {
		t.Errorf("renderEntryPoints expected empty on schemaless db, got: %q", got)
	}
	if got := renderHubSymbols(ctx, db); got != "" {
		t.Errorf("renderHubSymbols expected empty on schemaless db, got: %q", got)
	}
	if got := renderTestStructure(ctx, db); got != "" {
		t.Errorf("renderTestStructure expected empty on schemaless db, got: %q", got)
	}
	if _, err := renderQuickOrientation(ctx, db); err != nil {
		t.Errorf("renderQuickOrientation should not error, got: %v", err)
	}
}

// renderFingerprint propagates errors from each successive count query.
func TestRenderFingerprintMissingTables(t *testing.T) {
	ctx := context.Background()

	t.Run("missing sense_symbols", func(t *testing.T) {
		adapter, _ := newAdapter(t)
		if _, err := adapter.DB().ExecContext(ctx, "DROP TABLE sense_symbols"); err != nil {
			t.Fatal(err)
		}
		if _, err := renderFingerprint(ctx, adapter.DB()); err == nil {
			t.Error("expected error when sense_symbols missing")
		}
	})

	t.Run("missing sense_edges", func(t *testing.T) {
		adapter, _ := newAdapter(t)
		if _, err := adapter.DB().ExecContext(ctx, "DROP TABLE sense_edges"); err != nil {
			t.Fatal(err)
		}
		if _, err := renderFingerprint(ctx, adapter.DB()); err == nil {
			t.Error("expected error when sense_edges missing")
		}
	})
}

// renderKeyAbstractions propagates TopSymbolsByReach errors.
func TestRenderKeyAbstractionsError(t *testing.T) {
	ctx := context.Background()
	adapter, _ := newAdapter(t)
	if _, err := adapter.DB().ExecContext(ctx, "DROP TABLE sense_edges"); err != nil {
		t.Fatal(err)
	}
	if _, err := renderKeyAbstractions(ctx, adapter); err == nil {
		t.Error("expected error when underlying query fails")
	}
}
