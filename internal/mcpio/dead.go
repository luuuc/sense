package mcpio

import (
	"fmt"

	"github.com/luuuc/sense/internal/dead"
)

// BuildDeadCodeResponse assembles a wire DeadCodeResponse from the dead
// package's result plus a rolled-up symbol list. Matches the
// BuildBlastResponse / BuildGraphResponse pattern — one builder per
// response shape, shared between CLI and MCP server.
func BuildDeadCodeResponse(symbols []dead.Symbol, totalSymbols int) DeadCodeResponse {
	entries := make([]DeadSymbolEntry, len(symbols))
	uniqueFiles := map[string]struct{}{}
	for i, s := range symbols {
		confidence := s.Confidence
		if confidence == "" {
			confidence = dead.ConfidenceDead
		}
		entries[i] = DeadSymbolEntry{
			Symbol:     s.Name,
			Qualified:  s.Qualified,
			File:       s.File,
			LineStart:  s.LineStart,
			LineEnd:    s.LineEnd,
			Kind:       s.Kind,
			Confidence: confidence,
			VerifyCmd:  deadVerifyCmd(s.Name),
		}
		uniqueFiles[s.File] = struct{}{}
	}

	filesAvoided := len(uniqueFiles)
	return DeadCodeResponse{
		DeadSymbols:  entries,
		TotalSymbols: totalSymbols,
		DeadCount:    len(symbols),
		Note: "Symbols with zero incoming edges. Verify each candidate against these indexer blind spots before deleting: " +
			"(1) method-on-field dispatch (e.g. c.engine.X invoking a method through a struct field), " +
			"(2) function-value passing (handlers stored as fields, passed as args, or set via init()), " +
			"(3) runtime registration (DI containers, plugin registries, reflection-based loaders, ServiceLoader), " +
			"(4) interface/trait satisfaction via blank identifier (var _ Iface = (*T)(nil)), and " +
			"(5) exported symbols consumed by downstream packages outside the indexed tree.",
		SenseMetrics: DeadCodeMetrics{
			SymbolsAnalyzed:           totalSymbols,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * AvgTokensPerFile,
		},
	}
}

// deadVerifyCmd builds a copy-paste grep that lists every textual occurrence of
// a candidate's name across the tree — the definition plus any call sites the
// static index missed (duck-typed dispatch, metaprogramming). Fixed-string (-F)
// so predicate names ending in `?` aren't interpreted as a regex. The VCS and
// index directories are excluded as guaranteed noise; every source extension is
// still searched, since a predicate may be called from a view or template.
func deadVerifyCmd(name string) string {
	return fmt.Sprintf("grep -rFn --exclude-dir=.git --exclude-dir=.sense %q .", name)
}
