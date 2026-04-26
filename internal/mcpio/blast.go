package mcpio

import (
	"fmt"

	"github.com/luuuc/sense/internal/blast"
)

// BuildBlastResponse assembles a wire BlastResponse from the blast
// engine's Result plus a file-path lookup for caller symbols. The
// engine's Result holds model.Symbol records (with FileID) and
// pre-resolved AffectedTests file paths; this builder turns both
// into the documented (symbol, file) / (symbol, via, hops) shapes.
//
// A FileLookup miss renders an empty File string — DirectCallers
// are by construction indexed symbols (they live in sense_symbols),
// so the only way to miss is a race between the blast read and the
// file-path read. Empty is more honest than omitting the entry.
func BuildBlastResponse(r blast.Result, files FileLookup) BlastResponse {
	resp := BlastResponse{
		Symbol:        qualifiedOrName(r.Symbol),
		Risk:          r.Risk,
		RiskFactors:   append([]string{}, r.RiskReasons...),
		AffectedTests: append([]string{}, r.AffectedTests...),
		TotalAffected: r.TotalAffected,
	}

	for _, c := range r.DirectCallers {
		var file string
		if path, ok := files(c.FileID); ok {
			file = path
		}
		resp.DirectCallers = append(resp.DirectCallers, BlastCaller{
			Symbol:      qualifiedOrName(c),
			File:        file,
			ViaTemporal: r.DirectTemporalIDs[c.ID],
		})
	}
	for _, hop := range r.IndirectCallers {
		resp.IndirectCallers = append(resp.IndirectCallers, BlastIndirect{
			Symbol:      qualifiedOrName(hop.Symbol),
			Via:         qualifiedOrName(hop.Via),
			Hops:        hop.Hops,
			ViaTemporal: hop.ViaTemporal,
		})
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
				Symbol: qualifiedOrName(c),
				File:   file,
			})
		}
		for _, hop := range r.IndirectCallers {
			if _, ok := indirectSeen[hop.Symbol.ID]; ok {
				continue
			}
			indirectSeen[hop.Symbol.ID] = struct{}{}
			resp.IndirectCallers = append(resp.IndirectCallers, BlastIndirect{
				Symbol: qualifiedOrName(hop.Symbol),
				Via:    qualifiedOrName(hop.Via),
				Hops:   hop.Hops,
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
	resp.RiskFactors = []string{
		fmt.Sprintf("%d modified symbols; %d direct callers", len(results), len(resp.DirectCallers)),
	}

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

