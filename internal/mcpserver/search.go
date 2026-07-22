package mcpserver

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/search"
)

// snippetStripThreshold strips snippets from search results beyond this index
// (pitch 22-05 response compaction).
const snippetStripThreshold = 5

func (h *handlers) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError("sense_search: missing required parameter 'query'"), nil
	}

	limit := req.GetInt("limit", 10)
	results, meta, err := h.search.Search(ctx, h.searchOptions(req, query, limit))
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

	entries, uniqueFiles := h.buildSearchEntries(results, pathByID)
	entries, uniqueFiles, textFallbackFired := h.applyTextFallback(ctx, query, limit, entries, uniqueFiles)

	resp := assembleSearchResponse(entries, uniqueFiles, meta, textFallbackFired)
	h.tracker.Record("sense_search", query,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved, textFallbackFired)
	resp.NextSteps = searchHints(resp)

	out, err := mcpio.MarshalSearchCompact(resp)
	if err != nil {
		return nil, fmt.Errorf("sense_search: marshal: %w", err)
	}
	return mcp.NewToolResultText(string(out)), nil
}

// searchOptions reads the request parameters into engine search options,
// applying the profile's keyword-weight bias.
func (h *handlers) searchOptions(req mcp.CallToolRequest, query string, limit int) search.Options {
	keywordBias := h.defaults.SearchKeywordWeight - 0.5
	if keywordBias < 0 {
		keywordBias = 0
	}
	return search.Options{
		Query:       query,
		Limit:       limit,
		Language:    req.GetString("language", ""),
		MinScore:    req.GetFloat("min_score", 0.0),
		KeywordBias: keywordBias,
		Mode:        req.GetString("mode", search.ModeHybrid),
	}
}

// buildSearchEntries converts engine results into response entries, marking
// already-seen symbols, stripping snippets past the strip threshold, and
// collecting the set of unique files the results touch.
func (h *handlers) buildSearchEntries(results []search.Result, pathByID map[int64]string) ([]mcpio.SearchResultEntry, map[string]struct{}) {
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
			Source:     r.Source,
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
	return entries, uniqueFiles
}

// applyTextFallback appends literal grep-style matches when the index search
// underfilled the limit and a text fallback is available. It returns the
// (possibly extended) entries and file set, and whether the fallback fired.
func (h *handlers) applyTextFallback(ctx context.Context, query string, limit int, entries []mcpio.SearchResultEntry, uniqueFiles map[string]struct{}) ([]mcpio.SearchResultEntry, map[string]struct{}, bool) {
	if h.textFallback == nil || !h.textFallback.Available() || len(entries) >= limit {
		return entries, uniqueFiles, false
	}

	textResults := h.textFallback.Search(ctx, query, h.dir, []string{"."}, limit)
	matches := make([]mcpio.TextMatch, len(textResults))
	for i, tr := range textResults {
		matches[i] = mcpio.TextMatch{File: tr.File, Line: tr.Line, Match: tr.Match}
	}
	textEntries, fired := mcpio.ConvertTextResults(matches, entries)
	if !fired {
		return entries, uniqueFiles, false
	}
	entries = append(entries, textEntries...)
	for _, e := range textEntries {
		uniqueFiles[e.File] = struct{}{}
	}
	return entries, uniqueFiles, true
}

// assembleSearchResponse builds the response envelope from the finished
// entries, the search metadata, and whether the text fallback contributed.
func assembleSearchResponse(entries []mcpio.SearchResultEntry, uniqueFiles map[string]struct{}, meta search.SearchMeta, textFallbackFired bool) mcpio.SearchResponse {
	searchMode := meta.Mode
	if textFallbackFired {
		searchMode += "+text"
	}

	filesAvoided := len(uniqueFiles)
	return mcpio.SearchResponse{
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
}

// searchHints returns nothing by design. Both hints it used to emit -
// "strong match, explore its relationships" (the single most-issued hint in the
// go campaign, 126 of 605) and "cluster, check conventions in this area" (108)
// - only restated the payload: the top result's symbol, score and file are
// already in the response, and "go look at it" adds no information the model
// does not have. Over 605 issuances next_steps was obeyed 7% of the time on the
// next call; a channel that fires 11 times a session is noise, so a hint now
// has to name a tool, knob or gap that is NOT in the current payload to earn its
// slot. Neither of these did. Kept as a named no-op so the wiring stays uniform
// and a future load-bearing search hint has an obvious home.
func searchHints(mcpio.SearchResponse) []mcpio.NextStep {
	return nil
}
