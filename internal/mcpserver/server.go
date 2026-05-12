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
	"sync"
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
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
)

const serverInstructions = mcpio.ServerInstructions

const (
	// Skip interface resolution when direct callers already provide sufficient evidence.
	InterfaceResolutionThreshold = 3
	// Interface-dispatch inferred callers are structural but indirect.
	DispatchInferredConfidence = 0.8
	// Caps to bound query-time work when an interface has many implementors.
	maxDispatchEquivalents = 20
	maxDispatchInferred    = 50

	// Response compaction thresholds (pitch 22-05).
	compactEdgeKeepCount  = 10 // keep full line details for first N edges
	snippetStripThreshold = 5  // strip snippets from results beyond this index
	keySymbolsLimit       = 12 // max key symbols in conventions response
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
	s, _, cleanup, err := buildMCPServer(opts)
	if err != nil {
		return err
	}
	defer cleanup()
	return server.ServeStdio(s)
}

// buildMCPServer creates the MCP server and handlers without starting stdio
// transport. Returns the server, handlers, a cleanup function, and any error.
func buildMCPServer(opts RunOptions) (*server.MCPServer, *handlers, func(), error) {
	dir := opts.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("sense mcp: getwd: %w", err)
		}
		dir = wd
	}

	ctx := context.Background()
	adapter, err := cli.OpenIndex(ctx, dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("sense mcp: %w", err)
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
			return nil, nil, nil, fmt.Errorf("sense mcp: rebuild scan: %w", err)
		}
		adapter, err = cli.OpenIndex(ctx, dir)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("sense mcp: reopen after rebuild: %w", err)
		}
		fmt.Fprintf(os.Stderr, "sense mcp: rebuild complete\n")
	}

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
				_ = adapter.Close()
				return nil, nil, nil, fmt.Errorf("sense mcp: load embeddings: %w", err)
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
				_ = adapter.Close()
				return nil, nil, nil, fmt.Errorf("sense mcp: init embedder: %w", err)
			}
		}
	}

	engine := search.NewEngine(adapter, vectorIdx, embedder)

	var reranker *embed.ONNXReranker
	if embedder != nil {
		r, err := embed.NewBundledReranker(0)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sense mcp: init reranker: %v\n", err)
		} else {
			reranker = r
			engine.SetReranker(reranker)
		}
	}

	embedCtx, cancelEmbed := context.WithCancel(ctx)
	embedDone := make(chan struct{})

	if embeddingsEnabled && hasDebt && opts.WatchState == nil {
		senseDir := filepath.Join(dir, ".sense")
		go func() {
			defer close(embedDone)
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
	} else {
		close(embedDone)
	}

	tracker := metrics.NewTracker(adapter.DB())

	s := server.NewMCPServer(
		"sense",
		version.Version,
		server.WithToolCapabilities(false),
		server.WithInstructions(serverInstructions),
	)

	defaults := profile.DefaultParams()

	textFallback := search.NewTextFallback()

	h := &handlers{adapter: adapter, db: adapter.DB(), dir: dir, search: engine, textFallback: textFallback, watchState: opts.WatchState, tracker: tracker, defaults: defaults, seenSymbols: make(map[int64]bool)}

	s.AddTool(searchTool(), h.handleSearch)
	s.AddTool(graphTool(), h.handleGraph)
	s.AddTool(blastTool(), h.handleBlast)
	s.AddTool(conventionsTool(), h.handleConventions)
	s.AddTool(statusTool(), h.handleStatus)

	cleanup := func() {
		cancelEmbed()
		<-embedDone
		if embedder != nil {
			_ = embedder.Close()
		}
		if reranker != nil {
			_ = reranker.Close()
		}
		tracker.Close()
		_ = adapter.Close()
	}

	return s, h, cleanup, nil
}

// handlers holds shared state for MCP tool handlers. The adapter is
// kept for methods like ReadSymbol that live on *sqlite.Adapter; db
// is a convenience alias for plain-SQL callers (Lookup, LoadFilePaths).
type handlers struct {
	adapter      *sqlite.Adapter
	db           *sql.DB
	dir          string
	search       *search.Engine
	textFallback *search.TextFallback
	watchState   *mcpio.WatchState
	tracker      *metrics.Tracker
	defaults     profile.Defaults
	seenSymbols  map[int64]bool
	seenMu       sync.Mutex
}

// ---------------------------------------------------------------
// Tool schemas
// ---------------------------------------------------------------

func searchTool() mcp.Tool {
	return mcp.NewTool("sense_search",
		mcp.WithDescription("Find symbols by semantic and keyword matching across all indexed code. "+
			"Use this instead of grep when the question is about concepts, functionality, or behavior — "+
			"not exact strings. Also useful for exploring architecture by searching broad concepts "+
			"(e.g., 'routing', 'middleware', 'database'). Returns ranked symbols with file locations, "+
			"kinds, and relevance scores, without reading any source files into context."),
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
	return mcp.NewTool("sense_graph",
		mcp.WithDescription("Look up the structural relationships of a symbol: callers, callees, "+
			"inheritance, composition, includes, imports, and test coverage. "+
			"Use this instead of grep or file reading when the user asks about relationships, dependencies, "+
			"callers, or how a symbol connects to the rest of the codebase. "+
			"Returns a pre-computed graph from the Sense index with no context window cost for file contents. "+
			"For symbols called through interfaces or traits, dispatch-inferred callers appear in a separate "+
			"dispatch_inferred field (confidence 0.8).\n\n"+
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
			mcp.Description("When true, return project-wide dead symbols instead of per-symbol edges. Symbol, direction, and depth are ignored. Test-only references are excluded by default (symbols only called from test files are reported as dead)."),
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
	return mcp.NewTool("sense_blast",
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
	return mcp.NewTool("sense_status",
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
// sense_graph handler
// ---------------------------------------------------------------

func (h *handlers) handleGraph(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if req.GetBool("dead_code", false) {
		return h.handleDeadCode(ctx, req)
	}

	symbol := req.GetString("symbol", "")
	if symbol == "" {
		return mcp.NewToolResultError("sense_graph: missing required parameter 'symbol'"), nil
	}

	direction := model.Direction(req.GetString("direction", "both"))
	switch direction {
	case model.DirectionBoth, model.DirectionCallers, model.DirectionCallees:
	default:
		return mcp.NewToolResultError(fmt.Sprintf("sense_graph: direction must be both, callers, or callees (got %q)", direction)), nil
	}

	defaultDepth := 1
	if direction == model.DirectionCallers {
		defaultDepth = 2
	}
	depth := req.GetInt("depth", defaultDepth)
	if depth < 1 {
		depth = 1
	}
	if depth > mcpio.MaxGraphDepth {
		return mcp.NewToolResultError(fmt.Sprintf("sense_graph: depth %d exceeds maximum of %d", depth, mcpio.MaxGraphDepth)), nil
	}

	match, err := h.resolveSymbol(ctx, "sense_graph", symbol)
	if err != nil {
		if re, ok := err.(*resolveError); ok {
			return re.result, nil
		}
		return nil, err
	}

	gr, err := h.adapter.ReadSymbolGraph(ctx, match.ID, depth, direction, mcpio.MaxPerHop)
	if err != nil {
		return nil, fmt.Errorf("sense_graph: read symbol: %w", err)
	}

	fileIDs := cli.CollectGraphFileIDs(gr)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("sense_graph: load file paths: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	buildReq := mcpio.BuildGraphRequest{
		Direction:      direction,
		SegmentCallers: h.defaults.GraphSegmentCallers,
	}
	resp := mcpio.BuildFullGraphResponse(gr, lookup, buildReq)

	if direction != model.DirectionCallees {
		inferred := h.resolveDispatchCallers(ctx, &gr.Root, &resp, lookup)
		if len(inferred) > 0 {
			resp.DispatchInferred = inferred
		}
	}

	if len(resp.Edges.CalledBy) > compactEdgeKeepCount {
		compactCallEdges(resp.Edges.CalledBy)
	}
	if len(resp.Edges.Calls) > compactEdgeKeepCount {
		compactCallEdges(resp.Edges.Calls)
	}
	if len(resp.DispatchInferred) > compactEdgeKeepCount {
		compactDispatchInferred(resp.DispatchInferred)
	}

	h.seenMu.Lock()
	h.seenSymbols[gr.Root.Symbol.ID] = true
	h.seenMu.Unlock()

	h.tracker.Record("sense_graph", symbol,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	if len(resp.Edges.CalledBy) > 10 {
		resp.CoverageNote = "Graph edges from indexed source files — may miss callers in examples/, scripts/, or macro-generated code"
	}

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState)
	resp.Freshness = freshness
	resp.NextSteps = graphHints(resp, direction)

	out, err := mcpio.MarshalGraph(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_graph: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func (h *handlers) resolveDispatchCallers(ctx context.Context, root *model.SymbolContext, resp *mcpio.GraphResponse, lookup mcpio.FileLookup) []mcpio.DispatchInferredRef {
	sym := root.Symbol
	if sym.Kind != model.KindMethod || sym.ParentID == nil {
		return nil
	}
	if len(resp.Edges.CalledBy) >= InterfaceResolutionThreshold {
		return nil
	}

	equivIDs, err := sqlite.DispatchMethodIDs(ctx, h.db, sym.ID)
	if err != nil || len(equivIDs) == 0 {
		return nil
	}
	if len(equivIDs) > maxDispatchEquivalents {
		equivIDs = equivIDs[:maxDispatchEquivalents]
	}

	directCallers := make(map[string]struct{}, len(resp.Edges.CalledBy))
	for _, c := range resp.Edges.CalledBy {
		directCallers[c.Symbol] = struct{}{}
	}

	var inferred []mcpio.DispatchInferredRef
	for _, eqID := range equivIDs {
		if len(inferred) >= maxDispatchInferred {
			break
		}
		eqSym, err := h.adapter.ReadSymbol(ctx, eqID)
		if err != nil {
			continue
		}
		via := qualifiedOrNameRef(eqSym.Symbol)

		for _, e := range eqSym.Inbound {
			if e.Edge.Kind != model.EdgeCalls {
				continue
			}
			if len(inferred) >= maxDispatchInferred {
				break
			}
			callerName := qualifiedOrNameRef(e.Target)
			if _, dup := directCallers[callerName]; dup {
				continue
			}
			directCallers[callerName] = struct{}{}

			var filePath *string
			if p, ok := lookup(e.Target.FileID); ok {
				filePath = &p
			}
			inferred = append(inferred, mcpio.DispatchInferredRef{
				Symbol:     callerName,
				File:       filePath,
				LineStart:  e.Target.LineStart,
				LineEnd:    e.Target.LineEnd,
				Ref:        mcpio.FormatRefPtr(filePath, e.Target.LineStart),
				Via:        via,
				Confidence: mcpio.Confidence(DispatchInferredConfidence),
			})
		}
	}
	return inferred
}

func qualifiedOrNameRef(s model.Symbol) string {
	if s.Qualified != "" {
		return s.Qualified
	}
	return s.Name
}

func compactCallEdges(edges []mcpio.CallEdgeRef) {
	for i := compactEdgeKeepCount; i < len(edges); i++ {
		edges[i].LineStart = 0
		edges[i].LineEnd = 0
	}
}

func compactDispatchInferred(edges []mcpio.DispatchInferredRef) {
	for i := compactEdgeKeepCount; i < len(edges); i++ {
		edges[i].LineStart = 0
		edges[i].LineEnd = 0
	}
}

func graphHints(resp mcpio.GraphResponse, direction model.Direction) []mcpio.NextStep {
	var hints []mcpio.NextStep

	totalCallers := len(resp.Edges.CalledBy)
	if resp.TestCallerSummary != nil {
		totalCallers += resp.TestCallerSummary.Count
	}
	if totalCallers >= 5 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_blast",
			Args:   map[string]any{"symbol": resp.Symbol.Qualified},
			Reason: fmt.Sprintf("%d callers found — check blast radius before changing this symbol", totalCallers),
		})
	} else if totalCallers == 0 && !isTestFile(resp.Symbol.File) {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_search",
			Args:   map[string]any{"query": resp.Symbol.Name},
			Reason: "no callers found in graph — search for dynamic references",
		})
	}

	if direction == model.DirectionCallers && len(hints) < mcpio.MaxNextSteps {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_graph",
			Args:   map[string]any{"symbol": resp.Symbol.Qualified, "direction": "callees"},
			Reason: "see what this symbol depends on",
		})
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
	}
	return hints
}

func (h *handlers) handleDeadCode(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	language := req.GetString("language", "")
	domain := req.GetString("domain", "")

	result, err := dead.FindDead(ctx, h.db, dead.Options{
		Language:        language,
		Domain:          domain,
		ExcludeTestRefs: true,
	})
	if err != nil {
		return nil, fmt.Errorf("sense_graph dead_code: %w", err)
	}

	rolled := dead.Rollup(result.Dead)
	resp := mcpio.BuildDeadCodeResponse(rolled, result.TotalSymbols)
	resp.CoverageNote = "Static analysis — does not trace dynamic dispatch, decorator registration, or external API consumers"

	h.tracker.Record("sense_graph", "dead_code",
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	resp.NextSteps = deadCodeHints(resp)

	out, err := mcpio.MarshalDeadCode(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_graph dead_code: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func deadCodeHints(resp mcpio.DeadCodeResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.DeadCount > 0 && len(resp.DeadSymbols) > 0 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_graph",
			Args:   map[string]any{"symbol": resp.DeadSymbols[0].Qualified},
			Reason: "inspect the top dead symbol's relationships to confirm it's truly unused",
		})
	}

	return hints
}

func isTestFile(path string) bool {
	return mcpio.IsTestPath(path)
}

// ---------------------------------------------------------------
// sense_search handler
// ---------------------------------------------------------------

func (h *handlers) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("sense_search: missing required parameter 'query'"), nil
	}

	limit := req.GetInt("limit", 10)
	language := req.GetString("language", "")
	minScore := req.GetFloat("min_score", 0.0)

	keywordBias := h.defaults.SearchKeywordWeight - 0.5
	if keywordBias < 0 {
		keywordBias = 0
	}
	results, meta, err := h.search.Search(ctx, search.Options{
		Query:       query,
		Limit:       limit,
		Language:    language,
		MinScore:    minScore,
		KeywordBias: keywordBias,
	})
	if err != nil {
		return nil, fmt.Errorf("sense_search: %w", err)
	}

	fileIDs := make([]int64, len(results))
	for i, r := range results {
		fileIDs[i] = r.FileID
	}
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return nil, fmt.Errorf("sense_search: load file paths: %w", err)
	}

	entries := make([]mcpio.SearchResultEntry, len(results))
	uniqueFiles := map[string]struct{}{}
	for i, r := range results {
		path := pathByID[r.FileID]
		entries[i] = mcpio.SearchResultEntry{
			Symbol:     r.Qualified,
			File:       path,
			Line:       r.LineStart,
			Kind:       r.Kind,
			Score:      mcpio.SearchScore(r.Score),
			Snippet:    r.Snippet,
			References: r.References,
			Source:     "structural",
		}
		h.seenMu.Lock()
		if h.seenSymbols[r.SymbolID] {
			entries[i].Seen = true
			entries[i].Snippet = ""
		}
		if i >= snippetStripThreshold {
			entries[i].Snippet = ""
		}
		h.seenSymbols[r.SymbolID] = true
		h.seenMu.Unlock()
		if path != "" {
			uniqueFiles[path] = struct{}{}
		}
	}

	textFallbackFired := false
	if h.textFallback != nil && h.textFallback.Available() && len(entries) < limit {
		textResults := h.textFallback.Search(ctx, query, h.dir, []string{"."}, limit)
		matches := make([]mcpio.TextMatch, len(textResults))
		for i, tr := range textResults {
			matches[i] = mcpio.TextMatch{File: tr.File, Line: tr.Line, Match: tr.Match}
		}
		textEntries, fired := mcpio.ConvertTextResults(matches, entries)
		if fired {
			entries = append(entries, textEntries...)
			for _, e := range textEntries {
				uniqueFiles[e.File] = struct{}{}
			}
			textFallbackFired = true
		}
	}

	searchMode := meta.Mode
	if textFallbackFired {
		searchMode += "+text"
	}

	filesAvoided := len(uniqueFiles)
	resp := mcpio.SearchResponse{
		Results:    entries,
		SearchMode: searchMode,
		FusionWeights: mcpio.FusionWeights{
			Keyword: meta.KeywordWeight,
			Vector:  meta.VectorWeight,
		},
		SenseMetrics: mcpio.SearchMetrics{
			SymbolsSearched:           meta.SymbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
			TextFallbackFired:         textFallbackFired,
		},
	}
	h.tracker.Record("sense_search", query,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, textFallbackFired)

	resp.NextSteps = searchHints(resp)

	out, err := mcpio.MarshalSearch(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_search: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func searchHints(resp mcpio.SearchResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if len(resp.Results) > 0 && float64(resp.Results[0].Score) >= 0.8 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_graph",
			Args:   map[string]any{"symbol": resp.Results[0].Symbol},
			Reason: "strong match — explore its relationships",
		})
	}

	if len(hints) < mcpio.MaxNextSteps {
		fileCounts := map[string]int{}
		for _, r := range resp.Results {
			if r.File != "" {
				fileCounts[r.File]++
			}
		}
		for _, r := range resp.Results {
			if r.File != "" && fileCounts[r.File] >= 3 {
				hints = append(hints, mcpio.NextStep{
					Tool:   "sense_conventions",
					Args:   map[string]any{"domain": filepath.Dir(r.File)},
					Reason: "cluster of related symbols — check conventions in this area",
				})
				break
			}
		}
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
	}
	return hints
}

// ---------------------------------------------------------------
// sense_blast handler
// ---------------------------------------------------------------

func (h *handlers) handleBlast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbol := req.GetString("symbol", "")
	diff := req.GetString("diff", "")

	if symbol == "" && diff == "" {
		return mcp.NewToolResultError("sense_blast: pass either 'symbol' or 'diff'"), nil
	}
	if symbol != "" && diff != "" {
		return mcp.NewToolResultError("sense_blast: pass either 'symbol' or 'diff', not both"), nil
	}

	maxHops := req.GetInt("max_hops", h.defaults.BlastMaxHops)
	minConfidence := req.GetFloat("min_confidence", h.defaults.BlastMinConfidence)
	includeTests := req.GetBool("include_tests", true)

	opts := blast.Options{
		MaxHops:       maxHops,
		MinConfidence: minConfidence,
		MaxResults:    h.defaults.BlastResultCap,
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
	resp.CoverageNote = "Follows static call edges — does not trace reflection, runtime plugins, or cross-repo consumers"

	h.tracker.Record("sense_blast", blastArgs,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState)
	resp.Freshness = freshness
	resp.NextSteps = blastHints(resp)

	out, err := mcpio.MarshalBlast(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_blast: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func blastHints(resp mcpio.BlastResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.Risk == "high" {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_conventions",
			Reason: "high blast radius — check conventions before changing",
		})
	}

	if resp.TestsAffectedCount == 0 && len(resp.AffectedTests) == 0 && resp.TotalAffected > 0 && len(hints) < mcpio.MaxNextSteps {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_search",
			Args:   map[string]any{"query": resp.Symbol + " test"},
			Reason: "affected symbols have no test coverage — search for related tests",
		})
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
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
	match, err := h.resolveSymbol(ctx, "sense_blast", symbol)
	if err != nil {
		return mcpio.BlastResponse{}, err
	}

	siblingIDs, err := blast.SiblingSymbolIDs(ctx, h.db, match.ID)
	if err != nil {
		siblingIDs = []int64{match.ID}
	}

	blastResult, err := blast.Compute(ctx, h.db, siblingIDs, opts)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: compute: %w", err)
	}

	if err := blast.RollupParents(ctx, h.db, &blastResult); err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: rollup parents: %w", err)
	}

	fileIDs := cli.CollectBlastFileIDs(blastResult)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: load file paths: %w", err)
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
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: %w", err)
	}

	symbolIDs, err := cli.SymbolsInFiles(ctx, h.db, paths)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: %w", err)
	}

	results := make([]blast.Result, 0, len(symbolIDs))
	for _, sid := range symbolIDs {
		r, err := blast.Compute(ctx, h.db, []int64{sid}, opts)
		if err != nil {
			return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: %w", err)
		}
		if err := blast.RollupParents(ctx, h.db, &r); err != nil {
			return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: rollup parents: %w", err)
		}
		results = append(results, r)
	}

	fileIDs := cli.CollectDiffFileIDs(results)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	return mcpio.BuildDiffBlastResponse(ref, results, lookup), nil
}

// ---------------------------------------------------------------
// sense_conventions handler
// ---------------------------------------------------------------

func conventionsTool() mcp.Tool {
	return mcp.NewTool("sense_conventions",
		mcp.WithDescription("Detect project conventions and recurring patterns: inheritance hierarchies, "+
			"naming conventions, structural patterns, composition styles, and testing approaches. "+
			"Use this instead of reading multiple files to understand how existing code is structured "+
			"or what patterns to follow when writing new code. Essential for codebase orientation — "+
			"reveals the architectural patterns that define how this project is built. "+
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
	minStrength := req.GetFloat("min_strength", h.defaults.ConventionsMinStrength)

	results, symbolCount, err := conventions.Detect(ctx, h.db, conventions.Options{
		Domain:      domain,
		MinStrength: minStrength,
	})
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: %w", err)
	}

	keyEntries, err := buildKeyEntries(ctx, h.adapter, domain, keySymbolsLimit)
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: key symbols: %w", err)
	}

	instanceCap := h.defaults.ConventionsInstanceCap
	filesAvoided := min(symbolCount/5, 30)
	resp := mcpio.ConventionsResponse{
		KeySymbols: keyEntries,
		SenseMetrics: mcpio.ConventionsMetrics{
			SymbolsAnalyzed:           symbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
	for _, c := range results {
		instances := conventions.PickRepresentatives(c.Examples, instanceCap)
		snippets := lookupInstanceSnippets(ctx, h.db, instances, 3)
		resp.Conventions = append(resp.Conventions, mcpio.ConventionEntry{
			Category:       string(c.Category),
			Description:    c.Description,
			Strength:       mcpio.Confidence(c.Strength),
			Instances:      instances,
			TotalInstances: c.Instances,
			KeySymbol:      c.KeySymbol,
			Snippets:       snippets,
		})
	}

	mcpio.ApplyTokenBudget(&resp, h.defaults.ConventionsTokenBudget)
	mcpio.BuildConventionsSummary(&resp)

	h.tracker.Record("sense_conventions", domain,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	resp.NextSteps = conventionsHints(resp, domain)

	out, err := mcpio.MarshalConventions(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_conventions: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

func conventionsHints(resp mcpio.ConventionsResponse, domain string) []mcpio.NextStep {
	var hints []mcpio.NextStep

	for _, c := range resp.Conventions {
		if float64(c.Strength) >= 0.8 {
			hints = append(hints, mcpio.NextStep{
				Tool:   "sense_search",
				Args:   map[string]any{"query": c.Description},
				Reason: "strong convention — search for all instances",
			})
			break
		}
	}

	if domain != "" && len(hints) < mcpio.MaxNextSteps {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_conventions",
			Reason: "scoped results — run without domain filter for project-wide patterns",
		})
	}

	if len(hints) > mcpio.MaxNextSteps {
		hints = hints[:mcpio.MaxNextSteps]
	}
	return hints
}

func buildKeyEntries(ctx context.Context, adapter *sqlite.Adapter, domain string, limit int) ([]mcpio.KeySymbolEntry, error) {
	keySymbols, err := adapter.TopSymbolsByReach(ctx, domain, limit)
	if err != nil {
		return nil, err
	}
	entries := make([]mcpio.KeySymbolEntry, 0, len(keySymbols))
	for _, ks := range keySymbols {
		callers, _ := adapter.TopCallers(ctx, ks.ID, 3)
		callerNames := make([]string, len(callers))
		for i, c := range callers {
			callerNames[i] = c.Qualified
		}
		entries = append(entries, mcpio.KeySymbolEntry{
			Name:       ks.Qualified,
			Kind:       ks.Kind,
			Snippet:    ks.Snippet,
			References: ks.RefFiles,
			Callers:    callerNames,
		})
	}
	return entries, nil
}

func lookupInstanceSnippets(ctx context.Context, db *sql.DB, instances []string, limit int) []string {
	if len(instances) == 0 {
		return nil
	}
	n := min(len(instances), limit)
	names := instances[:n]
	placeholders := strings.Repeat("?,", len(names))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT snippet FROM sense_symbols WHERE name IN (` + placeholders + `) AND snippet IS NOT NULL AND snippet != '' LIMIT ?`
	args := make([]any, len(names)+1)
	for i, name := range names {
		args[i] = name
	}
	args[len(names)] = limit
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if rows.Scan(&s) == nil && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------
// sense_status handler
// ---------------------------------------------------------------

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

	out, err := mcpio.MarshalStatus(resp)
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

