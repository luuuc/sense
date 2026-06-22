package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

const (
	// Skip interface resolution when direct callers already provide sufficient evidence.
	InterfaceResolutionThreshold = 3
	// Interface-dispatch inferred callers are structural but indirect.
	DispatchInferredConfidence = 0.8
	// Caps to bound query-time work when an interface has many implementors.
	maxDispatchEquivalents = 20
	maxDispatchInferred    = 50

	// compactEdgeKeepCount keeps full line details for the first N edges
	// (pitch 22-05 response compaction).
	compactEdgeKeepCount = 10
)

// graphParams is the validated, defaulted input to a sense_graph query.
type graphParams struct {
	symbol        string
	fileHint      string
	direction     model.Direction
	depth         int
	minConfidence float64
	contextLines  int
}

func (h *handlers) handleGraph(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if req.GetBool("dead_code", false) {
		return h.handleDeadCode(ctx, req)
	}

	params, errResult := parseGraphParams(req)
	if errResult != nil {
		return errResult, nil
	}

	// Read-repair: refresh any stale touched files before resolving, so a
	// just-edited symbol is visible even before the debounced watcher fires.
	snap := h.repairAndSnapshot(ctx)

	match, err := h.resolveSymbol(ctx, "sense_graph", params.symbol, params.fileHint)
	if err != nil {
		if re, ok := err.(*resolveError); ok {
			return re.result, nil
		}
		return nil, err
	}

	resp, gr, err := h.buildGraphResponse(ctx, match, params)
	if err != nil {
		return nil, err
	}

	h.shapeGraphResponse(ctx, &resp, gr, params, snap)

	out, err := mcpio.MarshalGraphCompactDirectional(resp, params.direction)
	if err != nil {
		return nil, fmt.Errorf("sense_graph: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// parseGraphParams validates and defaults the request parameters. A non-nil
// result is a ready-to-return validation error; the caller returns it as-is.
func parseGraphParams(req mcp.CallToolRequest) (graphParams, *mcp.CallToolResult) {
	symbol := req.GetString("symbol", "")
	if symbol == "" {
		return graphParams{}, mcp.NewToolResultError("sense_graph: missing required parameter 'symbol'")
	}

	direction := model.Direction(req.GetString("direction", "both"))
	switch direction {
	case model.DirectionBoth, model.DirectionCallers, model.DirectionCallees:
	default:
		return graphParams{}, mcp.NewToolResultError(fmt.Sprintf("sense_graph: direction must be both, callers, or callees (got %q)", direction))
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
		return graphParams{}, mcp.NewToolResultError(fmt.Sprintf("sense_graph: depth %d exceeds maximum of %d", depth, mcpio.MaxGraphDepth))
	}

	// min_confidence defaults to the display floor (0.5); a sentinel of -1
	// lets us tell "absent" from an explicit 0.0. An explicit value is
	// validated to [0,1] and clamped to a small positive epsilon so the
	// builder's zero-means-default guard never swallows it.
	minConfidence := mcpio.GraphConfidenceFloor
	if v := req.GetFloat("min_confidence", -1); v >= 0 {
		if v > 1 {
			return graphParams{}, mcp.NewToolResultError(fmt.Sprintf("sense_graph: min_confidence must be between 0.0 and 1.0 (got %g)", v))
		}
		minConfidence = v
		if minConfidence < mcpio.MinGraphConfidence {
			minConfidence = mcpio.MinGraphConfidence
		}
	}

	return graphParams{
		symbol:        symbol,
		fileHint:      req.GetString("file", ""),
		direction:     direction,
		depth:         depth,
		minConfidence: minConfidence,
		contextLines:  req.GetInt("context_lines", mcpio.DefaultContextLines),
	}, nil
}

// buildGraphResponse runs the core query for a resolved symbol: it reads the
// graph, resolves file paths, builds the response, and enriches it with
// dispatch-inferred and also-called-by edges. It returns the response and the
// raw graph result (needed by the shaping step for the root symbol id).
func (h *handlers) buildGraphResponse(ctx context.Context, match cli.Match, p graphParams) (mcpio.GraphResponse, *model.GraphResult, error) {
	gr, err := h.adapter.ReadSymbolGraph(ctx, match.ID, p.depth, p.direction, mcpio.MaxPerHop)
	if err != nil {
		return mcpio.GraphResponse{}, nil, fmt.Errorf("sense_graph: read symbol: %w", err)
	}

	fileIDs := cli.CollectGraphFileIDs(gr)
	pathByID, err := cli.LoadFilePaths(ctx, h.db, fileIDs)
	if err != nil {
		return mcpio.GraphResponse{}, nil, fmt.Errorf("sense_graph: load file paths: %w", err)
	}
	lookup := func(id int64) (string, bool) {
		path, ok := pathByID[id]
		return path, ok
	}

	buildReq := mcpio.BuildGraphRequest{
		Direction:      p.direction,
		SegmentCallers: h.defaults.GraphSegmentCallers,
		Snippets:       mcpio.NewSnippetReader(h.dir, p.contextLines),
		MinConfidence:  p.minConfidence,
	}
	resp := mcpio.BuildFullGraphResponse(ctx, gr, lookup, buildReq)

	if p.direction != model.DirectionCallees {
		inferred := h.resolveDispatchCallers(ctx, &gr.Root, &resp, lookup)
		if len(inferred) > 0 {
			resp.DispatchInferred = inferred
		}
	}

	if p.direction == model.DirectionCallees || p.direction == model.DirectionBoth {
		enrichAlsoCalledBy(ctx, h.adapter, h.cachedSymbolCount(ctx), gr, &resp)
	}

	return resp, gr, nil
}

// shapeGraphResponse applies the presentation steps that run after the query:
// edge compaction, seen-symbol tracking, metrics, the coverage note, the
// freshness footer, next-step hints, and the token budget.
func (h *handlers) shapeGraphResponse(ctx context.Context, resp *mcpio.GraphResponse, gr *model.GraphResult, p graphParams, snap *staleSnapshot) {
	if len(resp.Edges.CalledBy) > compactEdgeKeepCount {
		compactCallEdges(resp.Edges.CalledBy)
	}
	if len(resp.Edges.Calls) > compactEdgeKeepCount {
		compactCallEdges(resp.Edges.Calls)
	}
	if len(resp.DispatchInferred) > compactEdgeKeepCount {
		compactDispatchInferred(resp.DispatchInferred)
	}

	h.tracker.Record("sense_graph", p.symbol,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	if len(resp.Edges.CalledBy) > 10 {
		resp.CoverageNote = "Graph edges from indexed source files — may miss callers in examples/, scripts/, or macro-generated code"
	}

	resp.Freshness = computeFreshness(ctx, h.db, h.dir, false, h.watchState, snap)
	resp.NextSteps = graphHints(*resp, p.direction)

	// Keep hub responses within the MCP token budget — sheds deeper layers
	// and trims the longest edge list, recording the count in OmittedEdges.
	mcpio.ApplyGraphBudget(resp, h.defaults.GraphTokenBudget)

	// Mark the root AND the FINAL rendered called_by callers seen — AFTER the
	// budget trim, so a later sense_blast collapses ONLY callers the model
	// actually received, never ones the budget dropped (collapsing an unshown
	// caller would silently lose it). The rendered called_by set is exactly
	// blast's depth-1 direct-caller set; inherit/include/compose and indirect
	// callers are not marked — blast lists those separately. Test callers,
	// segmented into their own bucket, are intentionally left un-collapsed.
	h.markSeen(append(renderedCallerIDs(resp), gr.Root.Symbol.ID))
}

const (
	// Thresholds for also_called_by enrichment on graph callee queries.
	// 5000: small enough that the batch caller query is fast; covers repos
	// where reading all files is still feasible for an LLM.
	alsoCalledBySymbolThreshold = 5000
	// 20: suppress noisy hub symbols that everything calls.
	alsoCalledByCallerCap = 20
)

func (h *handlers) cachedSymbolCount(ctx context.Context) int {
	h.symbolCountOnce.Do(func() {
		_ = h.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sense_symbols`).Scan(&h.symbolCountVal)
	})
	return h.symbolCountVal
}

func enrichAlsoCalledBy(ctx context.Context, adapter *sqlite.Adapter, symbolCount int, gr *model.GraphResult, resp *mcpio.GraphResponse) {
	if len(resp.Edges.Calls) == 0 {
		return
	}
	if symbolCount >= alsoCalledBySymbolThreshold {
		return
	}

	nameToID := make(map[string]int64)
	for _, e := range gr.Root.Outbound {
		if e.Edge.Kind == model.EdgeCalls || e.Edge.Kind == model.EdgeReferences {
			nameToID[qualifiedOrNameRef(e.Target)] = e.Target.ID
		}
	}

	var targetIDs []int64
	for _, c := range resp.Edges.Calls {
		if id, ok := nameToID[c.Symbol]; ok {
			targetIDs = append(targetIDs, id)
		}
	}
	if len(targetIDs) == 0 {
		return
	}

	// Fetch cap+1 so we can detect overflow: if we get more than cap, suppress the callee.
	callerMap, err := adapter.CallersOfTargets(ctx, targetIDs, gr.Root.Symbol.ID, alsoCalledByCallerCap+1)
	if err != nil {
		return
	}

	for i := range resp.Edges.Calls {
		id, ok := nameToID[resp.Edges.Calls[i].Symbol]
		if !ok {
			continue
		}
		callers := callerMap[id]
		if len(callers) == 0 || len(callers) > alsoCalledByCallerCap {
			continue
		}
		resp.Edges.Calls[i].AlsoCalledBy = callers
	}
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
	var inferredIDs []int64
	for _, eqID := range equivIDs {
		if len(inferred) >= maxDispatchInferred {
			break
		}
		eqSym, err := h.adapter.ReadSymbol(ctx, eqID)
		if err != nil {
			continue
		}
		via := qualifiedOrNameRef(eqSym.Symbol)
		inferred = appendDispatchCallers(inferred, &inferredIDs, eqSym.Inbound, via, directCallers, lookup)
	}
	// Dispatch-inferred callers reach the subject via interface dispatch — a
	// later sense_blast counts them among its direct callers, so record them
	// here to dedup that re-dump.
	h.markSeen(inferredIDs)
	return inferred
}

// appendDispatchCallers folds one dispatch-equivalent method's inbound call
// edges into inferred, skipping callers already seen (direct or via an earlier
// equivalent) and stopping once the per-query cap is reached.
func appendDispatchCallers(inferred []mcpio.DispatchInferredRef, ids *[]int64, inbound []model.EdgeRef, via string, directCallers map[string]struct{}, lookup mcpio.FileLookup) []mcpio.DispatchInferredRef {
	for _, e := range inbound {
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
		*ids = append(*ids, e.Target.ID)

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
	return inferred
}

// renderedCallerIDs collects the symbol ids of the callers the graph response
// ACTUALLY rendered in its
// called_by bucket, read after segmentation and the budget trim. These are
// exactly the depth-1 callers the model received and exactly blast's direct-
// caller set, so a later sense_blast can collapse them with no risk of hiding
// a caller that was never shown. Test callers (segmented into their own
// bucket) and indirect/inherit/include/compose targets are deliberately not
// returned — blast enumerates those separately. IDs of 0 (unresolved-source
// view edges) are skipped: they carry no symbol blast could match.
func renderedCallerIDs(resp *mcpio.GraphResponse) []int64 {
	ids := make([]int64, 0, len(resp.Edges.CalledBy))
	for _, c := range resp.Edges.CalledBy {
		if c.ID != 0 {
			ids = append(ids, c.ID)
		}
	}
	return ids
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
	switch {
	case totalCallers >= 5:
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_blast",
			Args:   map[string]any{"symbol": resp.Symbol.Qualified},
			Reason: fmt.Sprintf("%d callers found — check blast radius before changing this symbol", totalCallers),
		})
	case totalCallers == 0 && resp.LowConfidenceHidden > 0:
		// Callers exist in the index but sit below the display floor — e.g.
		// implicit-receiver calls to a method whose name is defined in several
		// classes, stamped 0.3 by name-collision fallback. Surfacing the knob
		// here stops an agent from reading the empty list as "unused".
		args := map[string]any{"symbol": resp.Symbol.Qualified, "min_confidence": 0.3}
		if direction == model.DirectionCallers {
			args["direction"] = "callers"
		}
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_graph",
			Args:   args,
			Reason: fmt.Sprintf("%d low-confidence callers hidden — re-run with min_confidence=0.3 to view before assuming this is unused", resp.LowConfidenceHidden),
		})
	case totalCallers == 0 && !isTestFile(resp.Symbol.File):
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

// ---------------------------------------------------------------
// dead_code mode (a mode of sense_graph)
// ---------------------------------------------------------------

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

	resp := mcpio.BuildUnreferencedResponse(result.Findings, result.TotalSymbols, 0)
	resp.CoverageNote = "Static analysis — does not trace dynamic dispatch, decorator registration, or external API consumers"

	h.tracker.Record("sense_graph", "dead_code",
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, false)

	resp.NextSteps = deadCodeHints(resp)

	out, err := mcpio.MarshalUnreferencedCompact(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_graph dead_code: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// deadCodeHints suggests confirming the top earned-`dead` symbol before
// removal. The possibly_dead groups carry their own verify recipes, so a hint
// is only added when there is an actionable dead entry.
func deadCodeHints(resp mcpio.UnreferencedResponse) []mcpio.NextStep {
	var hints []mcpio.NextStep

	if resp.DeadCount > 0 && len(resp.Unreferenced.Dead) > 0 {
		hints = append(hints, mcpio.NextStep{
			Tool:   "sense_graph",
			Args:   map[string]any{"symbol": resp.Unreferenced.Dead[0].Qualified},
			Reason: "inspect the top dead symbol's relationships to confirm it's truly unused",
		})
	}

	return hints
}

func isTestFile(path string) bool {
	return mcpio.IsTestPath(path)
}
