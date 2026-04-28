package profile

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
)

const (
	TierSmall  = "small"
	TierMedium = "medium"
	TierLarge  = "large"
)

const (
	smallThreshold         = 3000
	largeDynamicThreshold  = 15000
	largeStaticThreshold   = 20000
)

var dynamicLanguages = map[string]bool{
	"ruby":   true,
	"python": true,
}

type Profile struct {
	Tier        string  `json:"tier"`
	Symbols     int     `json:"symbols"`
	Edges       int     `json:"edges"`
	Density     float64 `json:"density"`
	PrimaryLang string  `json:"primary_language"`
	DynamicLang bool    `json:"dynamic_language"`
}

func Compute(ctx context.Context, db *sql.DB) (*Profile, error) {
	var symbols int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&symbols); err != nil {
		return nil, fmt.Errorf("profile: count symbols: %w", err)
	}

	var edges int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges`).Scan(&edges); err != nil {
		return nil, fmt.Errorf("profile: count edges: %w", err)
	}

	langSymbols, err := queryLangSymbolCounts(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("profile: language breakdown: %w", err)
	}

	primaryLang := primaryLanguage(langSymbols)
	dynamicCount := 0
	for lang, count := range langSymbols {
		if dynamicLanguages[lang] {
			dynamicCount += count
		}
	}
	isDynamic := symbols > 0 && dynamicCount*2 > symbols

	var density float64
	if symbols > 0 {
		density = float64(edges) / float64(symbols)
	}

	tier := computeTier(symbols, isDynamic)

	return &Profile{
		Tier:        tier,
		Symbols:     symbols,
		Edges:       edges,
		Density:     density,
		PrimaryLang: primaryLang,
		DynamicLang: isDynamic,
	}, nil
}

func computeTier(symbols int, dynamic bool) string {
	if symbols < smallThreshold {
		return TierSmall
	}
	threshold := largeStaticThreshold
	if dynamic {
		threshold = largeDynamicThreshold
	}
	if symbols >= threshold {
		return TierLarge
	}
	return TierMedium
}

func queryLangSymbolCounts(ctx context.Context, db *sql.DB) (map[string]int, error) {
	const q = `SELECT f.language, COUNT(s.id)
	           FROM sense_files f
	           JOIN sense_symbols s ON s.file_id = f.id
	           GROUP BY f.language`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]int)
	for rows.Next() {
		var lang string
		var count int
		if err := rows.Scan(&lang, &count); err != nil {
			return nil, err
		}
		out[lang] = count
	}
	return out, rows.Err()
}

func primaryLanguage(langSymbols map[string]int) string {
	var best string
	var bestCount int
	for lang, count := range langSymbols {
		if count > bestCount {
			best = lang
			bestCount = count
		}
	}
	return best
}

func Store(ctx context.Context, db *sql.DB, p *Profile) error {
	const q = `INSERT INTO sense_meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`
	pairs := []struct{ k, v string }{
		{"profile_tier", p.Tier},
		{"profile_symbols", fmt.Sprintf("%d", p.Symbols)},
		{"profile_edges", fmt.Sprintf("%d", p.Edges)},
		{"profile_density", fmt.Sprintf("%.4f", p.Density)},
		{"profile_primary_lang", p.PrimaryLang},
		{"profile_dynamic", fmt.Sprintf("%t", p.DynamicLang)},
	}
	for _, kv := range pairs {
		if _, err := db.ExecContext(ctx, q, kv.k, kv.v); err != nil {
			return fmt.Errorf("profile: store %s: %w", kv.k, err)
		}
	}
	return nil
}

func Load(ctx context.Context, db *sql.DB) *Profile {
	tier := readMeta(ctx, db, "profile_tier")
	if tier == "" {
		return nil
	}
	return &Profile{
		Tier:        tier,
		Symbols:     readMetaInt(ctx, db, "profile_symbols"),
		PrimaryLang: readMeta(ctx, db, "profile_primary_lang"),
		DynamicLang: readMeta(ctx, db, "profile_dynamic") == "true",
	}
}

func readMeta(ctx context.Context, db *sql.DB, key string) string {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM sense_meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

func readMetaInt(ctx context.Context, db *sql.DB, key string) int {
	s := readMeta(ctx, db, key)
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

type Defaults struct {
	SearchKeywordWeight    float64
	SearchVectorWeight     float64
	ConventionsMinStrength float64
	ConventionsInstanceCap int
	ConventionsTokenBudget int
	BlastMaxHops           int
	BlastMinConfidence     float64
	BlastResultCap         int
}

func DefaultsForTier(tier string) Defaults {
	switch tier {
	case TierSmall:
		return Defaults{
			SearchKeywordWeight:    0.5,
			SearchVectorWeight:     0.5,
			ConventionsMinStrength: 0.15,
			ConventionsInstanceCap: 5,
			ConventionsTokenBudget: 6000,
			BlastMaxHops:           5,
			BlastMinConfidence:     0.3,
			BlastResultCap:         200,
		}
	case TierLarge:
		return Defaults{
			SearchKeywordWeight:    0.6,
			SearchVectorWeight:     0.4,
			ConventionsMinStrength: 0.35,
			ConventionsInstanceCap: 3,
			ConventionsTokenBudget: 4000,
			BlastMaxHops:           2,
			BlastMinConfidence:     0.6,
			BlastResultCap:         75,
		}
	default:
		return Defaults{
			SearchKeywordWeight:    0.5,
			SearchVectorWeight:     0.5,
			ConventionsMinStrength: 0.30,
			ConventionsInstanceCap: 3,
			ConventionsTokenBudget: 4000,
			BlastMaxHops:           3,
			BlastMinConfidence:     0.5,
			BlastResultCap:         100,
		}
	}
}
