package mcpio

import "github.com/luuuc/sense/internal/dead"

// BuildDeadCodeResponse assembles a wire DeadCodeResponse from the dead
// package's result plus a rolled-up symbol list. Matches the
// BuildBlastResponse / BuildGraphResponse pattern — one builder per
// response shape, shared between CLI and MCP server.
func BuildDeadCodeResponse(symbols []dead.Symbol, totalSymbols int) DeadCodeResponse {
	entries := make([]DeadSymbolEntry, len(symbols))
	uniqueFiles := map[string]struct{}{}
	for i, s := range symbols {
		entries[i] = DeadSymbolEntry{
			Symbol:    s.Name,
			Qualified: s.Qualified,
			File:      s.File,
			LineStart: s.LineStart,
			LineEnd:   s.LineEnd,
			Kind:      s.Kind,
		}
		uniqueFiles[s.File] = struct{}{}
	}

	filesAvoided := len(uniqueFiles)
	return DeadCodeResponse{
		DeadSymbols:  entries,
		TotalSymbols: totalSymbols,
		DeadCount:    len(symbols),
		SenseMetrics: DeadCodeMetrics{
			SymbolsAnalyzed:           totalSymbols,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * AvgTokensPerFile,
		},
	}
}
