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
	"time"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/freshen"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"

	_ "modernc.org/sqlite"
)

const statusHelp = `usage: sense status [flags]

Show index health: file/symbol/edge counts, per-language breakdown,
coverage, freshness, and version info.

Flags:
  --json       Emit JSON matching the sense_status MCP schema
  -h, --help   Show this help

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
`

type healthInfo struct {
	verdict string // "healthy", "degraded", "unhealthy"
	detail  string // worst issue, e.g. "3 stale files"
}

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
	resp, health, exitCode := buildCLIStatusResponse(ctx, cio)
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

	renderStatusHuman(cio, resp, health)
	return ExitSuccess
}

func buildCLIStatusResponse(ctx context.Context, cio IO) (mcpio.StatusResponse, healthInfo, int) {
	var resp mcpio.StatusResponse
	var health healthInfo

	senseDir := filepath.Join(cio.Dir, ".sense")
	if env := os.Getenv("SENSE_DIR"); env != "" {
		senseDir = env
	}
	dbPath := filepath.Join(senseDir, "index.db")

	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense: no index found. Run 'sense scan' to build one.")
		return resp, health, ExitIndexMissing
	}

	info, err := os.Stat(dbPath)
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense status: %v\n", err)
		return resp, health, ExitGeneralError
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
		return resp, health, ExitGeneralError
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

	if prof := profile.Load(ctx, db); prof != nil {
		resp.Profile = &mcpio.StatusProfile{
			Tier:            prof.Tier,
			Symbols:         prof.Symbols,
			PrimaryLanguage: prof.PrimaryLang,
			DynamicLanguage: prof.DynamicLang,
		}
	}

	health = computeHealth(ctx, db, cio.Dir, resp)

	return resp, health, ExitSuccess
}

func computeHealth(ctx context.Context, db *sql.DB, dir string, resp mcpio.StatusResponse) healthInfo {
	h := healthInfo{verdict: "healthy"}

	if resp.Version != nil && !resp.Version.SchemaCurrent {
		h.verdict = "unhealthy"
		h.detail = "schema mismatch — run 'sense scan --rebuild'"
		return h
	}

	if countOrphanedEdges(ctx, db) > 0 {
		h.verdict = "unhealthy"
		h.detail = "orphaned edges — run 'sense scan --rebuild'"
		return h
	}

	if resp.Version != nil && !resp.Version.EmbeddingModelCurrent {
		h.verdict = "unhealthy"
		h.detail = "embedding model mismatch — run 'sense scan --rebuild'"
		return h
	}

	if EmbeddingsEnabled(dir) && resp.Index.Symbols > 0 {
		pct := resp.Index.Embeddings * 100 / resp.Index.Symbols
		if pct < 90 {
			h.verdict = "degraded"
			h.detail = fmt.Sprintf("embeddings incomplete (%d%%)", pct)
		}
	}

	if resp.Freshness.StaleFilesSeen != nil && *resp.Freshness.StaleFilesSeen > 0 {
		if h.verdict == "healthy" {
			h.verdict = "degraded"
		}
		if h.detail == "" {
			h.detail = fmt.Sprintf("%d stale files", *resp.Freshness.StaleFilesSeen)
		}
	}

	return h
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

	lastScanMeta := readMeta(ctx, db, "last_scan_at")

	var lastUpdateStr sql.NullString
	_ = db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`).Scan(&lastUpdateStr)

	if lastScanMeta != "" {
		if ts, err := time.Parse(time.RFC3339, lastScanMeta); err == nil {
			fmtStr := ts.UTC().Format(time.RFC3339)
			age := int64(time.Since(ts).Seconds())
			f.LastScan = &fmtStr
			f.IndexAgeSeconds = &age
		}
	}

	if lastUpdateStr.Valid {
		if ts, err := time.Parse(time.RFC3339Nano, lastUpdateStr.String); err == nil {
			if f.LastScan == nil {
				fmtStr := ts.UTC().Format(time.RFC3339)
				age := int64(time.Since(ts).Seconds())
				f.LastScan = &fmtStr
				f.IndexAgeSeconds = &age
			} else {
				scanAge := *f.IndexAgeSeconds
				updateAge := int64(time.Since(ts).Seconds())
				if abs(scanAge-updateAge) > 60 {
					fmtStr := ts.UTC().Format(time.RFC3339)
					f.LastUpdate = &fmtStr
					f.IndexUpdateAgeSeconds = &updateAge
				}
			}
		}
	}

	staleCount := countStaleFilesCLI(ctx, db, dir)
	f.StaleFilesSeen = &staleCount

	// Live watcher state: a one-shot `sense status` has no watcher of its
	// own, but it can tell whether a `sense mcp` server is watching this
	// repo (it holds the single-writer lock) and how many symbols are
	// pending embeddings.
	if freshen.IsWriterLocked(dir) {
		watching := true
		f.Watching = &watching
		if pending := embeddingDebtCLI(ctx, db); pending >= 0 {
			f.Pending = &pending
		}
	}

	return f
}

// embeddingDebtCLI counts symbols eligible for embeddings that have none
// yet. Returns -1 if the count cannot be determined.
func embeddingDebtCLI(ctx context.Context, db *sql.DB) int {
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_symbols s
		 LEFT JOIN sense_embeddings e ON e.symbol_id = s.id
		 WHERE e.symbol_id IS NULL`).Scan(&n); err != nil {
		return -1
	}
	return n
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
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

	storedModel := readMeta(ctx, db, "embedding_model")
	if storedModel == "" {
		storedModel = embed.ModelID
	}

	return &mcpio.StatusVersion{
		Binary:                version.Version,
		Schema:                schemaVer,
		SchemaCurrent:         schemaVer == sqlite.SchemaVersion,
		EmbeddingModel:        storedModel,
		EmbeddingModelCurrent: storedModel == embed.ModelID,
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

func renderStatusHuman(cio IO, resp mcpio.StatusResponse, health healthInfo) {
	w := cio.Stdout

	_, _ = fmt.Fprintf(w, "Index: %s", resp.Index.Path)
	if resp.Index.SizeBytes > 0 {
		_, _ = fmt.Fprintf(w, " (%s)", formatBytes(resp.Index.SizeBytes))
	}
	_, _ = fmt.Fprintln(w)

	if health.detail != "" {
		_, _ = fmt.Fprintf(w, "Health: %s (%s)\n", health.verdict, health.detail)
	} else {
		_, _ = fmt.Fprintf(w, "Health: %s\n", health.verdict)
	}

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

	if resp.Profile != nil {
		_, _ = fmt.Fprintf(w, "\nProfile: %s", resp.Profile.Tier)
		if resp.Profile.PrimaryLanguage != "" {
			_, _ = fmt.Fprintf(w, " (primary: %s", resp.Profile.PrimaryLanguage)
			if resp.Profile.DynamicLanguage {
				_, _ = fmt.Fprintf(w, ", dynamic")
			}
			_, _ = fmt.Fprintf(w, ")")
		}
		_, _ = fmt.Fprintln(w)
	}

	_, _ = fmt.Fprintf(w, "\nFreshness:\n")
	if resp.Freshness.LastScan != nil {
		_, _ = fmt.Fprintf(w, "  Last scan:   %s\n", formatAge(resp.Freshness.IndexAgeSeconds))
	} else {
		_, _ = fmt.Fprintf(w, "  Last scan:   unknown\n")
	}
	if resp.Freshness.LastUpdate != nil {
		_, _ = fmt.Fprintf(w, "  Last update: %s\n", formatAge(resp.Freshness.IndexUpdateAgeSeconds))
	}
	if resp.Freshness.StaleFilesSeen != nil && *resp.Freshness.StaleFilesSeen > 0 {
		_, _ = fmt.Fprintf(w, "  Stale files: %d\n", *resp.Freshness.StaleFilesSeen)
	}
	if resp.Freshness.Watching != nil && *resp.Freshness.Watching {
		_, _ = fmt.Fprintf(w, "  Watching:    yes\n")
	}
	if resp.Freshness.Pending != nil && *resp.Freshness.Pending > 0 {
		_, _ = fmt.Fprintf(w, "  Pending:     %d symbols awaiting embeddings\n", *resp.Freshness.Pending)
	}

	if resp.Version != nil {
		_, _ = fmt.Fprintf(w, "\nSchema: v%d", resp.Version.Schema)
		if resp.Version.SchemaCurrent {
			_, _ = fmt.Fprintf(w, " (current)\n")
		} else {
			_, _ = fmt.Fprintf(w, " (mismatch — run 'sense scan --rebuild')\n")
		}
		_, _ = fmt.Fprintf(w, "Embedding model: %s", resp.Version.EmbeddingModel)
		if resp.Version.EmbeddingModelCurrent {
			_, _ = fmt.Fprintf(w, " (current)\n")
		} else {
			_, _ = fmt.Fprintf(w, " (mismatch — binary has %s)\n", embed.ModelID)
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

func EmbeddingsEnabled(root string) bool {
	return config.IsEmbeddingsEnabled(root)
}
