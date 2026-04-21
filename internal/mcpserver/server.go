// Package mcpserver implements the `sense mcp` stdio server that
// exposes graph, blast, and status tools over the Model Context
// Protocol. Built on github.com/mark3labs/mcp-go — the de-facto Go
// MCP SDK. Each handler is a thin wrapper around the same engine code
// the CLI commands call, marshalled through internal/mcpio.
package mcpserver

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

// RunOptions configures the MCP server.
type RunOptions struct {
	Dir        string
	WatchState *mcpio.WatchState // nil when not in watch mode
}

// Run starts the MCP stdio server with default options.
func Run(dir string) error {
	return RunWithOptions(RunOptions{Dir: dir})
}

// RunWithOptions starts the MCP stdio server with explicit options.
func RunWithOptions(opts RunOptions) error {
	dir := opts.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("sense mcp: getwd: %w", err)
		}
		dir = wd
	}

	ctx := context.Background()
	adapter, err := cli.OpenIndex(ctx, dir)
	if err != nil {
		return fmt.Errorf("sense mcp: %w", err)
	}

	if adapter.Rebuilt {
		_ = adapter.Close()
		fmt.Fprintf(os.Stderr, "sense mcp: schema version mismatch — rebuilding index...\n")
		if _, err := scan.Run(ctx, scan.Options{
			Root:              dir,
			Output:            os.Stderr,
			Warnings:          os.Stderr,
			EmbeddingsEnabled: cli.EmbeddingsEnabled(dir),
		}); err != nil {
			return fmt.Errorf("sense mcp: rebuild scan: %w", err)
		}
		adapter, err = cli.OpenIndex(ctx, dir)
		if err != nil {
			return fmt.Errorf("sense mcp: reopen after rebuild: %w", err)
		}
		fmt.Fprintf(os.Stderr, "sense mcp: rebuild complete\n")
	}
	defer func() { _ = adapter.Close() }()

	if storedModel, _ := adapter.ReadMeta(ctx, "embedding_model"); storedModel != "" && storedModel != embed.ModelID {
		fmt.Fprintf(os.Stderr, "sense mcp: embedding model changed (index: %s, binary: %s). Search results may be degraded. Run `sense scan --force` to re-embed.\n", storedModel, embed.ModelID)
	}

	var vectorIdx search.VectorIndex
	var embedder embed.Embedder

	if cli.EmbeddingsEnabled(dir) {
		embeddings, err := adapter.LoadEmbeddings(ctx)
		if err != nil {
			return fmt.Errorf("sense mcp: load embeddings: %w", err)
		}
		if len(embeddings) > 0 {
			vectorIdx = search.BuildHNSWIndex(embeddings)
			embedder, err = embed.NewBundledEmbedder()
			if err != nil {
				return fmt.Errorf("sense mcp: init embedder: %w", err)
			}
			defer func() { _ = embedder.Close() }()
		}
	}

	engine := search.NewEngine(adapter, vectorIdx, embedder)

	tracker := metrics.NewTracker(adapter.DB())
	defer tracker.Close()

	s := server.NewMCPServer(
		"sense",
		version.Version,
		server.WithToolCapabilities(false),
	)

	h := &handlers{adapter: adapter, db: adapter.DB(), dir: dir, search: engine, watchState: opts.WatchState, tracker: tracker}

	s.AddTool(searchTool(), h.handleSearch)
	s.AddTool(graphTool(), h.handleGraph)
	s.AddTool(blastTool(), h.handleBlast)
	s.AddTool(conventionsTool(), h.handleConventions)
	s.AddTool(statusTool(), h.handleStatus)

	return server.ServeStdio(s)
}

// handlers holds shared state for MCP tool handlers. The adapter is
// kept for methods like ReadSymbol that live on *sqlite.Adapter; db
// is a convenience alias for plain-SQL callers (Lookup, LoadFilePaths).
type handlers struct {
	adapter    *sqlite.Adapter
	db         *sql.DB
	dir        string
	search     *search.Engine
	watchState *mcpio.WatchState
	tracker    *metrics.Tracker
}

// ---------------------------------------------------------------
// Tool schemas
// ---------------------------------------------------------------

func searchTool() mcp.Tool {
	return mcp.NewTool("sense.search",
		mcp.WithDescription("Hybrid semantic + keyword search across all indexed symbols"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Search",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural-language search query"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum results (default 10)"),
		),
		mcp.WithString("language",
			mcp.Description("Filter by language (e.g. \"ruby\", \"go\")"),
		),
		mcp.WithNumber("min_score",
			mcp.Description("Minimum score threshold 0.0–1.0 (default 0.5)"),
		),
	)
}

func graphTool() mcp.Tool {
	return mcp.NewTool("sense.graph",
		mcp.WithDescription("Symbol relationships — callers, callees, inheritance, tests"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Graph",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Required(),
			mcp.Description("Qualified or unqualified symbol name to look up"),
		),
		mcp.WithNumber("depth",
			mcp.Description("Traversal depth around the subject (default 1)"),
		),
		mcp.WithString("direction",
			mcp.Description("One of: both, callers, callees (default both)"),
			mcp.Enum("both", "callers", "callees"),
		),
	)
}

func blastTool() mcp.Tool {
	return mcp.NewTool("sense.blast",
		mcp.WithDescription("Blast radius for a symbol or diff — what breaks if this changes?"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Blast Radius",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Description("Qualified or unqualified symbol name (mutually exclusive with diff)"),
		),
		mcp.WithString("diff",
			mcp.Description("Git ref for diff-based blast (e.g. HEAD~1, main..feature)"),
		),
		mcp.WithNumber("max_hops",
			mcp.Description("Traversal depth (default 3)"),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Edge-confidence threshold 0.0–1.0 (default 0.7)"),
		),
		mcp.WithBoolean("include_tests",
			mcp.Description("Include affected test files (default true)"),
		),
	)
}

func statusTool() mcp.Tool {
	return mcp.NewTool("sense.status",
		mcp.WithDescription("Index health — file/symbol/edge counts, language breakdown, freshness"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Status",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
	)
}

// ---------------------------------------------------------------
// sense.graph handler
// ---------------------------------------------------------------

func (h *handlers) handleGraph(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbol, err := req.RequireString("symbol")
	if err != nil {
		return mcp.NewToolResultError("sense.graph: missing required parameter 'symbol'"), nil
	}

	depth := req.GetInt("depth", 1)
	if depth != 1 {
		return mcp.NewToolResultError("sense.graph: --depth > 1 is not yet supported"), nil
	}

	direction := req.GetString("direction", "both")

	matches, err := cli.Lookup(ctx, h.db, symbol)
	if err != nil {
		return nil, fmt.Errorf("sense.graph: lookup: %w", err)
	}
	switch len(matches) {
	case 0:
		return mcp.NewToolResultError(fmt.Sprintf("sense.graph: no symbol matches %q", symbol)), nil
	case 1:
		// resolved
	default:
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s (%s) %s:%d", m.Qualified, m.Kind, m.File, m.LineStart))
		}
		return mcp.NewToolResultError(fmt.Sprintf(
			"sense.graph: multiple symbols match %q — specify a qualified name:\n%s",
			symbol, strings.Join(lines, "\n"))), nil
	}

	sc, err := h.adapter.ReadSymbol(ctx, matches[0].ID)
	if err != nil {
		return nil, fmt.Errorf("sense.graph: read symbol: %w", err)
	}

	fileIDs := cli.CollectFileIDs(sc)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("sense.graph: load file paths: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	resp := mcpio.BuildGraphResponse(sc, lookup, mcpio.BuildGraphRequest{
		Direction: direction,
	})
	h.tracker.Record("sense.graph", symbol,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState)
	resp.Freshness = freshness

	out, err := mcpio.MarshalGraph(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.graph: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// ---------------------------------------------------------------
// sense.search handler
// ---------------------------------------------------------------

func (h *handlers) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("sense.search: missing required parameter 'query'"), nil
	}

	limit := req.GetInt("limit", 10)
	language := req.GetString("language", "")
	minScore := req.GetFloat("min_score", 0.0)

	results, symbolCount, err := h.search.Search(ctx, search.Options{
		Query:    query,
		Limit:    limit,
		Language: language,
		MinScore: minScore,
	})
	if err != nil {
		return nil, fmt.Errorf("sense.search: %w", err)
	}

	fileIDs := make([]int64, len(results))
	for i, r := range results {
		fileIDs[i] = r.FileID
	}
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("sense.search: load file paths: %w", err)
	}

	entries := make([]mcpio.SearchResultEntry, len(results))
	uniqueFiles := map[string]struct{}{}
	for i, r := range results {
		path := pathByID[r.FileID]
		entries[i] = mcpio.SearchResultEntry{
			Symbol:  r.Qualified,
			File:    path,
			Line:    r.LineStart,
			Kind:    r.Kind,
			Score:   mcpio.SearchScore(r.Score),
			Snippet: r.Snippet,
		}
		if path != "" {
			uniqueFiles[path] = struct{}{}
		}
	}

	filesAvoided := len(uniqueFiles)
	resp := mcpio.SearchResponse{
		Results: entries,
		SenseMetrics: mcpio.SearchMetrics{
			SymbolsSearched:           symbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
	h.tracker.Record("sense.search", query,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	out, err := mcpio.MarshalSearch(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.search: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// ---------------------------------------------------------------
// sense.blast handler
// ---------------------------------------------------------------

func (h *handlers) handleBlast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbol := req.GetString("symbol", "")
	diff := req.GetString("diff", "")

	if symbol == "" && diff == "" {
		return mcp.NewToolResultError("sense.blast: pass either 'symbol' or 'diff'"), nil
	}
	if symbol != "" && diff != "" {
		return mcp.NewToolResultError("sense.blast: pass either 'symbol' or 'diff', not both"), nil
	}

	maxHops := req.GetInt("max_hops", 3)
	minConfidence := req.GetFloat("min_confidence", 0.7)
	includeTests := req.GetBool("include_tests", true)

	opts := blast.Options{
		MaxHops:       maxHops,
		MinConfidence: minConfidence,
		IncludeTests:  includeTests,
	}

	var resp mcpio.BlastResponse

	if diff != "" {
		resp2, err := h.blastDiff(ctx, diff, opts)
		if err != nil {
			return nil, err
		}
		resp = resp2
	} else {
		resp2, err := h.blastSymbol(ctx, symbol, opts)
		if err != nil {
			if toolErr, ok := err.(*toolError); ok {
				return mcp.NewToolResultError(toolErr.msg), nil
			}
			return nil, err
		}
		resp = resp2
	}

	blastArgs := symbol
	if diff != "" {
		blastArgs = diff
	}
	h.tracker.Record("sense.blast", blastArgs,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState)
	resp.Freshness = freshness

	out, err := mcpio.MarshalBlast(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.blast: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

func (h *handlers) blastSymbol(ctx context.Context, symbol string, opts blast.Options) (mcpio.BlastResponse, error) {
	matches, err := cli.Lookup(ctx, h.db, symbol)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: lookup: %w", err)
	}
	switch len(matches) {
	case 0:
		return mcpio.BlastResponse{}, &toolError{fmt.Sprintf("sense.blast: no symbol matches %q", symbol)}
	case 1:
		// resolved
	default:
		var lines []string
		for _, m := range matches {
			lines = append(lines, fmt.Sprintf("  %s (%s) %s:%d", m.Qualified, m.Kind, m.File, m.LineStart))
		}
		return mcpio.BlastResponse{}, &toolError{fmt.Sprintf(
			"sense.blast: multiple symbols match %q — specify a qualified name:\n%s",
			symbol, strings.Join(lines, "\n"))}
	}

	result, err := blast.Compute(ctx, h.db, matches[0].ID, opts)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: compute: %w", err)
	}

	fileIDs := cli.CollectBlastFileIDs(result)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: load file paths: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	return mcpio.BuildBlastResponse(result, lookup), nil
}

func (h *handlers) blastDiff(ctx context.Context, ref string, opts blast.Options) (mcpio.BlastResponse, error) {
	paths, err := cli.GitDiffFiles(ctx, h.dir, ref)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: %w", err)
	}

	symbolIDs, err := cli.SymbolsInFiles(ctx, h.db, paths)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: %w", err)
	}

	results := make([]blast.Result, 0, len(symbolIDs))
	for _, sid := range symbolIDs {
		r, err := blast.Compute(ctx, h.db, sid, opts)
		if err != nil {
			return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: %w", err)
		}
		results = append(results, r)
	}

	fileIDs := cli.CollectDiffFileIDs(results)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	return mcpio.BuildDiffBlastResponse(ref, results, lookup), nil
}

// ---------------------------------------------------------------
// sense.conventions handler
// ---------------------------------------------------------------

func conventionsTool() mcp.Tool {
	return mcp.NewTool("sense.conventions",
		mcp.WithDescription("Detected project conventions — inheritance, naming, structure, composition, testing patterns"),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Conventions",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("domain",
			mcp.Description("Scope to files matching this path substring (e.g. \"models\", \"controllers\")"),
		),
		mcp.WithNumber("min_strength",
			mcp.Description("Minimum strength threshold 0.0–1.0 (default 0.0 — show all)"),
		),
	)
}

func (h *handlers) handleConventions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	domain := req.GetString("domain", "")
	minStrength := req.GetFloat("min_strength", 0.0)

	results, symbolCount, err := conventions.Detect(ctx, h.db, conventions.Options{
		Domain:      domain,
		MinStrength: minStrength,
	})
	if err != nil {
		return nil, fmt.Errorf("sense.conventions: %w", err)
	}

	filesAvoided := min(symbolCount/5, 30)
	resp := mcpio.ConventionsResponse{
		Conventions: make([]mcpio.ConventionEntry, len(results)),
		SenseMetrics: mcpio.ConventionsMetrics{
			SymbolsAnalyzed:           symbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
	for i, c := range results {
		resp.Conventions[i] = mcpio.ConventionEntry{
			Category:    string(c.Category),
			Description: c.Description,
			Instances:   c.Instances,
			Total:       c.Total,
			Strength:    mcpio.Confidence(c.Strength),
		}
	}

	h.tracker.Record("sense.conventions", domain,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	out, err := mcpio.MarshalConventions(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.conventions: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// ---------------------------------------------------------------
// sense.status handler
// ---------------------------------------------------------------

func (h *handlers) handleStatus(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, err := buildStatusResponse(ctx, h.db, h.dir, h.watchState)
	if err != nil {
		return nil, fmt.Errorf("sense.status: %w", err)
	}

	sess := h.tracker.Session()
	resp.Session = &mcpio.StatusSession{
		Queries:                   sess.Queries,
		EstimatedFileReadsAvoided: sess.FileReadsAvoided,
		EstimatedTokensSaved:      sess.TokensSaved,
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
	}

	out, err := mcpio.MarshalStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.status: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func buildStatusResponse(ctx context.Context, db *sql.DB, dir string, ws *mcpio.WatchState) (mcpio.StatusResponse, error) {
	var resp mcpio.StatusResponse

	dbPath := filepath.Join(dir, ".sense", "index.db")
	if relPath, err := filepath.Rel(dir, dbPath); err == nil {
		resp.Index.Path = relPath
	} else {
		resp.Index.Path = dbPath
	}
	if info, err := os.Stat(dbPath); err == nil {
		resp.Index.SizeBytes = info.Size()
	}

	row := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_files`)
	if err := row.Scan(&resp.Index.Files); err != nil {
		return resp, err
	}
	row = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`)
	if err := row.Scan(&resp.Index.Symbols); err != nil {
		return resp, err
	}
	row = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_edges`)
	if err := row.Scan(&resp.Index.Edges); err != nil {
		return resp, err
	}
	row = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_embeddings`)
	if err := row.Scan(&resp.Index.Embeddings); err != nil {
		return resp, err
	}

	if resp.Index.Symbols > 0 {
		resp.Index.Coverage = float64(resp.Index.Embeddings) / float64(resp.Index.Symbols)
	}

	langs, err := queryLanguageBreakdown(ctx, db)
	if err != nil {
		return resp, err
	}
	resp.Languages = langs

	freshness := computeFreshness(ctx, db, dir, true, ws)
	if freshness != nil {
		resp.Freshness = *freshness
	}

	var schemaVer int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVer); err == nil {
		storedModel := readMeta(ctx, db, "embedding_model")
		if storedModel == "" {
			storedModel = embed.ModelID
		}
		resp.Version = &mcpio.StatusVersion{
			Binary:                version.Version,
			Schema:                schemaVer,
			SchemaCurrent:         schemaVer == sqlite.SchemaVersion,
			EmbeddingModel:        storedModel,
			EmbeddingModelCurrent: storedModel == embed.ModelID,
		}
	}

	return resp, nil
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

// ---------------------------------------------------------------
// Freshness computation
// ---------------------------------------------------------------

func computeFreshness(ctx context.Context, db *sql.DB, dir string, includeMaxMtime bool, ws *mcpio.WatchState) *mcpio.Freshness {
	var lastScanStr sql.NullString
	row := db.QueryRowContext(ctx, `SELECT MAX(indexed_at) FROM sense_files`)
	if err := row.Scan(&lastScanStr); err != nil || !lastScanStr.Valid {
		return nil
	}

	lastScan, err := time.Parse(time.RFC3339Nano, lastScanStr.String)
	if err != nil {
		return nil
	}

	ageSeconds := int64(time.Since(lastScan).Seconds())
	lastScanFmt := lastScan.UTC().Format(time.RFC3339)

	f := &mcpio.Freshness{
		LastScan:        &lastScanFmt,
		IndexAgeSeconds: &ageSeconds,
	}

	staleCount, maxMtime := countStaleFiles(ctx, db, dir)
	f.StaleFilesSeen = &staleCount

	if includeMaxMtime && maxMtime != nil {
		ts := maxMtime.UTC().Format(time.RFC3339)
		f.MaxFileMtimeSinceScan = &ts
	}

	if ws != nil {
		watching, watchSince := ws.Get()
		if watching {
			f.Watching = &watching
			ts := watchSince.UTC().Format(time.RFC3339)
			f.WatchSince = &ts
		}
	}

	return f
}

func countStaleFiles(ctx context.Context, db *sql.DB, dir string) (int, *time.Time) {
	const q = `SELECT path, indexed_at FROM sense_files`
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return 0, nil
	}
	defer func() { _ = rows.Close() }()

	var stale int
	var maxMtime *time.Time

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
		mtime := info.ModTime()
		if mtime.After(indexedAt) {
			stale++
		}
		if maxMtime == nil || mtime.After(*maxMtime) {
			maxMtime = &mtime
		}
	}
	return stale, maxMtime
}

func readMeta(ctx context.Context, db *sql.DB, key string) string {
	var value string
	err := db.QueryRowContext(ctx, "SELECT value FROM sense_meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}

