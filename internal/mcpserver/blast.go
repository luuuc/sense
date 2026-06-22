package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/model"
)

func (h *handlers) handleBlast(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	symbol := req.GetString("symbol", "")
	diff := req.GetString("diff", "")
	fileHint := req.GetString("file", "")

	if symbol == "" && diff == "" {
		return mcp.NewToolResultError("sense_blast: pass either 'symbol' or 'diff'"), nil
	}
	if symbol != "" && diff != "" {
		return mcp.NewToolResultError("sense_blast: pass either 'symbol' or 'diff', not both"), nil
	}

	// Read-repair: refresh stale touched files before the blast resolves
	// and walks edges, so a just-edited symbol is current.
	snap := h.repairAndSnapshot(ctx)

	maxHops := req.GetInt("max_hops", h.defaults.BlastMaxHops)
	minConfidence := req.GetFloat("min_confidence", h.defaults.BlastMinConfidence)
	includeTests := req.GetBool("include_tests", true)

	opts := blast.Options{
		MaxHops:       maxHops,
		MinConfidence: minConfidence,
		MaxResults:    h.defaults.BlastResultCap,
		IncludeTests:  includeTests,
	}

	contextLines := req.GetInt("context_lines", mcpio.DefaultContextLines)
	snippets := mcpio.NewSnippetReader(h.dir, contextLines)

	var resp mcpio.BlastResponse

	if diff != "" {
		resp2, err := h.blastDiff(ctx, diff, opts, snippets)
		if err != nil {
			return nil, err
		}
		resp = resp2
	} else {
		resp2, err := h.blastSymbol(ctx, symbol, fileHint, opts, snippets)
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

	freshness := computeFreshness(ctx, h.db, h.dir, false, h.watchState, snap)
	resp.Freshness = freshness
	resp.NextSteps = blastHints(resp)

	// Keep the response within the MCP token budget — trims enumerated
	// lists while preserving the summary counts. Runs last so hints and
	// metrics still reflect the full radius.
	mcpio.ApplyBlastBudget(&resp, h.defaults.BlastTokenBudget)

	out, err := mcpio.MarshalBlastCompact(resp)
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

// ---------------------------------------------------------------
// Symbol resolution (shared by sense_graph and sense_blast)
// ---------------------------------------------------------------

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
func (h *handlers) resolveSymbol(ctx context.Context, tool, symbol, fileHint string) (cli.Match, error) {
	matches, err := cli.Lookup(ctx, h.db, symbol)
	if err != nil {
		return cli.Match{}, fmt.Errorf("%s: lookup: %w", tool, err)
	}

	// Disambiguate by file path substring when the caller supplied one.
	// Mirrors the CLI's --file flag: an ambiguous symbol (a re-opened Ruby
	// class, or a name shared with a JS/TS component in a full-stack repo)
	// resolves to the single candidate whose path contains the hint. A hint
	// that matches nothing is ignored so the normal not-found/ambiguous
	// handling still runs.
	if fileHint != "" {
		if filtered := cli.FilterMatches(matches, fileHint, ""); len(filtered) > 0 {
			matches = filtered
		}
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
		// Always steer to the `file` parameter: it disambiguates by path
		// substring and works even when the candidates share a qualified
		// name (a re-opened class) or span languages (a Ruby model vs a JS
		// component). A bare qualified-name retry fails in those cases.
		hint = fmt.Sprintf("Multiple symbols named %q exist — retry this tool with the `file` parameter set to the path of the one you want, e.g. file: %q (see top_matches for the paths)",
			symbol, items[0].match.File)
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

func (h *handlers) blastSymbol(ctx context.Context, symbol, fileHint string, opts blast.Options, snippets *mcpio.SnippetReader) (mcpio.BlastResponse, error) {
	match, err := h.resolveSymbol(ctx, "sense_blast", symbol, fileHint)
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

	resp := mcpio.BuildBlastResponseSeen(ctx, blastResult, lookup, snippets, h.seenPredicate())
	// Symmetry: record every direct caller so a later sense_graph/sense_blast
	// dedups against this blast, exactly as graph records its edge targets.
	h.markSeen(directCallerIDs(blastResult.DirectCallers))
	return resp, nil
}

// directCallerIDs extracts the symbol ids of a blast result's direct callers,
// the set marked seen after a blast so later calls can dedup against it.
func directCallerIDs(callers []model.Symbol) []int64 {
	ids := make([]int64, len(callers))
	for i, c := range callers {
		ids[i] = c.ID
	}
	return ids
}

func (h *handlers) blastDiff(ctx context.Context, ref string, opts blast.Options, snippets *mcpio.SnippetReader) (mcpio.BlastResponse, error) {
	hunks, err := cli.GitDiffHunks(ctx, h.dir, ref)
	if err != nil {
		return mcpio.BlastResponse{}, fmt.Errorf("sense_blast: %w", err)
	}

	symbolIDs, err := cli.SymbolsInChangedLines(ctx, h.db, hunks)
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

	resp := mcpio.BuildDiffBlastResponseSeen(ctx, ref, results, lookup, snippets, h.seenPredicate())
	for _, r := range results {
		h.markSeen(directCallerIDs(r.DirectCallers))
	}
	return resp, nil
}
