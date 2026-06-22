package mcpio

import (
	"context"
	"fmt"
	"path"
	"sort"

	"github.com/luuuc/sense/internal/blast"
)

const (
	tier1Cap = 200
	// directEnumCap bounds how many direct callers are enumerated inline.
	// On a high-fan-out hub a flat dump of all (up to tier1Cap) direct
	// callers is ~9k tokens in one tool result; the agent rarely needs the
	// full list as line-addressable entries. direct_callers_by_area carries
	// the true magnitude and structural shape, total_affected the true
	// count, so enumerating only the top slice keeps the response small
	// without hiding how big the radius is. The enumerated entries are the
	// actionable subset; the by-area map is the map of the rest. The
	// enumerated subset is the highest-confidence slice (see
	// BuildBlastResponse), so a modest cap still surfaces the most relevant
	// callers, still far under tier1Cap. 60 was chosen to hold recall: it
	// kept Opus's discourse cited-recall flat across three runs (30 was too
	// aggressive and dropped it). The weak/throttled-model case is NOT
	// proven safe — that bench scenario is too noisy to discriminate (see
	// the project memory on the small-subscription bench). directEnumCap and
	// BlastTokenBudget are two distinct knobs: this caps the COUNT of
	// enumerated callers; the budget is the token backstop that trims
	// further only when the enumerated subset (with its snippets) is itself
	// large enough to exceed the budget — at this cap it rarely fires.
	directEnumCap = 60
	// indirectEnumCap bounds the enumerated indirect_callers list. Indirect
	// callers are the weakest "what breaks?" signal (reached via N hops);
	// capping them keeps a high-fan-out blast small. total_affected still
	// reports every indirect caller in the radius.
	indirectEnumCap  = 20
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

// SeenFunc reports whether a symbol id was already returned to this MCP
// session by an earlier sense_graph/sense_blast call. A nil SeenFunc (the
// CLI path and most tests) means "nothing seen yet" — every caller is
// enumerated, so output is byte-identical to the un-deduplicated build.
type SeenFunc func(int64) bool

// seen is the nil-safe predicate: a nil SeenFunc reports false for every id.
func (f SeenFunc) seen(id int64) bool { return f != nil && f(id) }

// BuildBlastResponse assembles a wire BlastResponse from the blast
// engine's Result plus a file-path lookup for caller symbols. Results
// are partitioned by relevance tier:
//   - Tier 1 (breaks): full detail, capped at 200 items
//   - Tier 2 (references): count + top 5 examples
//   - Tier 3 (tests): count only
//
// This is the CLI/test entry point — it enumerates every caller. The MCP
// handler calls BuildBlastResponseSeen to collapse callers the session
// already received from an earlier sense_graph/sense_blast call.
func BuildBlastResponse(ctx context.Context, r blast.Result, files FileLookup, snippets *SnippetReader) BlastResponse {
	return BuildBlastResponseSeen(ctx, r, files, snippets, nil)
}

// BuildBlastResponseSeen is BuildBlastResponse plus per-session deduplication:
// a tier-1 direct caller whose id satisfies seen is dropped from the inline
// direct_callers enumeration and counted into seen_elsewhere instead. The
// magnitude fields (total_affected, direct_callers_by_area) and the
// completeness verdict are computed from the FULL caller set, so the collapse
// saves tokens without hiding radius — the agent already holds the collapsed
// callers from the prior call.
func BuildBlastResponseSeen(ctx context.Context, r blast.Result, files FileLookup, snippets *SnippetReader, seen SeenFunc) BlastResponse {
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
	directTier1 := 0
	var tier2All []BlastCaller

	var tier1Direct []rankedCaller
	seenCount := 0
	var seenFiles []string

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
			// Every tier-1 direct caller feeds the by-area map (the true
			// structural shape) and the directTier1 magnitude count, but only
			// directEnumCap are enumerated inline (the actionable subset).
			// Both run BEFORE the seen-collapse so the magnitude reflects the
			// full set regardless of how many callers the session already has.
			addArea(&resp.DirectCallersByArea, file)
			directTier1++
			// Collapse a caller the session already received from an earlier
			// sense_graph/sense_blast call: count it for seen_elsewhere and
			// skip the inline enumeration. Only this specific id is collapsed
			// — never the whole list — so unseen callers stay fully listed.
			if seen.seen(c.ID) {
				seenCount++
				seenFiles = append(seenFiles, file)
				continue
			}
			tier1Direct = append(tier1Direct, rankedCaller{entry: entry, area: areaOf(file), id: c.ID, conf: r.DirectConfidence[c.ID]})
		} else {
			tier2All = append(tier2All, entry)
		}
	}

	// Enumerate inline breadth-first across areas. Only the enumerated
	// callers carry file:line and are citable, so the subset must span the
	// blast radius rather than sample one corner of it. On a high-fan-out
	// hub every direct caller often has confidence 1.0, so a flat conf-DESC
	// rank degenerates to id-ASC, and IDs cluster by scan directory — the
	// top-cap slice ends up entirely in one big low-ID area while every
	// scattered area is crowded out. Instead: group by area, then round-
	// robin so each area surfaces its best exemplar before any area
	// surfaces a second. Within an area the exemplar is the existing signal
	// (confidence DESC, then ID ASC); areas are visited by descending area
	// count, tiebreak area-name ASC, so the selection and its order are
	// fully deterministic. The by-area map and total_affected still carry
	// the full magnitude.
	resp.DirectCallers = enumerateByArea(tier1Direct, directEnumCap)
	if seenCount > 0 {
		note := fmt.Sprintf("%d of %d direct callers were already returned by an earlier sense_graph/sense_blast call on this symbol; only new callers are listed.",
			seenCount, directTier1)
		if seenCount == directTier1 {
			// All direct callers were already shown — there is no "new" subset.
			note = fmt.Sprintf("all %d direct callers were already returned by an earlier sense_graph/sense_blast call on this symbol; see that response for the list.",
				directTier1)
		}
		resp.SeenVia = &BlastSeenSummary{Count: seenCount, Note: note}
	}
	// Charge the full tier-1 direct count against the shared tier1Cap so
	// indirect enumeration keeps its prior ceiling even though only the top
	// directEnumCap direct callers are enumerated inline. Indirect callers
	// are additionally bounded by indirectEnumCap: on a high-fan-out hub they
	// must not expand to fill the room freed by capping the direct list, or
	// the response stays large for the weakest signal.
	tier1Count = directTier1
	for _, hop := range r.IndirectCallers {
		if !hasTiers || r.SymbolTiers[hop.Symbol.ID] == blast.TierBreaks {
			if tier1Count < tier1Cap && len(resp.IndirectCallers) < indirectEnumCap {
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

	uniqueFiles := countUniqueBlastFiles(resp, seenFiles...)
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
	// Direct callers collapsed into seen_elsewhere are NOT hidden: the agent
	// already received them from an earlier call this session, so they count
	// as resolved. Folding the seen count into `resolved` keeps a complete
	// verdict complete (the seen-collapse is a dedup, not a truncation) and
	// keeps the cap check honest (the full set is still accounted for).
	seenCollapsed := 0
	if resp.SeenVia != nil {
		seenCollapsed = resp.SeenVia.Count
	}
	// total_affected is the engine's count of the direct+indirect caller union.
	// The inherit/include/compose groups are a re-classification of those SAME
	// caller IDs by edge kind, not additional symbols, so they must NOT be summed
	// in — doing so would understate `hidden` and let "complete" mask callers the
	// tier cap or a trim dropped. Count only the caller union, matching the
	// denomination of total_affected.
	enumerated := len(resp.DirectCallers) + len(resp.IndirectCallers) + seenCollapsed
	hidden := totalAffected - enumerated
	if hidden < 0 {
		hidden = 0
	}
	// The enumerated direct_callers are capped at directEnumCap; the by-area
	// map carries the full tier-1 direct count. If more direct callers exist
	// than were enumerated (or collapsed as already-seen), the response is
	// partial even when total_affected happens to match.
	capped := sumAreas(resp.DirectCallersByArea) > len(resp.DirectCallers)+seenCollapsed
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
	return BuildDiffBlastResponseSeen(ctx, ref, results, files, snippets, nil)
}

// BuildDiffBlastResponseSeen is BuildDiffBlastResponse plus per-session
// deduplication: a direct caller whose id satisfies seen is collapsed into
// seen_elsewhere instead of being enumerated. total_affected still counts the
// full deduplicated caller union, so the radius is unchanged.
func BuildDiffBlastResponseSeen(ctx context.Context, ref string, results []blast.Result, files FileLookup, snippets *SnippetReader, seen SeenFunc) BlastResponse {
	resp := BlastResponse{
		Symbol: "diff:" + ref,
	}

	directSeen := map[int64]struct{}{}
	indirectSeen := map[int64]struct{}{}
	testsSeen := map[string]struct{}{}
	risk := blast.RiskLow
	totalEdges := 0
	directTotal := 0
	seenCount := 0
	var seenFiles []string

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
			// Count every unique direct caller toward the radius BEFORE the
			// seen-collapse, then collapse the ones the session already holds.
			directTotal++
			var file string
			if path, ok := files(c.FileID); ok {
				file = path
			}
			if seen.seen(c.ID) {
				seenCount++
				seenFiles = append(seenFiles, file)
				continue
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

	if seenCount > 0 {
		resp.SeenVia = &BlastSeenSummary{
			Count: seenCount,
			Note: fmt.Sprintf("%d of %d direct callers were already returned by an earlier sense_graph/sense_blast call on this symbol; only new callers are listed.",
				seenCount, directTotal),
		}
	}

	resp.Risk = risk
	resp.TestsAffectedCount = len(resp.AffectedTests)
	// directTotal, not the post-collapse enumeration length, so total_affected
	// and the risk-factor count report the full deduplicated radius.
	resp.TotalAffected = directTotal + len(resp.IndirectCallers)
	resp.AffectedSymbols = resp.TotalAffected
	resp.GraphEdgesTraversed = totalEdges
	resp.Note = "affected_symbols counts unique symbols in the blast radius — not source-level references or graph edges traversed. total_affected is an alias for affected_symbols."
	resp.RiskFactors = []string{
		fmt.Sprintf("%d modified symbols; %d direct callers", len(results), directTotal),
	}

	segmentBlastCallers(&resp)

	uniqueFiles := countUniqueBlastFiles(resp, seenFiles...)
	resp.AffectedFiles = uniqueFiles
	resp.SenseMetrics = BlastMetrics{
		SymbolsTraversed:          len(results) + directTotal + len(resp.IndirectCallers),
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
	}
	return resp
}

// rankedCaller pairs a tier-1 direct caller's wire entry with the keys
// that drive area-stratified enumeration: area groups callers into
// subsystems, conf/id pick the exemplar within an area.
type rankedCaller struct {
	entry BlastCaller
	area  string
	id    int64
	conf  float64
}

// areaOf returns the subsystem key for a file — its parent directory,
// the same key addArea tallies. An unresolved file falls under ".".
func areaOf(file string) string {
	if file == "" {
		return "."
	}
	return path.Dir(file)
}

// enumerateByArea selects up to cap direct callers breadth-first across
// areas. Each area contributes its best exemplar before any area
// contributes a second, so a small scattered area (lib/email) surfaces a
// citable exemplar instead of being crowded out by a big low-ID area.
// Areas are visited by descending member count, tiebreak area-name ASC;
// within an area the exemplar order is the existing signal (confidence
// DESC, then ID ASC). The returned slice is area-clustered in that same
// visitation order, so the reader sees the enumerated callers by
// subsystem. Selection and order are deterministic across builds.
//
// When the callers span MORE areas than limit, round-robin seats one per
// area, so the most-populated limit areas each get an exemplar and the
// smaller tail is represented only in direct_callers_by_area — summarised,
// never hidden (see TestBuildBlastResponseMoreAreasThanCap). Descending
// count means a bigger (more-affected) subsystem is never dropped for a
// one-off; the long tail loses its inline exemplar but keeps its true
// count in by_area, which is what makes that tradeoff acceptable.
func enumerateByArea(callers []rankedCaller, limit int) []BlastCaller {
	if len(callers) == 0 || limit <= 0 {
		return nil
	}

	// Bucket by area, then rank within each bucket by the existing signal.
	buckets := map[string][]rankedCaller{}
	for _, rc := range callers {
		buckets[rc.area] = append(buckets[rc.area], rc)
	}
	areas := make([]string, 0, len(buckets))
	for area, b := range buckets {
		sort.SliceStable(b, func(i, j int) bool {
			if b[i].conf != b[j].conf {
				return b[i].conf > b[j].conf
			}
			return b[i].id < b[j].id
		})
		buckets[area] = b
		areas = append(areas, area)
	}
	// Deterministic area visitation: most-populated first (its breadth is
	// most worth sampling), tiebreak by area name so two equal-count areas
	// always order the same way.
	sort.SliceStable(areas, func(i, j int) bool {
		if len(buckets[areas[i]]) != len(buckets[areas[j]]) {
			return len(buckets[areas[i]]) > len(buckets[areas[j]])
		}
		return areas[i] < areas[j]
	})

	// Round-robin: rank r takes the r-th exemplar from every area in turn,
	// so each area surfaces its best before any surfaces a second. Stop at
	// cap. Areas that received a slot in this pass are emitted
	// area-clustered (in visitation order) so the reader groups them by
	// subsystem; the round-robin only decides WHICH callers are chosen.
	chosen := map[string][]BlastCaller{}
	picked := 0
	for rank := 0; picked < limit; rank++ {
		progressed := false
		for _, area := range areas {
			b := buckets[area]
			if rank >= len(b) {
				continue
			}
			progressed = true
			chosen[area] = append(chosen[area], b[rank].entry)
			picked++
			if picked >= limit {
				break
			}
		}
		if !progressed {
			break // every area exhausted
		}
	}

	out := make([]BlastCaller, 0, picked)
	for _, area := range areas {
		out = append(out, chosen[area]...)
	}
	return out
}

// addArea increments the by-area tally for a caller's file directory.
// The area is the file's parent directory — coarse enough to name a
// subsystem (app/models, app/jobs) yet fine enough to distinguish them.
// A file at the repo root, or a caller with no resolved file, falls under
// ".". The map is lazily allocated so a zero-caller blast omits the field.
func addArea(m *map[string]int, file string) {
	if *m == nil {
		*m = make(map[string]int)
	}
	(*m)[areaOf(file)]++
}

// sumAreas totals the by-area direct-caller tally — the true tier-1
// direct count, even when direct_callers enumerates only the top slice.
func sumAreas(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// countUniqueBlastFiles counts the distinct files across the enumerated
// direct callers and affected tests, plus any extra paths supplied. The
// extra paths carry the files of callers collapsed by the seen-dedup, so
// affected_files reports the same magnitude whether or not a caller was
// already returned earlier this session.
func countUniqueBlastFiles(resp BlastResponse, extra ...string) int {
	seen := map[string]struct{}{}
	for _, c := range resp.DirectCallers {
		if c.File != "" {
			seen[c.File] = struct{}{}
		}
	}
	for _, t := range resp.AffectedTests {
		seen[t] = struct{}{}
	}
	for _, f := range extra {
		if f != "" {
			seen[f] = struct{}{}
		}
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
