package profile

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestComputeTier(t *testing.T) {
	tests := []struct {
		symbols int
		dynamic bool
		want    string
	}{
		{0, false, TierSmall},
		{500, false, TierSmall},
		{2999, false, TierSmall},
		{3000, false, TierMedium},
		{5000, false, TierMedium},
		{15000, false, TierMedium},
		{19999, false, TierMedium},
		{20000, false, TierLarge},
		{50000, false, TierLarge},
		// Dynamic language shift: large threshold drops to 15,000.
		{14999, true, TierMedium},
		{15000, true, TierLarge},
		{20000, true, TierLarge},
	}
	for _, tt := range tests {
		name := fmt.Sprintf("%d_dynamic=%v", tt.symbols, tt.dynamic)
		t.Run(name, func(t *testing.T) {
			got := computeTier(tt.symbols, tt.dynamic)
			if got != tt.want {
				t.Errorf("computeTier(%d, %v) = %q, want %q", tt.symbols, tt.dynamic, got, tt.want)
			}
		})
	}
}

func TestDefaultsForTier(t *testing.T) {
	small := DefaultsForTier(TierSmall)
	medium := DefaultsForTier(TierMedium)
	large := DefaultsForTier(TierLarge)

	if small.BlastMaxHops <= medium.BlastMaxHops {
		t.Errorf("small BlastMaxHops (%d) should exceed medium (%d)", small.BlastMaxHops, medium.BlastMaxHops)
	}
	if medium.BlastMaxHops <= large.BlastMaxHops {
		t.Errorf("medium BlastMaxHops (%d) should exceed large (%d)", medium.BlastMaxHops, large.BlastMaxHops)
	}
	if small.BlastMinConfidence >= medium.BlastMinConfidence {
		t.Errorf("small BlastMinConfidence (%.2f) should be less than medium (%.2f)", small.BlastMinConfidence, medium.BlastMinConfidence)
	}
	if small.BlastResultCap <= large.BlastResultCap {
		t.Errorf("small BlastResultCap (%d) should exceed large (%d)", small.BlastResultCap, large.BlastResultCap)
	}
	if small.ConventionsMinStrength >= large.ConventionsMinStrength {
		t.Errorf("small ConventionsMinStrength (%.2f) should be less than large (%.2f)", small.ConventionsMinStrength, large.ConventionsMinStrength)
	}
	if small.ConventionsTokenBudget <= large.ConventionsTokenBudget {
		t.Errorf("small ConventionsTokenBudget (%d) should exceed large (%d)", small.ConventionsTokenBudget, large.ConventionsTokenBudget)
	}
	if large.SearchKeywordWeight <= medium.SearchKeywordWeight {
		t.Errorf("large SearchKeywordWeight (%.2f) should exceed medium (%.2f)", large.SearchKeywordWeight, medium.SearchKeywordWeight)
	}

	// Unknown tier falls back to medium defaults.
	unknown := DefaultsForTier("unknown")
	if unknown != medium {
		t.Errorf("unknown tier should return medium defaults")
	}
}

func TestDefaultsForTierValues(t *testing.T) {
	large := DefaultsForTier(TierLarge)
	if large.BlastMaxHops != 2 {
		t.Errorf("large BlastMaxHops = %d, want 2", large.BlastMaxHops)
	}
	if large.BlastMinConfidence != 0.7 {
		t.Errorf("large BlastMinConfidence = %.2f, want 0.70", large.BlastMinConfidence)
	}
	if large.BlastResultCap != 50 {
		t.Errorf("large BlastResultCap = %d, want 50", large.BlastResultCap)
	}
	if large.ConventionsMinStrength != 0.40 {
		t.Errorf("large ConventionsMinStrength = %.2f, want 0.40", large.ConventionsMinStrength)
	}
	if large.ConventionsTokenBudget != 3000 {
		t.Errorf("large ConventionsTokenBudget = %d, want 3000", large.ConventionsTokenBudget)
	}
}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
		CREATE TABLE sense_files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL,
			language TEXT NOT NULL,
			hash TEXT NOT NULL DEFAULT '',
			symbols INTEGER NOT NULL DEFAULT 0,
			indexed_at TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE sense_symbols (
			id INTEGER PRIMARY KEY,
			file_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			qualified TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT '',
			line_start INTEGER NOT NULL DEFAULT 0,
			line_end INTEGER NOT NULL DEFAULT 0,
			snippet TEXT NOT NULL DEFAULT ''
		);
		CREATE TABLE sense_edges (
			id INTEGER PRIMARY KEY,
			source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			confidence REAL NOT NULL DEFAULT 1.0
		);
		CREATE TABLE sense_meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

func populateFixture(t *testing.T, db *sql.DB, lang string, symbolCount int) {
	t.Helper()
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	fileCount := symbolCount / 10
	if fileCount == 0 {
		fileCount = 1
	}
	for i := 0; i < fileCount; i++ {
		_, err := tx.ExecContext(ctx, `INSERT INTO sense_files (path, language, hash) VALUES (?, ?, ?)`,
			fmt.Sprintf("file_%d.go", i), lang, fmt.Sprintf("h%d", i))
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < symbolCount; i++ {
		fileID := (i % fileCount) + 1
		_, err := tx.ExecContext(ctx, `INSERT INTO sense_symbols (file_id, name, qualified, kind) VALUES (?, ?, ?, ?)`,
			fileID, fmt.Sprintf("Sym%d", i), fmt.Sprintf("pkg.Sym%d", i), "function")
		if err != nil {
			t.Fatal(err)
		}
	}
	edgeCount := symbolCount * 2
	for i := 0; i < edgeCount; i++ {
		src := (i % symbolCount) + 1
		tgt := ((i + 1) % symbolCount) + 1
		_, err := tx.ExecContext(ctx, `INSERT INTO sense_edges (source_id, target_id, kind) VALUES (?, ?, ?)`,
			src, tgt, "calls")
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestComputeSmallRepo(t *testing.T) {
	db := openTestDB(t)
	populateFixture(t, db, "go", 500)

	prof, err := Compute(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Tier != TierSmall {
		t.Errorf("tier = %q, want %q", prof.Tier, TierSmall)
	}
	if prof.Symbols != 500 {
		t.Errorf("symbols = %d, want 500", prof.Symbols)
	}
	if prof.PrimaryLang != "go" {
		t.Errorf("primary_lang = %q, want %q", prof.PrimaryLang, "go")
	}
	if prof.DynamicLang {
		t.Error("dynamic_lang should be false for go")
	}
}

func TestComputeMediumRepo(t *testing.T) {
	db := openTestDB(t)
	populateFixture(t, db, "go", 5000)

	prof, err := Compute(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Tier != TierMedium {
		t.Errorf("tier = %q, want %q", prof.Tier, TierMedium)
	}
}

func TestComputeLargeRepo(t *testing.T) {
	db := openTestDB(t)
	populateFixture(t, db, "go", 25000)

	prof, err := Compute(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Tier != TierLarge {
		t.Errorf("tier = %q, want %q", prof.Tier, TierLarge)
	}
}

func TestComputeDynamicLanguageShift(t *testing.T) {
	db := openTestDB(t)
	// 15,000 Ruby symbols → dynamic language shift makes this "large"
	// instead of "medium" (static threshold is 20,000).
	populateFixture(t, db, "ruby", 15000)

	prof, err := Compute(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if prof.Tier != TierLarge {
		t.Errorf("tier = %q, want %q (dynamic language shift)", prof.Tier, TierLarge)
	}
	if !prof.DynamicLang {
		t.Error("dynamic_lang should be true for ruby-dominant project")
	}
	if prof.PrimaryLang != "ruby" {
		t.Errorf("primary_lang = %q, want %q", prof.PrimaryLang, "ruby")
	}
}

func TestStoreAndLoad(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	original := &Profile{
		Tier:        TierLarge,
		Symbols:     28431,
		Edges:       52000,
		Density:     1.8292,
		PrimaryLang: "ruby",
		DynamicLang: true,
	}

	if err := Store(ctx, db, original); err != nil {
		t.Fatal(err)
	}

	loaded := Load(ctx, db)
	if loaded == nil {
		t.Fatal("Load returned nil")
	}
	if loaded.Tier != original.Tier {
		t.Errorf("tier = %q, want %q", loaded.Tier, original.Tier)
	}
	if loaded.Symbols != original.Symbols {
		t.Errorf("symbols = %d, want %d", loaded.Symbols, original.Symbols)
	}
	if loaded.PrimaryLang != original.PrimaryLang {
		t.Errorf("primary_lang = %q, want %q", loaded.PrimaryLang, original.PrimaryLang)
	}
	if loaded.DynamicLang != original.DynamicLang {
		t.Errorf("dynamic_lang = %v, want %v", loaded.DynamicLang, original.DynamicLang)
	}
}

func TestLoadReturnsNilWhenNoProfile(t *testing.T) {
	db := openTestDB(t)
	loaded := Load(context.Background(), db)
	if loaded != nil {
		t.Errorf("Load should return nil when no profile stored, got %+v", loaded)
	}
}

func TestPrimaryLanguage(t *testing.T) {
	tests := []struct {
		name  string
		langs map[string]int
		want  string
	}{
		{"single", map[string]int{"go": 100}, "go"},
		{"go dominant", map[string]int{"go": 500, "ruby": 100}, "go"},
		{"ruby dominant", map[string]int{"go": 100, "ruby": 500}, "ruby"},
		{"empty", map[string]int{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := primaryLanguage(tt.langs)
			if got != tt.want {
				t.Errorf("primaryLanguage = %q, want %q", got, tt.want)
			}
		})
	}
}
