package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"

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
