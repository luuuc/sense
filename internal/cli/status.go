package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"

	_ "modernc.org/sqlite"
)

const statusHelp = `usage: sense status [flags]

Show index health: file/symbol/edge counts, per-language breakdown,
coverage, freshness, and version info.

Flags:
  --json       Emit JSON matching the sense.status MCP schema
  -h, --help   Show this help

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
`

// RunStatus prints index health and embedding coverage.
func RunStatus(args []string, cio IO) int {
	fs := flag.NewFlagSet("sense status", flag.ContinueOnError)
	fs.SetOutput(cio.Stderr)
	jsonFlag := fs.Bool("json", false, "")
	fs.Usage = func() { _, _ = fmt.Fprint(cio.Stderr, statusHelp) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}

	ctx := context.Background()
	resp, exitCode := buildCLIStatusResponse(ctx, cio)
	if exitCode != ExitSuccess {
		return exitCode
	}

	if *jsonFlag {
		out, err := mcpio.MarshalStatus(resp)
		if err != nil {
			_, _ = fmt.Fprintf(cio.Stderr, "sense status: %v\n", err)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}

	renderStatusHuman(cio, resp)
	return ExitSuccess
}

func buildCLIStatusResponse(ctx context.Context, cio IO) (mcpio.StatusResponse, int) {
	var resp mcpio.StatusResponse

	senseDir := filepath.Join(cio.Dir, ".sense")
	if env := os.Getenv("SENSE_DIR"); env != "" {
		senseDir = env
	}
	dbPath := filepath.Join(senseDir, "index.db")

	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense: no index found. Run 'sense scan' to build one.")
		return resp, ExitIndexMissing
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense status: %v\n", err)
		return resp, ExitGeneralError
	}

	relPath, _ := filepath.Rel(cio.Dir, dbPath)
	if relPath == "" {
		relPath = dbPath
	}
	resp.Index.Path = relPath
	resp.Index.SizeBytes = info.Size()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense status: %v\n", err)
		return resp, ExitGeneralError
	}
	defer func() { _ = db.Close() }()

	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_files").Scan(&resp.Index.Files)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_symbols").Scan(&resp.Index.Symbols)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_edges").Scan(&resp.Index.Edges)
	_ = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sense_embeddings").Scan(&resp.Index.Embeddings)

	if resp.Index.Symbols > 0 {
		resp.Index.Coverage = float64(resp.Index.Embeddings) / float64(resp.Index.Symbols)
	}

	resp.Languages = queryLangBreakdown(ctx, db)
	resp.Freshness = computeCLIFreshness(ctx, db, cio.Dir)
	resp.Version = buildVersionInfo(ctx, db)
	resp.Lifetime = queryLifetimeCounters(ctx, db)

	return resp, ExitSuccess
}

func queryLangBreakdown(ctx context.Context, db *sql.DB) map[string]mcpio.StatusLanguage {
	const q = `SELECT f.language, COUNT(DISTINCT f.id), COUNT(s.id)
	           FROM sense_files f
	           LEFT JOIN sense_symbols s ON s.file_id = f.id
	           GROUP BY f.language
	           ORDER BY f.language`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return map[string]mcpio.StatusLanguage{}
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]mcpio.StatusLanguage)
	for rows.Next() {
		var lang string
		var sl mcpio.StatusLanguage
		if err := rows.Scan(&lang, &sl.Files, &sl.Symbols); err != nil {
			continue
		}
		sl.Tier = extract.LanguageTier(lang)
		out[lang] = sl
	}
	return out
}


func computeCLIFreshness(ctx context.Context, db *sql.DB, dir string) mcpio.Freshness {
	var f mcpio.Freshness

	var lastScanStr sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`).Scan(&lastScanStr)
	if !lastScanStr.Valid {
		return f
	}

	lastScan, err := time.Parse(time.RFC3339Nano, lastScanStr.String)
	if err != nil {
		return f
	}

	lastScanFmt := lastScan.UTC().Format(time.RFC3339)
	ageSeconds := int64(time.Since(lastScan).Seconds())
	f.LastScan = &lastScanFmt
	f.IndexAgeSeconds = &ageSeconds

	staleCount := countStaleFilesCLI(ctx, db, dir)
	f.StaleFilesSeen = &staleCount

	return f
}

func countStaleFilesCLI(ctx context.Context, db *sql.DB, dir string) int {
	rows, err := db.QueryContext(ctx, `SELECT path, indexed_at FROM sense_files`)
	if err != nil {
		return 0
	}
	defer func() { _ = rows.Close() }()

	var stale int
	for rows.Next() {
		var path, indexedAtStr string
		if err := rows.Scan(&path, &indexedAtStr); err != nil {
			continue
		}
		indexedAt, err := time.Parse(time.RFC3339Nano, indexedAtStr)
		if err != nil {
			continue
		}
		fullPath := filepath.Join(dir, path)
		info, err := os.Stat(fullPath)
		if err != nil {
			continue
		}
		if info.ModTime().After(indexedAt) {
			stale++
		}
	}
	return stale
}

func buildVersionInfo(ctx context.Context, db *sql.DB) *mcpio.StatusVersion {
	var schemaVer int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVer); err != nil {
		return nil
	}

	return &mcpio.StatusVersion{
		Binary:                version.Version,
		Schema:                schemaVer,
		SchemaCurrent:         schemaVer == sqlite.SchemaVersion,
		EmbeddingModel:        "all-MiniLM-L6-v2",
		EmbeddingModelCurrent: true,
	}
}

func renderStatusHuman(cio IO, resp mcpio.StatusResponse) {
	w := cio.Stdout

	_, _ = fmt.Fprintf(w, "Index: %s", resp.Index.Path)
	if resp.Index.SizeBytes > 0 {
		_, _ = fmt.Fprintf(w, " (%s)", formatBytes(resp.Index.SizeBytes))
	}
	_, _ = fmt.Fprintln(w)

	coveragePct := resp.Index.Coverage * 100
	_, _ = fmt.Fprintf(w, "  Files:      %-6d  Coverage: %.1f%%\n", resp.Index.Files, coveragePct)
	_, _ = fmt.Fprintf(w, "  Symbols:  %-6d  Edges: %d\n", resp.Index.Symbols, resp.Index.Edges)

	enabled := EmbeddingsEnabled(cio.Dir)
	if enabled {
		embPct := 0
		if resp.Index.Symbols > 0 {
			embPct = resp.Index.Embeddings * 100 / resp.Index.Symbols
		}
		_, _ = fmt.Fprintf(w, "  Embeddings: %d (%d%%)\n", resp.Index.Embeddings, embPct)
	} else {
		_, _ = fmt.Fprintf(w, "  Embeddings: disabled\n")
	}

	if len(resp.Languages) > 0 {
		_, _ = fmt.Fprintf(w, "\nLanguages:\n")
		langs := sortedLangs(resp.Languages)
		for _, lang := range langs {
			sl := resp.Languages[lang]
			_, _ = fmt.Fprintf(w, "  %-14s %3d files  %5d symbols   tier: %s\n",
				lang, sl.Files, sl.Symbols, sl.Tier)
		}
	}

	_, _ = fmt.Fprintf(w, "\nFreshness:\n")
	if resp.Freshness.LastScan != nil {
		_, _ = fmt.Fprintf(w, "  Last scan:   %s\n", formatAge(resp.Freshness.IndexAgeSeconds))
	} else {
		_, _ = fmt.Fprintf(w, "  Last scan:   unknown\n")
	}
	if resp.Freshness.StaleFilesSeen != nil {
		_, _ = fmt.Fprintf(w, "  Stale files: %d\n", *resp.Freshness.StaleFilesSeen)
	}
	watching := false
	if resp.Freshness.Watching != nil {
		watching = *resp.Freshness.Watching
	}
	if watching {
		_, _ = fmt.Fprintf(w, "  Watching:    yes\n")
	} else {
		_, _ = fmt.Fprintf(w, "  Watching:    no\n")
	}

	if resp.Version != nil {
		_, _ = fmt.Fprintf(w, "\nSchema: v%d", resp.Version.Schema)
		if resp.Version.SchemaCurrent {
			_, _ = fmt.Fprintf(w, " (current)\n")
		} else {
			_, _ = fmt.Fprintf(w, " (mismatch — run 'sense scan --force')\n")
		}
		_, _ = fmt.Fprintf(w, "Embedding model: %s", resp.Version.EmbeddingModel)
		if resp.Version.EmbeddingModelCurrent {
			_, _ = fmt.Fprintf(w, " (matches binary)\n")
		} else {
			_, _ = fmt.Fprintf(w, " (mismatch — run 'sense scan --force')\n")
		}
	}

	if resp.Lifetime != nil && resp.Lifetime.Queries > 0 {
		_, _ = fmt.Fprintf(w, "\nLifetime: %d queries, ~%s tokens saved\n",
			resp.Lifetime.Queries, formatTokens(resp.Lifetime.EstimatedTokensSaved))
	}
}

func queryLifetimeCounters(ctx context.Context, db *sql.DB) *mcpio.StatusLifetime {
	rows, err := db.QueryContext(ctx, `SELECT key, value FROM sense_metrics`)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()

	var lt mcpio.StatusLifetime
	found := false
	for rows.Next() {
		var key string
		var value int
		if err := rows.Scan(&key, &value); err != nil {
			continue
		}
		found = true
		switch key {
		case "lifetime_queries":
			lt.Queries = value
		case "lifetime_file_reads_avoided":
			lt.EstimatedFileReadsAvoided = value
		case "lifetime_tokens_saved":
			lt.EstimatedTokensSaved = value
		}
	}
	if !found {
		return nil
	}
	return &lt
}

func sortedLangs(m map[string]mcpio.StatusLanguage) []string {
	langs := make([]string, 0, len(m))
	for k := range m {
		langs = append(langs, k)
	}
	sort.Strings(langs)
	return langs
}

func formatAge(ageSeconds *int64) string {
	if ageSeconds == nil {
		return "unknown"
	}
	secs := *ageSeconds
	switch {
	case secs < 60:
		return fmt.Sprintf("%d seconds ago", secs)
	case secs < 3600:
		return fmt.Sprintf("%d minutes ago", secs/60)
	case secs < 86400:
		return fmt.Sprintf("%d hours ago", secs/3600)
	default:
		return fmt.Sprintf("%d days ago", secs/86400)
	}
}

func formatTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.0fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// EmbeddingsEnabled resolves whether embeddings are active by checking
// the SENSE_EMBEDDINGS env var first, then falling back to config.yml.
// Default is true (embeddings on).
func EmbeddingsEnabled(root string) bool {
	if env := os.Getenv("SENSE_EMBEDDINGS"); env != "" {
		return !strings.EqualFold(env, "false") && env != "0"
	}
	cfg, err := config.Load(root)
	if err != nil {
		return true
	}
	return cfg.EmbeddingsEnabled()
}
