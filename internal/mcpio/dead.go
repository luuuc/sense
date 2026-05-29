package mcpio

import (
	"fmt"

	"github.com/luuuc/sense/internal/dead"
)

// BuildDeadCodeResponse assembles a wire DeadCodeResponse from the dead
// package's result plus a rolled-up symbol list. Matches the
// BuildBlastResponse / BuildGraphResponse pattern — one builder per
// response shape, shared between CLI and MCP server. frameworks tailors
// the blind-spot caveat to the project's ecosystem.
func BuildDeadCodeResponse(symbols []dead.Symbol, totalSymbols int, frameworks []string) DeadCodeResponse {
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
		Note:         deadCodeNote(frameworks),
		SenseMetrics: DeadCodeMetrics{
			SymbolsAnalyzed:           totalSymbols,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * AvgTokensPerFile,
		},
	}
}

const deadNotePrefix = "Symbols with zero incoming edges. Verify each candidate against these indexer blind spots before deleting: "

// deadCodeNote returns a blind-spot caveat tailored to the project's
// ecosystem. A Go-flavored note (struct-field dispatch, ServiceLoader,
// blank-identifier interface satisfaction) is noise on a Rails app and
// omits the failure modes that actually occur there — so Rails projects
// get Rails idioms instead.
func deadCodeNote(frameworks []string) string {
	for _, f := range frameworks {
		if f == "Rails" {
			return deadNotePrefix +
				"(1) routing — controller actions are dispatched by config/routes, never called from Ruby; " +
				"(2) view & Hotwire dispatch — methods reached from ERB or Stimulus data-controller/data-action/data-*-target attributes; " +
				"(3) ActiveSupport::Concern mixins — methods defined in an included module become instance methods of the includer; " +
				"(4) callbacks & metaprogramming — before_action/after_action by symbol, define_method, const_get/constantize, STI; and " +
				"(5) framework lifecycle hooks invoked by Rails rather than application code."
		}
	}
	return deadNotePrefix +
		"(1) method-on-field dispatch (e.g. c.engine.X invoking a method through a struct field), " +
		"(2) function-value passing (handlers stored as fields, passed as args, or set via init()), " +
		"(3) runtime registration (DI containers, plugin registries, reflection-based loaders, ServiceLoader), " +
		"(4) interface/trait satisfaction via blank identifier (var _ Iface = (*T)(nil)), and " +
		"(5) exported symbols consumed by downstream packages outside the indexed tree."
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
