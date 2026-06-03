package mcpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

func (h *handlers) handleStatus(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := buildStatusResponse(ctx, h.db, h.dir, h.watchState)
	if err != nil {
		return nil, fmt.Errorf("sense_status: %w", err)
	}

	if resp.Structure != nil {
		// Key symbols are optional in status; degrade silently on error.
		if entries, err := buildKeyEntries(ctx, h.adapter, "", 8); err == nil {
			resp.Structure.KeySymbols = entries
		}
	}

	sess := h.tracker.Session()
	resp.Session = &mcpio.StatusSession{
		Queries:                   sess.Queries,
		EstimatedFileReadsAvoided: sess.FileReadsAvoided,
		EstimatedTokensSaved:      sess.TokensSaved,
		TextFallbackFired:         sess.TextFallbackFired,
	}
	if top := h.tracker.TopQuery(); top != nil {
		resp.Session.TopQuery = &mcpio.StatusTopQuery{
			Tool:                 top.Tool,
			Args:                 top.Args,
			EstimatedTokensSaved: top.TokensSaved,
		}
	}

	lt := h.tracker.Lifetime(ctx)
	resp.Lifetime = &mcpio.StatusLifetime{
		Queries:                   lt.Queries,
		EstimatedFileReadsAvoided: lt.FileReadsAvoided,
		EstimatedTokensSaved:      lt.TokensSaved,
		TextFallbackFired:         lt.TextFallbackFired,
	}

	resp.NextSteps = statusHints(resp, sess.Queries)

	out, err := mcpio.MarshalStatusCompact(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_status: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func statusHints(resp mcpio.StatusResponse, sessionQueries int) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.Freshness.StaleFilesSeen != nil && *resp.Freshness.StaleFilesSeen > 0 {
		hints = append(hints, mcpio.NextStep{
			Reason: fmt.Sprintf("index has %d stale files — consider running `sense scan`", *resp.Freshness.StaleFilesSeen),
		})
	}

	if sessionQueries == 0 && len(hints) < mcpio.MaxNextSteps {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_conventions",
			Reason: "start of session — check project conventions",
		})
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
	}
	return hints
}

func buildStatusResponse(ctx context.Context, db *sql.DB, dir string, ws *mcpio.WatchState) (mcpio.StatusResponse, error) {
	var resp mcpio.StatusResponse

	index, progress, err := queryIndexCounts(ctx, db, dir)
	if err != nil {
		return resp, err
	}
	resp.Index = index
	resp.EmbeddingProgress = progress

	langs, err := queryLanguageBreakdown(ctx, db)
	if err != nil {
		return resp, err
	}
	resp.Languages = langs

	if freshness := computeFreshness(ctx, db, dir, true, ws, nil); freshness != nil {
		resp.Freshness = *freshness
	}

	resp.Version = queryVersion(ctx, db)

	structure, err := buildStructure(ctx, db, resp, langs)
	if err != nil {
		return resp, err
	}
	resp.Structure = structure

	if prof := profile.Load(ctx, db); prof != nil {
		resp.Profile = &mcpio.StatusProfile{
			Tier:            prof.Tier,
			Symbols:         prof.Symbols,
			PrimaryLanguage: prof.PrimaryLang,
			DynamicLanguage: prof.DynamicLang,
			Description:     readMeta(ctx, db, "project_description"),
		}
	}

	return resp, nil
}

// queryIndexCounts reads the index path/size and the file/symbol/edge/embedding
// counts, deriving embedding coverage and the embedding-debt progress (non-nil
// only when symbols remain unembedded).
func queryIndexCounts(ctx context.Context, db *sql.DB, dir string) (mcpio.StatusIndex, *mcpio.EmbeddingProgress, error) {
	var index mcpio.StatusIndex

	dbPath := filepath.Join(dir, ".sense", "index.db")
	if relPath, err := filepath.Rel(dir, dbPath); err == nil {
		index.Path = relPath
	} else {
		index.Path = dbPath
	}
	if info, err := os.Stat(dbPath); err == nil {
		index.SizeBytes = info.Size()
	}

	counts := []struct {
		query string
		dst   *int
	}{
		{`SELECT COUNT(*) FROM sense_files`, &index.Files},
		{`SELECT COUNT(*) FROM sense_symbols`, &index.Symbols},
		{`SELECT COUNT(*) FROM sense_edges`, &index.Edges},
		{`SELECT COUNT(*) FROM sense_embeddings`, &index.Embeddings},
	}
	for _, c := range counts {
		if err := db.QueryRowContext(ctx, c.query).Scan(c.dst); err != nil {
			return index, nil, err
		}
	}

	if index.Symbols > 0 {
		index.Coverage = float64(index.Embeddings) / float64(index.Symbols)
	}

	var progress *mcpio.EmbeddingProgress
	if debt := index.Symbols - index.Embeddings; debt > 0 {
		pct := 0
		if index.Symbols > 0 {
			pct = index.Embeddings * 100 / index.Symbols
		}
		progress = &mcpio.EmbeddingProgress{
			Total:    index.Symbols,
			Embedded: index.Embeddings,
			Percent:  pct,
		}
	}

	return index, progress, nil
}

// queryVersion reports the schema and embedding-model versions, comparing each
// to the binary's current values. It returns nil when the schema version
// pragma cannot be read.
func queryVersion(ctx context.Context, db *sql.DB) *mcpio.StatusVersion {
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

// ---------------------------------------------------------------
// Structural orientation
// ---------------------------------------------------------------

func buildStructure(ctx context.Context, db *sql.DB, resp mcpio.StatusResponse, langs map[string]mcpio.StatusLanguage) (*mcpio.StatusStructure, error) {
	ns, err := queryTopNamespaces(ctx, db)
	if err != nil {
		return nil, err
	}
	hubs, err := queryHubSymbols(ctx, db)
	if err != nil {
		return nil, err
	}
	entries, err := queryEntryPoints(ctx, db)
	if err != nil {
		return nil, err
	}
	frameworks := loadFrameworks(ctx, db)
	fp := buildFingerprint(resp, langs, ns, hubs, frameworks)
	return &mcpio.StatusStructure{
		TopNamespaces: ns,
		HubSymbols:    hubs,
		EntryPoints:   entries,
		Frameworks:    frameworks,
		Fingerprint:   fp,
	}, nil
}

func loadFrameworks(ctx context.Context, db *sql.DB) []string {
	val := readMeta(ctx, db, "frameworks")
	if val == "" {
		return nil
	}
	var frameworks []string
	if err := json.Unmarshal([]byte(val), &frameworks); err != nil {
		return nil
	}
	return frameworks
}

func queryTopNamespaces(ctx context.Context, db *sql.DB) ([]mcpio.StatusNamespace, error) {
	const q = `SELECT f.path, COUNT(s.id)
	FROM sense_files f
	JOIN sense_symbols s ON s.file_id = f.id
	GROUP BY f.id`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var filePath string
		var symCount int
		if err := rows.Scan(&filePath, &symCount); err != nil {
			return nil, err
		}
		ns := namespacePrefixFromPath(filePath)
		counts[ns] += symCount
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]mcpio.StatusNamespace, 0, len(counts))
	for name, syms := range counts {
		out = append(out, mcpio.StatusNamespace{Name: name, Symbols: syms, Kind: "directory"})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbols != out[j].Symbols {
			return out[i].Symbols > out[j].Symbols
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out, nil
}

func namespacePrefixFromPath(p string) string {
	first, rest, ok := strings.Cut(p, "/")
	if !ok {
		return "."
	}
	if second, _, ok := strings.Cut(rest, "/"); ok {
		return first + "/" + second
	}
	return first
}

func queryHubSymbols(ctx context.Context, db *sql.DB) ([]mcpio.StatusHub, error) {
	const q = `SELECT s.name, COUNT(DISTINCT e.file_id) AS reach, s.kind,
		(SELECT e2.kind FROM sense_edges e2
		 WHERE e2.target_id = s.id
		 GROUP BY e2.kind ORDER BY COUNT(*) DESC LIMIT 1) AS dominant_edge
	FROM sense_symbols s
	JOIN sense_edges e ON e.target_id = s.id
	GROUP BY s.id
	ORDER BY
	  CASE WHEN s.kind IN ('class','interface','module','type','struct','trait') THEN 0 ELSE 1 END,
	  reach DESC
	LIMIT 5`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []mcpio.StatusHub
	for rows.Next() {
		var h mcpio.StatusHub
		var dominantEdge string
		if err := rows.Scan(&h.Name, &h.Callers, &h.Kind, &dominantEdge); err != nil {
			return nil, err
		}
		h.Role = edgeKindToRole(dominantEdge)
		out = append(out, h)
	}
	return out, rows.Err()
}

func edgeKindToRole(edgeKind string) string {
	switch edgeKind {
	case "inherits":
		return "base class"
	case "includes", "composes":
		return "mixin"
	default:
		return "hub"
	}
}

func queryEntryPoints(ctx context.Context, db *sql.DB) ([]mcpio.StatusEntryPoint, error) {
	// Symbol-based entry points: main or Main functions
	const symQ = `SELECT s.name, f.path, s.kind
	FROM sense_symbols s
	JOIN sense_files f ON f.id = s.file_id
	WHERE s.name IN ('main', 'Main') AND s.kind = 'function'
	LIMIT 5`

	rows, err := db.QueryContext(ctx, symQ)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []mcpio.StatusEntryPoint
	for rows.Next() {
		var ep mcpio.StatusEntryPoint
		if err := rows.Scan(&ep.Name, &ep.File, &ep.Kind); err != nil {
			return nil, err
		}
		out = append(out, ep)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// File-based entry points: known patterns at root or src/
	const fileQ = `SELECT path FROM sense_files
	WHERE (
	       path IN ('main.go','main.py','main.rs','main.rb','main.java','main.kt','main.scala','main.c','main.cpp')
	    OR path IN ('src/main.go','src/main.py','src/main.rs','src/main.ts','src/main.js')
	    OR path IN ('routes.rb','config/routes.rb')
	    OR path IN ('index.ts','index.tsx','index.js','index.jsx',
	                'src/index.ts','src/index.tsx','src/index.js','src/index.jsx')
	    OR path IN ('App.tsx','App.jsx','App.ts','App.js',
	                'src/App.tsx','src/App.jsx','src/App.ts','src/App.js')
	)
	LIMIT 5`

	frows, err := db.QueryContext(ctx, fileQ)
	if err != nil {
		return nil, err
	}
	defer func() { _ = frows.Close() }()

	seen := make(map[string]bool)
	for _, ep := range out {
		seen[ep.File] = true
	}
	for frows.Next() {
		var fpath string
		if err := frows.Scan(&fpath); err != nil {
			return nil, err
		}
		if seen[fpath] {
			continue
		}
		out = append(out, mcpio.StatusEntryPoint{
			Name: path.Base(fpath),
			File: fpath,
			Kind: "file",
		})
	}
	return out, frows.Err()
}

func buildFingerprint(resp mcpio.StatusResponse, langs map[string]mcpio.StatusLanguage, ns []mcpio.StatusNamespace, hubs []mcpio.StatusHub, frameworks []string) string {
	// Primary language: the one with the most symbols
	primaryLang := ""
	maxSyms := 0
	for lang, info := range langs {
		if info.Symbols > maxSyms || (info.Symbols == maxSyms && lang < primaryLang) {
			maxSyms = info.Symbols
			primaryLang = lang
		}
	}
	if primaryLang == "" {
		return ""
	}

	// Top namespace names
	nsNames := make([]string, 0, 3)
	for i, n := range ns {
		if i >= 3 {
			break
		}
		nsNames = append(nsNames, fmt.Sprintf("%s (%d)", n.Name, n.Symbols))
	}

	// Hub symbol descriptions with role hints
	hubNames := make([]string, 0, 3)
	for i, h := range hubs {
		if i >= 3 {
			break
		}
		hubNames = append(hubNames, fmt.Sprintf("%s (%s, %s, %d callers)", h.Name, h.Kind, h.Role, h.Callers))
	}

	parts := []string{
		fmt.Sprintf("%s project.", capitalizeFirst(primaryLang)),
	}
	if len(frameworks) > 0 {
		parts = append(parts, fmt.Sprintf("Frameworks: %s.", strings.Join(frameworks, ", ")))
	}
	parts = append(parts, fmt.Sprintf("%d files, %d symbols.", resp.Index.Files, resp.Index.Symbols))
	if len(nsNames) > 0 {
		parts = append(parts, fmt.Sprintf("Heaviest areas: %s.", strings.Join(nsNames, ", ")))
	}
	if len(hubNames) > 0 {
		parts = append(parts, fmt.Sprintf("Hub symbols: %s.", strings.Join(hubNames, ", ")))
	}

	return strings.Join(parts, " ")
}

func capitalizeFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// ---------------------------------------------------------------
// Language tier breakdown
// ---------------------------------------------------------------

func queryLanguageBreakdown(ctx context.Context, db *sql.DB) (map[string]mcpio.StatusLanguage, error) {
	const q = `SELECT f.language, COUNT(DISTINCT f.id), COUNT(s.id)
	           FROM sense_files f
	           LEFT JOIN sense_symbols s ON s.file_id = f.id
	           GROUP BY f.language
	           ORDER BY f.language`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]mcpio.StatusLanguage)
	for rows.Next() {
		var lang string
		var sl mcpio.StatusLanguage
		if err := rows.Scan(&lang, &sl.Files, &sl.Symbols); err != nil {
			return nil, err
		}
		sl.Tier = extract.LanguageTier(lang)
		out[lang] = sl
	}
	return out, rows.Err()
}

func readMeta(ctx context.Context, db *sql.DB, key string) string {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM sense_meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}
