package mcpio

import (
	"fmt"

	"github.com/luuuc/sense/internal/blast"
)

const (
	tier1Cap         = 200
	tier2ExamplesCap = 5
)

// BuildBlastResponse assembles a wire BlastResponse from the blast
// engine's Result plus a file-path lookup for caller symbols. Results
// are partitioned by relevance tier:
//   - Tier 1 (breaks): full detail, capped at 200 items
//   - Tier 2 (references): count + top 5 examples
//   - Tier 3 (tests): count only
func BuildBlastResponse(r blast.Result, files FileLookup) BlastResponse {
	resp := BlastResponse{
		Symbol:             qualifiedOrName(r.Symbol),
		Risk:               r.Risk,
		RiskFactors:        append([]string{}, r.RiskReasons...),
		AffectedTests:      append([]string{}, r.AffectedTests...),
		TotalAffected:      r.TotalAffected,
		TestsAffectedCount: len(r.AffectedTests),
		Truncated:          r.Truncated,
	}

	hasTiers := len(r.SymbolTiers) > 0
	tier1Count := 0
	var tier2All []BlastCaller

	for _, c := range r.DirectCallers {
		var file string
		if path, ok := files(c.FileID); ok {
			file = path
		}
		entry := BlastCaller{
			Symbol:      qualifiedOrName(c),
			File:        file,
			LineStart:   c.LineStart,
			LineEnd:     c.LineEnd,
			Ref:         FormatRef(file, c.LineStart),
			ViaTemporal: r.DirectTemporalIDs[c.ID],
		}

		if !hasTiers || r.SymbolTiers[c.ID] == blast.TierBreaks {
			if tier1Count < tier1Cap {
				resp.DirectCallers = append(resp.DirectCallers, entry)
				tier1Count++
			}
		} else {
			tier2All = append(tier2All, entry)
		}
	}
	for _, hop := range r.IndirectCallers {
		if !hasTiers || r.SymbolTiers[hop.Symbol.ID] == blast.TierBreaks {
			if tier1Count < tier1Cap {
				var file string
				if path, ok := files(hop.Symbol.FileID); ok {
					file = path
				}
				resp.IndirectCallers = append(resp.IndirectCallers, BlastIndirect{
					Symbol:      qualifiedOrName(hop.Symbol),
					Via:         qualifiedOrName(hop.Via),
					Hops:        hop.Hops,
					LineStart:   hop.Symbol.LineStart,
					LineEnd:     hop.Symbol.LineEnd,
					Ref:         FormatRef(file, hop.Symbol.LineStart),
					ViaTemporal: hop.ViaTemporal,
				})
				tier1Count++
			}
		} else {
			var file string
			if path, ok := files(hop.Symbol.FileID); ok {
				file = path
			}
			tier2All = append(tier2All, BlastCaller{
				Symbol:    qualifiedOrName(hop.Symbol),
				File:      file,
				LineStart: hop.Symbol.LineStart,
				LineEnd:   hop.Symbol.LineEnd,
				Ref:       FormatRef(file, hop.Symbol.LineStart),
			})
		}
	}

	for _, s := range r.AffectedSubclasses {
		var file string
		if path, ok := files(s.FileID); ok {
			file = path
		}
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
		resp.AffectedSubclasses = append(resp.AffectedSubclasses, entry)
		tier2All = append(tier2All, entry)
	}
	for _, s := range r.AffectedViaComposition {
		var file string
		if path, ok := files(s.FileID); ok {
			file = path
		}
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
		resp.AffectedViaComposition = append(resp.AffectedViaComposition, entry)
		tier2All = append(tier2All, entry)
	}
	for _, s := range r.AffectedViaIncludes {
		var file string
		if path, ok := files(s.FileID); ok {
			file = path
		}
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
		resp.AffectedViaIncludes = append(resp.AffectedViaIncludes, entry)
		tier2All = append(tier2All, entry)
	}

	examples := tier2All
	if len(examples) > tier2ExamplesCap {
		examples = examples[:tier2ExamplesCap]
	}
	resp.References = BlastTierSummary{
		Count:    len(tier2All),
		Examples: examples,
	}

	resp.Note = "total_affected counts unique symbols in the blast radius — not source-level references or graph edges traversed."

	segmentBlastCallers(&resp)

	if r.TotalAffected == 0 && r.SubjectHasCallees {
		resp.VerifyHint = "This symbol has outgoing calls but zero incoming callers in the index. This is unusual — verify with grep before concluding it's unused."
	}

	uniqueFiles := countUniqueBlastFiles(resp)
	resp.SenseMetrics = BlastMetrics{
		SymbolsTraversed:          1 + len(r.DirectCallers) + len(r.IndirectCallers),
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
	}
	return resp
}

// BuildDiffBlastResponse assembles one BlastResponse from many
// blast.Result inputs — the "diff blast" shape where the subject
// is a set of modified symbols rather than a single one. Dedup is
// by caller symbol id (a caller affected by two modified symbols
// still counts once) and by test file path. Risk aggregates to
// the max tier observed across inputs — the classifier is
// deliberately conservative at the diff level: "any high is the
// answer is high."
//
// The subject line becomes "diff:<ref>" so a consumer reading the
// `symbol` field can tell it apart from a symbol-form response.
// The documented schema's BlastResponse does not define a separate
// diff shape; reusing the same response type keeps 01-05's MCP
// surface one tool, one response.
func BuildDiffBlastResponse(ref string, results []blast.Result, files FileLookup) BlastResponse {
	resp := BlastResponse{
		Symbol: "diff:" + ref,
	}

	directSeen := map[int64]struct{}{}
	indirectSeen := map[int64]struct{}{}
	testsSeen := map[string]struct{}{}
	risk := blast.RiskLow

	for _, r := range results {
		if riskRank(r.Risk) > riskRank(risk) {
			risk = r.Risk
		}
		for _, c := range r.DirectCallers {
			if _, ok := directSeen[c.ID]; ok {
				continue
			}
			directSeen[c.ID] = struct{}{}
			var file string
			if path, ok := files(c.FileID); ok {
				file = path
			}
			resp.DirectCallers = append(resp.DirectCallers, BlastCaller{
				Symbol:    qualifiedOrName(c),
				File:      file,
				LineStart: c.LineStart,
				LineEnd:   c.LineEnd,
				Ref:       FormatRef(file, c.LineStart),
			})
		}
		for _, hop := range r.IndirectCallers {
			if _, ok := indirectSeen[hop.Symbol.ID]; ok {
				continue
			}
			indirectSeen[hop.Symbol.ID] = struct{}{}
			var file string
			if path, ok := files(hop.Symbol.FileID); ok {
				file = path
			}
			resp.IndirectCallers = append(resp.IndirectCallers, BlastIndirect{
				Symbol:    qualifiedOrName(hop.Symbol),
				Via:       qualifiedOrName(hop.Via),
				Hops:      hop.Hops,
				LineStart: hop.Symbol.LineStart,
				LineEnd:   hop.Symbol.LineEnd,
				Ref:       FormatRef(file, hop.Symbol.LineStart),
			})
		}
		for _, t := range r.AffectedTests {
			if _, ok := testsSeen[t]; ok {
				continue
			}
			testsSeen[t] = struct{}{}
			resp.AffectedTests = append(resp.AffectedTests, t)
		}
	}

	resp.Risk = risk
	resp.TotalAffected = len(resp.DirectCallers) + len(resp.IndirectCallers)
	resp.Note = "total_affected counts unique symbols in the blast radius — not source-level references or graph edges traversed."
	resp.RiskFactors = []string{
		fmt.Sprintf("%d modified symbols; %d direct callers", len(results), len(resp.DirectCallers)),
	}

	segmentBlastCallers(&resp)

	uniqueFiles := countUniqueBlastFiles(resp)
	resp.SenseMetrics = BlastMetrics{
		SymbolsTraversed:          len(results) + len(resp.DirectCallers) + len(resp.IndirectCallers),
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
	}
	return resp
}

func countUniqueBlastFiles(resp BlastResponse) int {
	seen := map[string]struct{}{}
	for _, c := range resp.DirectCallers {
		if c.File != "" {
			seen[c.File] = struct{}{}
		}
	}
	for _, t := range resp.AffectedTests {
		seen[t] = struct{}{}
	}
	return len(seen)
}

// riskRank orders the three classifier tiers so BuildDiffBlastResponse
// can take max(perSubjectRisks). Tier names reference the blast
// package's exported constants so the vocabulary has one source of
// truth — a rename there is a compiler error here. Unknown strings
// rank 0 (below low) so a missing Risk field does not silently
// shadow a legitimate "low" aggregate.
func riskRank(r string) int {
	switch r {
	case blast.RiskHigh:
		return 3
	case blast.RiskMedium:
		return 2
	case blast.RiskLow:
		return 1
	}
	return 0
}

// segmentBlastCallers counts production vs test affected symbols
// across all caller lists and sets the summary fields.
func segmentBlastCallers(resp *BlastResponse) {
	var prod, test int
	for _, c := range resp.DirectCallers {
		if IsTestPath(c.File) {
			test++
		} else {
			prod++
		}
	}
	prod += len(resp.IndirectCallers)
	for _, c := range resp.AffectedSubclasses {
		if IsTestPath(c.File) {
			test++
		} else {
			prod++
		}
	}
	for _, c := range resp.AffectedViaComposition {
		if IsTestPath(c.File) {
			test++
		} else {
			prod++
		}
	}
	for _, c := range resp.AffectedViaIncludes {
		if IsTestPath(c.File) {
			test++
		} else {
			prod++
		}
	}
	test += len(resp.AffectedTests)
	resp.ProductionAffected = prod
	resp.TestAffected = test
}

