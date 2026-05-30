package mcpio

import (
	"fmt"
	"sort"

	"github.com/luuuc/sense/internal/dead"
)

// BuildUnreferencedResponse assembles the honest-verdict wire response from
// the arbiter's findings. It splits findings into the earned `dead` list and
// `possibly_dead` reason groups, ranks the groups by removability, applies
// the symbol limit (dead is never truncated; possibly_dead is capped with
// per-group dropped counts), and attaches verify recipes. The internal
// open/closed-world vocabulary never crosses this boundary — only verdict,
// reason, and verify do.
//
// limit caps the total number of symbols reported. A non-positive limit means
// no cap. dead entries are always kept; the cap applies to possibly_dead only,
// since the few earned deads are the actionable core and must never be cut.
func BuildUnreferencedResponse(findings []dead.Finding, totalSymbols, limit int) UnreferencedResponse {
	var deadEntries []DeadEntry
	groups := map[string][]PossiblyDeadSymbol{}
	hints := map[string]string{}
	uniqueFiles := map[string]struct{}{}

	for _, f := range findings {
		s := f.Symbol
		uniqueFiles[s.File] = struct{}{}
		if f.Verdict == dead.VerdictDead {
			deadEntries = append(deadEntries, DeadEntry{
				Qualified: s.Qualified,
				File:      s.File,
				Line:      s.LineStart,
				Kind:      s.Kind,
				Verify:    deadVerify(s.Name, s.File, s.LineStart, s.NameOccurrences),
			})
			continue
		}
		// possibly_dead: group by reason code. A finding without a reason is
		// a contract violation (every possibly_dead carries one); guard it
		// into a synthetic group rather than panicking on bad input.
		code, hint := reasonOf(f)
		hints[code] = hint
		groups[code] = append(groups[code], PossiblyDeadSymbol{
			Qualified: s.Qualified,
			File:      s.File,
			Line:      s.LineStart,
			Kind:      s.Kind,
		})
	}

	deadEntries = sortDeadEntries(deadEntries)
	possiblyGroups := rankGroups(groups, hints)

	// Apply the limit: dead is always kept; the remaining budget is spent on
	// possibly_dead in rank order, recording per-group dropped counts.
	possiblyGroups = applyLimit(possiblyGroups, limit, len(deadEntries))

	possiblyCount := 0
	for _, g := range possiblyGroups {
		possiblyCount += len(g.Symbols) + g.Dropped
	}

	filesAvoided := len(uniqueFiles)
	return UnreferencedResponse{
		Unreferenced: UnreferencedSymbols{
			Dead:         deadEntries,
			PossiblyDead: possiblyGroups,
		},
		TotalSymbols:      totalSymbols,
		DeadCount:         len(deadEntries),
		PossiblyDeadCount: possiblyCount,
		SenseMetrics: DeadCodeMetrics{
			SymbolsAnalyzed:           totalSymbols,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * AvgTokensPerFile,
		},
	}
}

// reasonOf extracts a finding's reason code and hint, falling back to a
// generic "unknown" reason if one is somehow missing (defensive — the
// arbiter always sets a reason on possibly_dead).
func reasonOf(f dead.Finding) (code, hint string) {
	if f.Reason == nil {
		return "unknown", "unreferenced; reason unavailable — verify callers manually before removing"
	}
	return f.Reason.Code, f.Reason.Hint
}

// sortDeadEntries orders the dead list by file then line, for stable,
// readable output.
func sortDeadEntries(entries []DeadEntry) []DeadEntry {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].File != entries[j].File {
			return entries[i].File < entries[j].File
		}
		return entries[i].Line < entries[j].Line
	})
	return entries
}

// rankGroups turns the reason→symbols map into ranked groups: by reason
// priority descending (most likely removable first), ties broken by code for
// determinism. Symbols within a group are ordered by file then line.
func rankGroups(groups map[string][]PossiblyDeadSymbol, hints map[string]string) []PossiblyDeadGroup {
	out := make([]PossiblyDeadGroup, 0, len(groups))
	for code, syms := range groups {
		sort.SliceStable(syms, func(i, j int) bool {
			if syms[i].File != syms[j].File {
				return syms[i].File < syms[j].File
			}
			return syms[i].Line < syms[j].Line
		})
		out = append(out, PossiblyDeadGroup{
			Reason:  ReasonInfo{Code: code, Hint: hints[code]},
			Verify:  groupVerify(code),
			Symbols: syms,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := dead.ReasonPriority(out[i].Reason.Code), dead.ReasonPriority(out[j].Reason.Code)
		if pi != pj {
			return pi > pj // higher priority (more removable) first
		}
		return out[i].Reason.Code < out[j].Reason.Code
	})
	return out
}

// groupVerify returns the reason's group verify recipe, falling back to a
// generic instruction for an unknown code.
func groupVerify(code string) string {
	if v := dead.ReasonGroupVerify(code); v != "" {
		return v
	}
	return "Unreferenced symbols sharing this reason — grep the repo for each name as a call and as a literal before removing."
}

// applyLimit caps the total reported symbols at limit. dead entries (deadKept)
// always count against the budget but are never dropped; the remaining budget
// is spent on possibly_dead groups in rank order. When a group is partially
// or fully cut, its Dropped count records how many symbols were removed, so
// truncation is reported, never silent. A non-positive limit means no cap.
func applyLimit(groups []PossiblyDeadGroup, limit, deadKept int) []PossiblyDeadGroup {
	if limit <= 0 {
		return groups
	}
	budget := limit - deadKept
	if budget < 0 {
		budget = 0
	}
	for i := range groups {
		n := len(groups[i].Symbols)
		switch {
		case budget >= n:
			budget -= n
		case budget == 0:
			groups[i].Dropped += n
			groups[i].Symbols = nil
		default:
			groups[i].Dropped += n - budget
			groups[i].Symbols = groups[i].Symbols[:budget]
			budget = 0
		}
	}
	return groups
}

// deadVerify builds the per-symbol verify recipe for a `dead` entry: a
// call-scoped grep (`\.name`) that finds receiver-dot call sites the static
// index missed, with the definition's own file:line filtered out so a single
// surviving hit means a genuinely missed call. When the name is too common
// across the index for a grep to be useful, it points at the definition site
// for manual inspection instead. This is the per-symbol counterpart to the
// per-group recipe carried by possibly_dead groups.
func deadVerify(name, file string, line, occurrences int) string {
	if occurrences > verifyTooCommonThreshold {
		return fmt.Sprintf("name %q is too common to auto-verify (%d index occurrences) — inspect %s:%d and its call sites manually",
			name, occurrences, file, line)
	}
	pattern := `\.` + escapeForERE(name)
	grep := `grep -rEn --exclude-dir=.git --exclude-dir=.sense "` + pattern + `" .`
	exclude := fmt.Sprintf(`grep -vF "./%s:%d:"`, file, line)
	return grep + " | " + exclude
}
