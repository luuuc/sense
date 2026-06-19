package mcpio

import (
	"context"
	"fmt"

	"github.com/luuuc/sense/internal/blast"
)

const (
	tier1Cap         = 200
	tier2ExamplesCap = 5
	// blastTestExamplesCap bounds the affected-test sample kept when a
	// response must be trimmed to fit a token budget. tests_affected_count
	// still reports the true total.
	blastTestExamplesCap = 10
)

// ApplyBlastBudget trims a blast response in least-relevant-first order
// until its estimated token count fits within budget. Summary counts
// (total_affected, tests_affected_count, references.count) are never
// reduced — only the enumerated lists shrink — so the headline answer
// stays truthful and reachable even when the body is capped. Any trim
// sets Truncated. A non-positive budget disables trimming.
//
// Applied on the MCP path only — the token budget is an LLM-context
// concern. The CLI emits the full (untrimmed) response for piping to
// jq and friends, so the two surfaces can diverge by design.
func ApplyBlastBudget(resp *BlastResponse, budget int) {
	if budget <= 0 {
		return
	}
	// 1. Affected-test sample: tests_affected_count carries the real total.
	if estimateJSONTokens(resp) > budget && len(resp.AffectedTests) > blastTestExamplesCap {
		resp.AffectedTests = resp.AffectedTests[:blastTestExamplesCap]
		resp.Truncated = true
	}
	// 2. Call-site snippets are supporting detail; a caller's identity is
	// the decision-relevant signal for "what breaks?". Shed snippets before
	// dropping any caller, so more callers survive within the same budget.
	if estimateJSONTokens(resp) > budget {
		stripped := false
		for i := range resp.DirectCallers {
			if resp.DirectCallers[i].CallSite != nil {
				resp.DirectCallers[i].CallSite = nil
				stripped = true
			}
		}
		if stripped {
			resp.Truncated = true
		}
	}
	// 3. Indirect callers are a weaker signal than direct callers.
	for estimateJSONTokens(resp) > budget && len(resp.IndirectCallers) > 0 {
		n := len(resp.IndirectCallers)
		resp.IndirectCallers = resp.IndirectCallers[:n-trimStep(n)]
		resp.Truncated = true
	}
	// 4. Direct callers last; keep at least one. total_affected still
	// reports how many exist beyond what is shown.
	for estimateJSONTokens(resp) > budget && len(resp.DirectCallers) > 1 {
		n := len(resp.DirectCallers)
		resp.DirectCallers = resp.DirectCallers[:n-trimStep(n-1)]
		resp.Truncated = true
	}
	// Budget trimming may have dropped callers — recompute the verdict so
	// "complete" never survives a trim that actually shed dependents.
	if resp.Completeness != nil {
		resp.Completeness = blastCompleteness(resp, resp.TotalAffected)
	}
}

// trimStep returns how many trailing items to drop this iteration —
// ~10% of what remains, at least 1 — so trimming a long list converges
// in a handful of marshal passes instead of one pass per dropped item.
func trimStep(n int) int {
	if step := n / 10; step > 1 {
		return step
	}
	return 1
}

// BuildBlastResponse assembles a wire BlastResponse from the blast
// engine's Result plus a file-path lookup for caller symbols. Results
// are partitioned by relevance tier:
//   - Tier 1 (breaks): full detail, capped at 200 items
//   - Tier 2 (references): count + top 5 examples
//   - Tier 3 (tests): count only
func BuildBlastResponse(ctx context.Context, r blast.Result, files FileLookup, snippets *SnippetReader) BlastResponse {
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
			Relation:    "calls " + r.Symbol.Name,
			LineStart:   c.LineStart,
			LineEnd:     c.LineEnd,
			Ref:         FormatRef(file, c.LineStart),
			ViaTemporal: r.DirectTemporalIDs[c.ID],
			CallSite:    snippets.ReadBlastEdgeSite(ctx, c.ID, r.DirectEdgeSites, files),
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
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, Relation: "inherits " + r.Symbol.Name, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
		resp.AffectedSubclasses = append(resp.AffectedSubclasses, entry)
		tier2All = append(tier2All, entry)
	}
	for _, s := range r.AffectedViaComposition {
		var file string
		if path, ok := files(s.FileID); ok {
			file = path
		}
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, Relation: "composes " + r.Symbol.Name, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
		resp.AffectedViaComposition = append(resp.AffectedViaComposition, entry)
		tier2All = append(tier2All, entry)
	}
	for _, s := range r.AffectedViaIncludes {
		var file string
		if path, ok := files(s.FileID); ok {
			file = path
		}
		entry := BlastCaller{Symbol: qualifiedOrName(s), File: file, Relation: "includes " + r.Symbol.Name, LineStart: s.LineStart, LineEnd: s.LineEnd, Ref: FormatRef(file, s.LineStart)}
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

	resp.SnippetsTruncated = snippets.Truncated(len(r.DirectCallers))

	resp.AffectedSymbols = r.TotalAffected
	resp.GraphEdgesTraversed = r.EdgesTraversed
	resp.Note = "affected_symbols counts unique symbols in the blast radius — not source-level references or graph edges traversed. total_affected is an alias for affected_symbols."

	segmentBlastCallers(&resp)

	if r.TotalAffected == 0 && r.SubjectHasCallees {
		resp.VerifyHint = "This symbol has outgoing calls but zero incoming callers in the index. This is unusual — verify with grep before concluding it's unused."
	}

	if subjectFile, ok := files(r.Symbol.FileID); ok {
		resp.IndexCaveat = IndexCaveat(subjectFile)
		// view_edges comes from the engine's ViewReached flag, not from
		// DirectCallers: a view edge has a NULL source_id, so the view never
		// appears as a caller symbol — only the engine's direct edge-table
		// check sees it.
		resp.ViewEdges = viewEdgesSignal(subjectFile, r.ViewReached)
	}

	uniqueFiles := countUniqueBlastFiles(resp)
	resp.AffectedFiles = uniqueFiles
	resp.SenseMetrics = BlastMetrics{
		SymbolsTraversed:          1 + len(r.DirectCallers) + len(r.IndirectCallers),
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
	}
	resp.Completeness = blastCompleteness(&resp, r.TotalAffected)
	return resp
}

// blastCompleteness builds the consolidated stop/verify verdict.
// "complete" means the enumerated set IS the full
// statically-resolvable dependent set — the agent should act on it and
// NOT re-grep. It is deliberately conservative: any budget trim, a hit on
// the direct-caller cap, or affected symbols left un-enumerated downgrades
// to "partial". Dynamic-dispatch residual is left to index_caveat and
// never folded into "complete", so the verdict can't over-claim into a
// duck-typed gap (the Lobsters failure mode).
func blastCompleteness(resp *BlastResponse, totalAffected int) *Completeness {
	// total_affected is the engine's count of the direct+indirect caller union.
	// The inherit/include/compose groups are a re-classification of those SAME
	// caller IDs by edge kind, not additional symbols, so they must NOT be summed
	// in — doing so would understate `hidden` and let "complete" mask callers the
	// tier cap or a trim dropped. Count only the caller union, matching the
	// denomination of total_affected.
	enumerated := len(resp.DirectCallers) + len(resp.IndirectCallers)
	hidden := totalAffected - enumerated
	if hidden < 0 {
		hidden = 0
	}
	capped := len(resp.DirectCallers) >= tier1Cap
	if hidden > 0 || capped {
		return &Completeness{
			Verdict:  "partial",
			Resolved: enumerated,
			Hidden:   hidden,
			Advice:   "Partial: total_affected is the true count. Narrow with a direction or query a specific name for the rest.",
		}
	}
	return &Completeness{
		Verdict:  "complete",
		Resolved: enumerated,
		Advice:   "Complete resolvable dependent set — act on it, do not re-grep. Dynamic-dispatch residual, if any, is in index_caveat.",
	}
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
func BuildDiffBlastResponse(ctx context.Context, ref string, results []blast.Result, files FileLookup, snippets *SnippetReader) BlastResponse {
	resp := BlastResponse{
		Symbol: "diff:" + ref,
	}

	directSeen := map[int64]struct{}{}
	indirectSeen := map[int64]struct{}{}
	testsSeen := map[string]struct{}{}
	risk := blast.RiskLow
	totalEdges := 0

	for _, r := range results {
		totalEdges += r.EdgesTraversed
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
				CallSite:  snippets.ReadBlastEdgeSite(ctx, c.ID, r.DirectEdgeSites, files),
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
	resp.TestsAffectedCount = len(resp.AffectedTests)
	resp.TotalAffected = len(resp.DirectCallers) + len(resp.IndirectCallers)
	resp.AffectedSymbols = resp.TotalAffected
	resp.GraphEdgesTraversed = totalEdges
	resp.Note = "affected_symbols counts unique symbols in the blast radius — not source-level references or graph edges traversed. total_affected is an alias for affected_symbols."
	resp.RiskFactors = []string{
		fmt.Sprintf("%d modified symbols; %d direct callers", len(results), len(resp.DirectCallers)),
	}

	segmentBlastCallers(&resp)

	uniqueFiles := countUniqueBlastFiles(resp)
	resp.AffectedFiles = uniqueFiles
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
