// Package mcpserver implements the `sense mcp` stdio server that
// exposes graph, blast, and status tools over the Model Context
// Protocol. Built on github.com/mark3labs/mcp-go — the de-facto Go
// MCP SDK. Each handler is a thin wrapper around the same engine code
// the CLI commands call, marshalled through internal/mcpio.
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
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

const serverInstructions = "When Sense is available and indexed, prefer Sense tools over grep, glob, " +
	"and file-walking agents for structural and semantic code questions. " +
	"Sense provides pre-indexed results that are faster and more complete.\n\n" +
	"WHEN TO USE SENSE TOOLS:\n" +
	"- Symbol relationships, callers, dependencies → sense.graph\n" +
	"- \"What would break if I changed X?\", impact analysis → sense.blast\n" +
	"- Conceptual/semantic code search (not exact string match) → sense.search\n" +
	"- Project patterns and conventions → sense.conventions\n" +
	"- Index health, what's indexed → sense.status\n\n" +
	"WHEN NOT TO USE SENSE TOOLS:\n" +
	"- Exact text/string search → use grep\n" +
	"- Reading file contents → use your file reading tool\n" +
	"- Editing code → Sense is read-only"

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
			Embed:             true,
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
	var hasDebt bool

	embeddingsEnabled := cli.EmbeddingsEnabled(dir)
	if embeddingsEnabled {
		hnswPath := filepath.Join(dir, ".sense", "hnsw.bin")
		idx, loadErr := search.LoadHNSWIndex(hnswPath)
		if loadErr == nil && idx != nil {
			vectorIdx = idx
		} else {
			embeddings, err := adapter.LoadEmbeddings(ctx)
			if err != nil {
				return fmt.Errorf("sense mcp: load embeddings: %w", err)
			}
			if len(embeddings) > 0 {
				vectorIdx = search.BuildHNSWIndex(embeddings)
			}
		}

		debtCount, _ := adapter.EmbeddingDebtCount(ctx)
		hasDebt = debtCount > 0

		if (vectorIdx != nil && vectorIdx.Len() > 0) || hasDebt {
			embedder, err = embed.NewBundledEmbedder(0)
			if err != nil {
				return fmt.Errorf("sense mcp: init embedder: %w", err)
			}
			defer func() { _ = embedder.Close() }()
		}
	}

	engine := search.NewEngine(adapter, vectorIdx, embedder)

	embedCtx, cancelEmbed := context.WithCancel(ctx)
	defer cancelEmbed()

	if embeddingsEnabled && hasDebt && opts.WatchState == nil {
		senseDir := filepath.Join(dir, ".sense")
		go func() {
			n, err := scan.EmbedPending(embedCtx, adapter, dir, senseDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "sense mcp: background embed failed: %v\n", err)
				return
			}
			if n == 0 {
				return
			}
			hnswPath := filepath.Join(senseDir, "hnsw.bin")
			newIdx, loadErr := search.LoadHNSWIndex(hnswPath)
			if loadErr != nil {
				fmt.Fprintf(os.Stderr, "sense mcp: load hnsw after embed: %v\n", loadErr)
				return
			}
			engine.SetVectors(newIdx)
			fmt.Fprintf(os.Stderr, "sense mcp: embeddings complete (%d symbols) — search upgraded to hybrid mode\n", n)
		}()
	}

	tracker := metrics.NewTracker(adapter.DB())
	defer tracker.Close()

	s := server.NewMCPServer(
		"sense",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithInstructions(serverInstructions),
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
		mcp.WithDescription("Find symbols by semantic and keyword matching across all indexed code. "+
			"Use this instead of grep when the question is about concepts, functionality, or behavior — "+
			"not exact strings. Returns ranked symbols with file locations, kinds, and relevance scores, "+
			"without reading any source files into context."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Search",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Natural language description of what you're looking for, e.g. 'how does auth work', 'payment error handling', 'user validation'"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of results to return (default 10)"),
		),
		mcp.WithString("language",
			mcp.Description("Filter results to a specific language, e.g. 'ruby', 'go', 'typescript'"),
		),
		mcp.WithNumber("min_score",
			mcp.Description("Minimum relevance score threshold 0.0–1.0 (default 0.0). Raise to filter weak matches."),
		),
	)
}

func graphTool() mcp.Tool {
	return mcp.NewTool("sense.graph",
		mcp.WithDescription("Look up the structural relationships of a symbol: callers, callees, "+
			"inheritance, composition, includes, imports, and test coverage. "+
			"Use this instead of grep or file reading when the user asks about relationships, dependencies, "+
			"callers, or how a symbol connects to the rest of the codebase. "+
			"Returns a pre-computed graph from the Sense index with no context window cost for file contents.\n\n"+
			"When dead_code is true, returns project-wide dead symbols (zero incoming references) instead of "+
			"per-symbol edges. The symbol, direction, and depth parameters are ignored in this mode."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Graph",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Description("Qualified or unqualified symbol name, e.g. 'User', 'Checkout::Order', 'HandleRequest'. Ignored when dead_code is true."),
		),
		mcp.WithNumber("depth",
			mcp.Description("How many hops to traverse from the symbol (default 1)"),
		),
		mcp.WithString("direction",
			mcp.Description("Which edges to follow: 'both' (default), 'callers' (who calls this), or 'callees' (what this calls)"),
			mcp.Enum("both", "callers", "callees"),
		),
		mcp.WithBoolean("dead_code",
			mcp.Description("When true, return project-wide dead symbols instead of per-symbol edges. Symbol, direction, and depth are ignored."),
		),
		mcp.WithString("language",
			mcp.Description("Filter dead code results to a specific language, e.g. 'go', 'ruby'. Only used when dead_code is true."),
		),
		mcp.WithString("domain",
			mcp.Description("Filter dead code results to a path substring, e.g. 'services', 'models'. Only used when dead_code is true."),
		),
	)
}

func blastTool() mcp.Tool {
	return mcp.NewTool("sense.blast",
		mcp.WithDescription("Compute what would break if a symbol or diff changed. "+
			"Follows the chain of callers and dependents multiple hops deep, including affected tests. "+
			"Use this instead of manually tracing callers when the user asks about impact, risk, "+
			"safe-to-change analysis, or what would break. Accepts a symbol name or a git ref for "+
			"diff-based analysis. Returns affected files, symbols, and test coverage with confidence scores."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Blast Radius",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("symbol",
			mcp.Description("Symbol to analyze, e.g. 'User', 'Checkout::Order'. Mutually exclusive with diff."),
		),
		mcp.WithString("diff",
			mcp.Description("Git ref for diff-based blast, e.g. 'HEAD~1', 'main..feature'. Mutually exclusive with symbol."),
		),
		mcp.WithNumber("max_hops",
			mcp.Description("How many dependency hops to follow (default 3). Higher values find more distant impacts."),
		),
		mcp.WithNumber("min_confidence",
			mcp.Description("Minimum edge confidence 0.0–1.0 (default 0.7). Lower values include weaker relationships."),
		),
		mcp.WithBoolean("include_tests",
			mcp.Description("Include affected test files in the results (default true)"),
		),
	)
}

func statusTool() mcp.Tool {
	return mcp.NewTool("sense.status",
		mcp.WithDescription("Check Sense index health and coverage. "+
			"Returns file, symbol, edge, and embedding counts, language breakdown by tier, "+
			"index freshness, and cumulative session metrics. "+
			"Use this to verify what is indexed and whether the index is stale. "+
			"Also useful for reporting how Sense has been used in the current session."),
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
	if req.GetBool("dead_code", false) {
		return h.handleDeadCode(ctx, req)
	}

	symbol := req.GetString("symbol", "")
	if symbol == "" {
		return mcp.NewToolResultError("sense.graph: missing required parameter 'symbol'"), nil
	}

	depth := req.GetInt("depth", 1)
	if depth != 1 {
		return mcp.NewToolResultError("sense.graph: --depth > 1 is not yet supported"), nil
	}

	direction := req.GetString("direction", "both")

	match, err := h.resolveSymbol(ctx, "sense.graph", symbol)
	if err != nil {
		if re, ok := err.(*resolveError); ok {
			return re.result, nil
		}
		return nil, err
	}

	sc, err := h.adapter.ReadSymbol(ctx, match.ID)
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
	resp.NextSteps = graphHints(resp, direction)

	out, err := mcpio.MarshalGraph(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.graph: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func graphHints(resp mcpio.GraphResponse, direction string) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if len(resp.Edges.CalledBy) >= 5 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.blast",
			Args:   map[string]any{"symbol": resp.Symbol.Qualified},
			Reason: fmt.Sprintf("%d callers found — check blast radius before changing this symbol", len(resp.Edges.CalledBy)),
		})
	} else if len(resp.Edges.CalledBy) == 0 && !isTestFile(resp.Symbol.File) {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.search",
			Args:   map[string]any{"query": resp.Symbol.Name},
			Reason: "no callers found in graph — search for dynamic references",
		})
	}

	if direction == "callers" && len(hints) < 2 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.graph",
			Args:   map[string]any{"symbol": resp.Symbol.Qualified, "direction": "callees"},
			Reason: "see what this symbol depends on",
		})
	}

	if len(hints) > 2 {
		hints = hints[:2]
	}
	return hints
}

func (h *handlers) handleDeadCode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	language := req.GetString("language", "")
	domain := req.GetString("domain", "")

	result, err := dead.FindDead(ctx, h.db, dead.Options{
		Language: language,
		Domain:   domain,
	})
	if err != nil {
		return nil, fmt.Errorf("sense.graph dead_code: %w", err)
	}

	rolled := dead.Rollup(result.Dead)
	resp := mcpio.BuildDeadCodeResponse(rolled, result.TotalSymbols)

	h.tracker.Record("sense.graph", "dead_code",
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	resp.NextSteps = deadCodeHints(resp)

	out, err := mcpio.MarshalDeadCode(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.graph dead_code: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func deadCodeHints(resp mcpio.DeadCodeResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.DeadCount > 0 && len(resp.DeadSymbols) > 0 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.graph",
			Args:   map[string]any{"symbol": resp.DeadSymbols[0].Qualified},
			Reason: "inspect the top dead symbol's relationships to confirm it's truly unused",
		})
	}

	return hints
}

func isTestFile(path string) bool {
	return strings.Contains(path, "_test.") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/spec/") ||
		strings.HasPrefix(path, "test/") ||
		strings.HasPrefix(path, "tests/") ||
		strings.HasPrefix(path, "spec/")
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

	results, meta, err := h.search.Search(ctx, search.Options{
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
		Results:    entries,
		SearchMode: meta.Mode,
		FusionWeights: mcpio.FusionWeights{
			Keyword: meta.KeywordWeight,
			Vector:  meta.VectorWeight,
		},
		SenseMetrics: mcpio.SearchMetrics{
			SymbolsSearched:           meta.SymbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
	h.tracker.Record("sense.search", query,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	resp.NextSteps = searchHints(resp)

	out, err := mcpio.MarshalSearch(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.search: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func searchHints(resp mcpio.SearchResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if len(resp.Results) > 0 && float64(resp.Results[0].Score) >= 0.8 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.graph",
			Args:   map[string]any{"symbol": resp.Results[0].Symbol},
			Reason: "strong match — explore its relationships",
		})
	}

	if len(hints) < 2 {
		fileCounts := map[string]int{}
		for _, r := range resp.Results {
			if r.File != "" {
				fileCounts[r.File]++
			}
		}
		for _, r := range resp.Results {
			if r.File != "" && fileCounts[r.File] >= 3 {
				hints = append(hints, mcpio.NextStep{
					Tool:   "sense.conventions",
					Args:   map[string]any{"domain": filepath.Dir(r.File)},
					Reason: "cluster of related symbols — check conventions in this area",
				})
				break
			}
		}
	}

	if len(hints) > 2 {
		hints = hints[:2]
	}
	return hints
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
			if re, ok := err.(*resolveError); ok {
				return re.result, nil
			}
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
	resp.NextSteps = blastHints(resp)

	out, err := mcpio.MarshalBlast(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.blast: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func blastHints(resp mcpio.BlastResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.Risk == "high" {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.conventions",
			Reason: "high blast radius — check conventions before changing",
		})
	}

	if len(resp.AffectedTests) == 0 && resp.TotalAffected > 0 && len(hints) < 2 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.search",
			Args:   map[string]any{"query": resp.Symbol + " test"},
			Reason: "affected symbols have no test coverage — search for related tests",
		})
	}

	if len(hints) > 2 {
		hints = hints[:2]
	}
	return hints
}

type toolError struct{ msg string }

func (e *toolError) Error() string { return e.msg }

type resolveError struct{ result *mcp.CallToolResult }

func (e *resolveError) Error() string { return "resolve: unresolved symbol" }

// disambiguationCap limits the number of candidates in a disambiguation
// response. Enough for the LLM to pick without overwhelming the context.
const disambiguationCap = 10

// resolveSymbol runs Lookup and returns the single resolved match.
// When the symbol is not found, ambiguous, or only fuzzy-matched, it
// returns a resolveError whose result field carries a pre-built
// *mcp.CallToolResult with structured JSON for the LLM.
func (h *handlers) resolveSymbol(ctx context.Context, tool, symbol string) (cli.Match, error) {
	matches, err := cli.Lookup(ctx, h.db, symbol)
	if err != nil {
		return cli.Match{}, fmt.Errorf("%s: lookup: %w", tool, err)
	}

	if len(matches) == 0 {
		return cli.Match{}, &resolveError{notFoundResult(symbol)}
	}

	if len(matches) == 1 && matches[0].Resolution != cli.ResFuzzy {
		return matches[0], nil
	}

	if matches[0].Resolution == cli.ResFuzzy {
		return cli.Match{}, &resolveError{suggestionsResult(symbol, matches)}
	}

	if winner, ok := h.dominantMatch(ctx, matches); ok {
		return winner, nil
	}

	return cli.Match{}, &resolveError{h.disambiguationResult(ctx, symbol, matches)}
}

// dominantMatch returns the single best match when one candidate has
// overwhelmingly more edges than all others — e.g. Topic(107 edges)
// vs Topic(1 edge) vs Topic(0 edges). The top candidate must have at
// least 5× the runner-up's edge count (and the runner-up must have
// ≤ 2 edges) to auto-resolve. This prevents the LLM from entering a
// retry loop when the disambiguation hint is circular (top match
// qualified == query).
func (h *handlers) dominantMatch(ctx context.Context, matches []cli.Match) (cli.Match, bool) {
	ids := make([]int64, len(matches))
	for i := range matches {
		ids[i] = matches[i].ID
	}
	counts, err := cli.EdgeCounts(ctx, h.db, ids)
	if err != nil {
		return cli.Match{}, false
	}

	type ranked struct {
		match cli.Match
		edges int
	}
	items := make([]ranked, len(matches))
	for i, m := range matches {
		items[i] = ranked{match: m, edges: counts[m.ID]}
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].edges > items[j].edges
	})

	if len(items) < 2 {
		return cli.Match{}, false
	}
	top := items[0].edges
	runnerUp := items[1].edges
	if top >= 5 && runnerUp <= 2 && top >= runnerUp*5 {
		return items[0].match, true
	}
	return cli.Match{}, false
}

func notFoundResult(symbol string) *mcp.CallToolResult {
	resp := map[string]any{
		"error": "symbol not found",
		"query": symbol,
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultError(string(out))
}

func suggestionsResult(symbol string, matches []cli.Match) *mcp.CallToolResult {
	suggestions := make([]string, 0, len(matches))
	for _, m := range matches {
		if m.Qualified != m.Name {
			suggestions = append(suggestions, fmt.Sprintf("%s (%s)", m.Qualified, m.Kind))
		} else {
			suggestions = append(suggestions, fmt.Sprintf("%s (%s) %s", m.Name, m.Kind, m.File))
		}
	}
	resp := map[string]any{
		"error":       "symbol not found",
		"query":       symbol,
		"suggestions": suggestions,
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(out))
}

func (h *handlers) disambiguationResult(ctx context.Context, symbol string, matches []cli.Match) *mcp.CallToolResult {
	ids := make([]int64, len(matches))
	for i := range matches {
		ids[i] = matches[i].ID
	}
	edgeCounts, err := cli.EdgeCounts(ctx, h.db, ids)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sense: edge count query failed: %v\n", err)
		edgeCounts = map[int64]int{}
	}

	type ranked struct {
		match cli.Match
		edges int
	}
	items := make([]ranked, len(matches))
	for i, m := range matches {
		items[i] = ranked{match: m, edges: edgeCounts[m.ID]}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].edges != items[j].edges {
			return items[i].edges > items[j].edges
		}
		return items[i].match.Qualified < items[j].match.Qualified
	})

	limit := disambiguationCap
	if limit > len(items) {
		limit = len(items)
	}
	topMatches := make([]string, limit)
	for i := 0; i < limit; i++ {
		m := items[i]
		topMatches[i] = fmt.Sprintf("%s (%d edges, %s) %s:%d",
			m.match.Qualified, m.edges, m.match.Kind, m.match.File, m.match.LineStart)
	}

	hint := ""
	if len(topMatches) > 0 {
		if items[0].match.Qualified == symbol {
			hint = fmt.Sprintf("Multiple symbols named %q exist — pick the one in the right file from top_matches above", symbol)
		} else {
			hint = fmt.Sprintf("Refine with a qualified name, e.g. %q", items[0].match.Qualified)
		}
	}

	resp := map[string]any{
		"ambiguous":   true,
		"query":       symbol,
		"matches":     len(matches),
		"resolution":  string(matches[0].Resolution),
		"top_matches": topMatches,
		"hint":        hint,
	}
	out, _ := json.MarshalIndent(resp, "", "  ")
	return mcp.NewToolResultText(string(out))
}

func (h *handlers) blastSymbol(ctx context.Context, symbol string, opts blast.Options) (mcpio.BlastResponse, error) {
	match, err := h.resolveSymbol(ctx, "sense.blast", symbol)
	if err != nil {
		return mcpio.BlastResponse{}, err
	}

	siblingIDs, err := blast.SiblingSymbolIDs(ctx, h.db, match.ID)
	if err != nil {
		siblingIDs = []int64{match.ID}
	}

	blastResult, err := blast.Compute(ctx, h.db, siblingIDs, opts)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: compute: %w", err)
	}

	fileIDs := cli.CollectBlastFileIDs(blastResult)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense.blast: load file paths: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	return mcpio.BuildBlastResponse(blastResult, lookup), nil
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
		r, err := blast.Compute(ctx, h.db, []int64{sid}, opts)
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
		mcp.WithDescription("Detect project conventions and recurring patterns: inheritance hierarchies, "+
			"naming conventions, structural patterns, composition styles, and testing approaches. "+
			"Use this instead of reading multiple files to understand how existing code is structured "+
			"or what patterns to follow when writing new code. "+
			"Returns conventions with strength scores and instance counts, scoped by domain if specified."),
		mcp.WithToolAnnotation(mcp.ToolAnnotation{
			Title:           "Sense Conventions",
			ReadOnlyHint:    mcp.ToBoolPtr(true),
			DestructiveHint: mcp.ToBoolPtr(false),
			IdempotentHint:  mcp.ToBoolPtr(true),
			OpenWorldHint:   mcp.ToBoolPtr(false),
		}),
		mcp.WithString("domain",
			mcp.Description("Filter conventions by domain, e.g. 'models', 'controllers', 'services', 'test'. Matches path substrings."),
		),
		mcp.WithNumber("min_strength",
			mcp.Description("Minimum convention strength 0.0–1.0 (default 0.3). Lower to see weaker patterns, raise to see only strong, well-established ones."),
		),
	)
}

func (h *handlers) handleConventions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	domain := req.GetString("domain", "")
	minStrength := req.GetFloat("min_strength", 0.3)

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
			Category:       string(c.Category),
			Description:    c.Description,
			Strength:       mcpio.Confidence(c.Strength),
			Instances:      conventions.PickRepresentatives(c.Examples, 3),
			TotalInstances: c.Instances,
		}
	}

	mcpio.ApplyTokenBudget(&resp, mcpio.DefaultTokenBudget)
	mcpio.BuildConventionsSummary(&resp)

	h.tracker.Record("sense.conventions", domain,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved)

	resp.NextSteps = conventionsHints(resp, domain)

	out, err := mcpio.MarshalConventions(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.conventions: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func conventionsHints(resp mcpio.ConventionsResponse, domain string) []mcpio.NextStep {
	var hints []mcpio.NextStep

	for _, c := range resp.Conventions {
		if float64(c.Strength) >= 0.8 {
			hints = append(hints, mcpio.NextStep{
				Tool:   "sense.search",
				Args:   map[string]any{"query": c.Description},
				Reason: "strong convention — search for all instances",
			})
			break
		}
	}

	if domain != "" && len(hints) < 2 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.conventions",
			Reason: "scoped results — run without domain filter for project-wide patterns",
		})
	}

	if len(hints) > 2 {
		hints = hints[:2]
	}
	return hints
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

	resp.NextSteps = statusHints(resp, sess.Queries)

	out, err := mcpio.MarshalStatus(resp)
	if err != nil {
		return nil, fmt.Errorf("sense.status: marshal: %w", err)
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

	if sessionQueries == 0 && len(hints) < 2 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense.conventions",
			Reason: "start of session — check project conventions",
		})
	}

	if len(hints) > 2 {
		hints = hints[:2]
	}
	return hints
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

	if debt := resp.Index.Symbols - resp.Index.Embeddings; debt > 0 {
		pct := 0
		if resp.Index.Symbols > 0 {
			pct = resp.Index.Embeddings * 100 / resp.Index.Symbols
		}
		resp.EmbeddingProgress = &mcpio.EmbeddingProgress{
			Total:    resp.Index.Symbols,
			Embedded: resp.Index.Embeddings,
			Percent:  pct,
		}
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

	structure, err := buildStructure(ctx, db, resp, langs)
	if err != nil {
		return resp, err
	}
	resp.Structure = structure

	return resp, nil
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
	fp := buildFingerprint(resp, langs, ns, hubs)
	return &mcpio.StatusStructure{
		TopNamespaces: ns,
		HubSymbols:    hubs,
		EntryPoints:   entries,
		Fingerprint:   fp,
	}, nil
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
	const q = `SELECT s.name, COUNT(e.id) AS in_degree, s.kind
	FROM sense_symbols s
	JOIN sense_edges e ON e.target_id = s.id
	GROUP BY s.id
	ORDER BY in_degree DESC
	LIMIT 5`

	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []mcpio.StatusHub
	for rows.Next() {
		var h mcpio.StatusHub
		if err := rows.Scan(&h.Name, &h.Callers, &h.Kind); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
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

func buildFingerprint(resp mcpio.StatusResponse, langs map[string]mcpio.StatusLanguage, ns []mcpio.StatusNamespace, hubs []mcpio.StatusHub) string {
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

	// Hub symbol names
	hubNames := make([]string, 0, 3)
	for i, h := range hubs {
		if i >= 3 {
			break
		}
		hubNames = append(hubNames, h.Name)
	}

	parts := []string{
		fmt.Sprintf("%s project.", capitalizeFirst(primaryLang)),
		fmt.Sprintf("%d files, %d symbols.", resp.Index.Files, resp.Index.Symbols),
	}
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

