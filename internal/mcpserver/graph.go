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

//nolint:gocyclo // 27-05: retired by the mcpserver split
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

	// Read-repair: refresh any stale touched files before resolving, so a
	// just-edited symbol is visible even before the debounced watcher fires.
	snap := h.repairAndSnapshot(ctx)

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

	contextLines := req.GetInt("context_lines", mcpio.DefaultContextLines)

	// min_confidence defaults to the display floor (0.5); a sentinel of -1
	// lets us tell "absent" from an explicit 0.0. An explicit value is
	// validated to [0,1] and clamped to a small positive epsilon so the
	// builder's zero-means-default guard never swallows it.
	minConfidence := mcpio.GraphConfidenceFloor
	if v := req.GetFloat("min_confidence", -1); v >= 0 {
		if v > 1 {
			return mcp.NewToolResultError(fmt.Sprintf("sense_graph: min_confidence must be between 0.0 and 1.0 (got %g)", v)), nil
		}
		minConfidence = v
		if minConfidence < mcpio.MinGraphConfidence {
			minConfidence = mcpio.MinGraphConfidence
		}
	}

	buildReq := mcpio.BuildGraphRequest{
		Direction:      direction,
		SegmentCallers: h.defaults.GraphSegmentCallers,
		Snippets:       mcpio.NewSnippetReader(h.dir, contextLines),
		MinConfidence:  minConfidence,
	}
	resp := mcpio.BuildFullGraphResponse(ctx, gr, lookup, buildReq)

	if direction != model.DirectionCallees {
		inferred := h.resolveDispatchCallers(ctx, &gr.Root, &resp, lookup)
		if len(inferred) > 0 {
			resp.DispatchInferred = inferred
		}
	}

	if direction == model.DirectionCallees || direction == model.DirectionBoth {
		enrichAlsoCalledBy(ctx, h.adapter, h.cachedSymbolCount(ctx), gr, &resp)
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

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState, snap)
	resp.Freshness = freshness
	resp.NextSteps = graphHints(resp, direction)

	// Keep hub responses within the MCP token budget — sheds deeper layers
	// and trims the longest edge list, recording the count in OmittedEdges.
	mcpio.ApplyGraphBudget(&resp, h.defaults.GraphTokenBudget)

	out, err := mcpio.MarshalGraphCompactDirectional(resp, direction)
	if err != nil {
		return nil, fmt.Errorf("sense_graph: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
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

//nolint:gocyclo // 27-05: retired by the mcpserver split
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
