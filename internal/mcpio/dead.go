package mcpio

import (
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/dead"
)

// verifyTooCommonThreshold is the index-occurrence count above which a
// name is considered too common to auto-verify by grep. Above it, a text
// search for the name floods the caller (a predicate like "success?" is
// defined and called all over a Rails app), so the verify hint points at
// the definition site for manual inspection instead. Estimated from the
// index (symbols + resolved edges sharing the name); see
// dead.Symbol.NameOccurrences. Tuned against maket, where the
// mis-attributed Checkout predicates sit well above it.
const verifyTooCommonThreshold = 25

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
		cmd, tooCommon := deadVerifyCmd(s.Name, s.File, s.LineStart, s.NameOccurrences)
		entries[i] = DeadSymbolEntry{
			Symbol:          s.Name,
			Qualified:       s.Qualified,
			File:            s.File,
			LineStart:       s.LineStart,
			LineEnd:         s.LineEnd,
			Kind:            s.Kind,
			Confidence:      confidence,
			VerifyCmd:       cmd,
			VerifyTooCommon: tooCommon,
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
				"(2) ActiveSupport::Concern mixins — methods defined in an included module become instance methods of the includer; " +
				"(3) callbacks & metaprogramming — before_action/after_action by symbol, define_method, const_get/constantize, STI; " +
				"(4) framework lifecycle hooks invoked by Rails rather than application code; and " +
				"(5) view templates that produced no indexed edges — a pure-markup or otherwise unmodeled ERB file. " +
				"View & Hotwire dispatch is NOT a blind spot: methods reached from ERB or Stimulus " +
				"data-controller/data-action/data-*-target attributes, rendered partials, i18n keys, and embedded-Ruby " +
				"helper/route calls ARE extracted and resolve, so a symbol reached only from a view is not dead."
		}
	}
	return deadNotePrefix +
		"(1) method-on-field dispatch (e.g. c.engine.X invoking a method through a struct field), " +
		"(2) function-value passing (handlers stored as fields, passed as args, or set via init()), " +
		"(3) runtime registration (DI containers, plugin registries, reflection-based loaders, ServiceLoader), " +
		"(4) interface/trait satisfaction via blank identifier (var _ Iface = (*T)(nil)), and " +
		"(5) exported symbols consumed by downstream packages outside the indexed tree."
}

// deadVerifyCmd builds the verify hint for one dead candidate. It returns
// the hint string and whether the name was too common to auto-verify.
//
// Normally it emits a copy-paste grep scoped to CALL syntax — `\.name`,
// the receiver-dot form a missed dynamic dispatch (`result.success?`)
// takes — rather than the bare name, which on a common predicate floods
// the caller with hundreds of unrelated definitions and comments. The
// definition's own file:line is filtered out with a trailing `grep -v`,
// so a single surviving hit means a genuinely missed call site, not the
// definition echoing back.
//
// When the name's index-occurrence estimate exceeds the threshold, even a
// call-scoped grep would return too much to be useful, so the hint points
// at the definition site for manual inspection instead and tooCommon is
// true.
func deadVerifyCmd(name, file string, line, occurrences int) (cmd string, tooCommon bool) {
	if occurrences > verifyTooCommonThreshold {
		return fmt.Sprintf("name %q too common to auto-verify (%d index occurrences) — inspect %s:%d and its call sites manually",
			name, occurrences, file, line), true
	}

	// Scope to call syntax: a leading receiver dot, the name with any `?`/`!`
	// predicate/bang suffix escaped for the regex engine. The pattern is
	// wrapped in literal shell double quotes — %q would Go-escape the
	// backslash and hand grep `\\.` (a literal backslash) instead of `\.`.
	pattern := `\.` + escapeForERE(name)
	grep := `grep -rEn --exclude-dir=.git --exclude-dir=.sense "` + pattern + `" .`
	// Exclude the definition's own file:line. `grep -rEn` prefixes each hit
	// with `./path:line:`; a fixed-string (-F) match on that prefix drops the
	// def line without the `.`s acting as wildcards.
	exclude := fmt.Sprintf(`grep -vF "./%s:%d:"`, file, line)
	return grep + " | " + exclude, false
}

// escapeForERE escapes the ERE metacharacters that occur in Ruby method
// names — `?` (predicate), `!` (bang), and `.` — so the call-scoped grep
// pattern matches the literal name. Other identifier characters are
// regex-safe.
func escapeForERE(name string) string {
	r := strings.NewReplacer(
		`.`, `\.`,
		`?`, `\?`,
		`!`, `\!`,
	)
	return r.Replace(name)
}
