// Package blast computes a symbol's blast radius: the set of symbols
// that would be affected (directly or indirectly) if the subject
// changed. The traversal is a reverse-direction BFS on structural
// edges (calls, inherits, includes, composes, temporal, tests) with
// confidence decay as the primary depth control and MaxHops as a
// hard cap.
//
// The engine reads through a plain *sql.DB handle so it can be used
// by any SQLite consumer (CLI in 01-04, MCP server in 01-05, or
// future watch-mode tooling) without coupling to the sqlite.Adapter
// write path.
//
// Cards 11 (risk formula) and 12 (test association) fill their parts
// of the Result — risk classification and the AffectedTests list.
// This card exposes the Result shape and wires the BFS so those
// follow-ons are additive.
package blast

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

const defaultMaxResults = 100

// MaxFrontierWidth caps BFS frontier per hop — distinct from the SQL batch chunk (500).
const MaxFrontierWidth = 500

// defaultMaxHops matches the pitch's acceptance-criterion call:
// Options{MaxHops: 3, IncludeTests: true}. Callers that pass
// MaxHops: 0 get three hops of traversal — deep enough to be useful
// for blast-radius questions, shallow enough to stay fast on a 30K
// symbol graph.
const defaultMaxHops = 3

const defaultMinConfidence = 0.5

// Options bounds a blast computation. The zero-value Options{} is a
// valid "give me sensible defaults" request:
//
//   - MaxHops 0 (unset) ⇒ three hops. MaxHops is a hard cap kept for
//     backward compatibility; confidence decay is the primary depth
//     control.
//   - MinConfidence 0 (unset) ⇒ 0.5. This is the cumulative path
//     confidence threshold: at each BFS hop the edge confidence is
//     multiplied into the running product, and traversal stops when
//     the product drops below this value.
//   - IncludeTests false ⇒ AffectedTests stays empty; callers opt in.
type Options struct {
	MaxHops       int
	MinConfidence float64
	MaxResults    int
	IncludeTests  bool
}

// Tier classifies a blast result by its relevance to breakage.
type Tier int

const (
	TierBreaks     Tier = 1 // Direct API consumers — calls/temporal edges
	TierReferences Tier = 2 // Associations, composition, inheritance — reference but unlikely to break
	TierTests      Tier = 3 // Test code exercising the symbol
)

// classifyTier maps an edge kind to its relevance tier.
func classifyTier(edgeKind string) Tier {
	switch edgeKind {
	case "calls", "temporal", "member":
		return TierBreaks
	case "tests":
		return TierTests
	default:
		return TierReferences
	}
}

// shouldExpandChildren returns true for concrete type kinds (class, type)
// whose methods should be added to the BFS seed set. Modules and interfaces
// are excluded: interface children are method signatures (not implementations),
// and module children are part of the definition (not consumers).
func shouldExpandChildren(kind model.SymbolKind) bool {
	switch kind {
	case model.KindClass, model.KindType:
		return true
	default:
		return false
	}
}

const (
	// InheritsDecay — type hierarchy is a strong structural signal.
	InheritsDecay = 0.7
	// ComposesDecay — field/association; ORM relationships survive 2 hops.
	ComposesDecay = 0.5
	// IncludesDecay — mixins/imports are weak, prune early.
	IncludesDecay = 0.3
	// StructuralMinConf — lowered floor for structural edges (composes/inherits/includes) so ORM chains survive 2 hops.
	StructuralMinConf = 0.2
	// TestsDecay — test edges are weaker than structural edges but stronger than includes.
	TestsDecay = 0.5
)

// kindDecay returns the confidence multiplier for an edge kind during
// BFS traversal. Per-kind values ensure ORM associations (composes)
// survive two hops while mixins/imports prune early.
func kindDecay(edgeKind string) float64 {
	switch edgeKind {
	case "inherits":
		return InheritsDecay
	case "composes":
		return ComposesDecay
	case "includes":
		return IncludesDecay
	case "tests":
		return TestsDecay
	default:
		return 1.0
	}
}

// CallerHop describes one indirect caller — a symbol reachable from
// the subject via more than one calls-edge step. Via is the caller
// one hop closer to the subject (predecessor on the BFS shortest
// path), so a consumer can render "X calls Y which calls <subject>".
// Hops is 1-indexed from the subject: Hops=2 means "callers of
// direct callers". ViaTemporal is true when this hop traversed a
// temporal coupling edge rather than a structural one.
type CallerHop struct {
	Symbol      model.Symbol
	Via         model.Symbol
	Hops        int
	ViaTemporal bool
}

// Result is the full blast-radius answer for one subject symbol. The
// shape is fixed by the pitch's public API.
//
// Risk is one of RiskLow / RiskMedium / RiskHigh. RiskReasons is
// guaranteed to have at least one entry so consumers can read
// `Reasons[0]` for the primary factor without a length check; the
// slice type leaves room for additional factors (e.g. "crosses
// module boundary") when the pitch's extension clause triggers.
// AffectedTests is populated only when Options.IncludeTests is set;
// the empty, non-nil default keeps JSON encoders stable.
type Result struct {
	Symbol          model.Symbol
	Risk            string
	RiskReasons     []string
	DirectCallers   []model.Symbol
	IndirectCallers []CallerHop
	AffectedTests   []string
	TotalAffected   int
	// DirectTemporalIDs tracks which direct callers were reached via
	// a temporal edge. Keyed by symbol ID.
	DirectTemporalIDs map[int64]bool

	// Edge-kind groups: filtered views over the same nodes that appear
	// in DirectCallers/IndirectCallers. A node appears in at most one
	// group (the edge kind that discovered it first in BFS order).
	AffectedSubclasses     []model.Symbol
	AffectedViaComposition []model.Symbol
	AffectedViaIncludes    []model.Symbol

	// SymbolTiers classifies each affected symbol by relevance tier.
	// Keyed by symbol ID. Used by response shapers to cap output.
	SymbolTiers map[int64]Tier

	Truncated bool
}

// Compute returns the blast radius of symbolIDs under the given
// options. The first ID is the canonical subject for display; all IDs
// seed the BFS frontier at hop 0. This handles Ruby class reopenings
// where a single class is defined across multiple files — callers may
// point to any reopening, so all must be seeds.
//
// For single-symbol queries, pass a one-element slice.
func Compute(ctx context.Context, db *sql.DB, symbolIDs []int64, opts Options) (Result, error) {
	if len(symbolIDs) == 0 {
		return Result{}, fmt.Errorf("blast: no symbol IDs provided")
	}
	if opts.MaxHops <= 0 {
		opts.MaxHops = defaultMaxHops
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = defaultMinConfidence
	}
	if opts.MaxResults <= 0 {
		opts.MaxResults = defaultMaxResults
	}

	subject, err := loadSymbol(ctx, db, symbolIDs[0])
	if err != nil {
		return Result{}, fmt.Errorf("blast: load subject %d: %w", symbolIDs[0], err)
	}

	visited := map[int64]int{}
	predecessor := map[int64]int64{}
	viaTemporal := map[int64]bool{}
	visitedKind := map[int64]string{}
	pathConf := map[int64]float64{}
	frontier := make([]int64, 0, len(symbolIDs))
	for _, id := range symbolIDs {
		visited[id] = 0
		pathConf[id] = 1.0
		frontier = append(frontier, id)
	}

	childSet := map[int64]struct{}{}
	if shouldExpandChildren(subject.Kind) {
		childIDs, err := loadChildIDs(ctx, db, symbolIDs)
		if err != nil {
			return Result{}, fmt.Errorf("blast: load children: %w", err)
		}
		for _, id := range childIDs {
			if _, seen := visited[id]; !seen {
				visited[id] = 0
				pathConf[id] = 1.0
				visitedKind[id] = "member"
				frontier = append(frontier, id)
				childSet[id] = struct{}{}
			}
		}
	}

	truncated := false
	for hop := 1; hop <= opts.MaxHops; hop++ {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("blast: cancelled at hop %d: %w", hop, err)
		}
		pairs, err := expandFrontier(ctx, db, frontier)
		if err != nil {
			return Result{}, fmt.Errorf("blast: hop %d: %w", hop, err)
		}
		var next []int64
		for _, pair := range pairs {
			if _, seen := visited[pair.source]; seen {
				continue
			}
			cumConf := pathConf[pair.target] * pair.confidence * kindDecay(pair.kind)
			minConf := opts.MinConfidence
			if pair.kind == "composes" || pair.kind == "inherits" || pair.kind == "includes" {
				minConf = StructuralMinConf
			}
			if cumConf < minConf {
				continue
			}
			visited[pair.source] = hop
			predecessor[pair.source] = pair.target
			visitedKind[pair.source] = pair.kind
			pathConf[pair.source] = cumConf
			if pair.kind == "temporal" {
				viaTemporal[pair.source] = true
			}
			next = append(next, pair.source)
		}
		// Cap frontier width: evicted nodes are removed from visited so they
		// may be re-discovered via a stronger path in a later hop. This is
		// intentional — it preserves the highest-confidence paths.
		if len(next) > MaxFrontierWidth {
			sort.Slice(next, func(i, j int) bool {
				return pathConf[next[i]] > pathConf[next[j]]
			})
			evicted := next[MaxFrontierWidth:]
			next = next[:MaxFrontierWidth]
			for _, id := range evicted {
				delete(visited, id)
				delete(predecessor, id)
				delete(visitedKind, id)
				delete(pathConf, id)
				delete(viaTemporal, id)
			}
			truncated = true
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}

	// Split visited into children (hop 0, from parent_id), caller-direct
	// (hop ≤1, from edges), and indirect (hop >1). Seeds are skipped.
	// Children bypass MaxResults — they're deterministic (parent_id).
	seedSet := make(map[int64]struct{}, len(symbolIDs))
	for _, id := range symbolIDs {
		seedSet[id] = struct{}{}
	}
	var childDirectIDs, callerDirectIDs, indirectIDs []int64
	for id, hops := range visited {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		if hops <= 1 {
			if _, isChild := childSet[id]; isChild {
				childDirectIDs = append(childDirectIDs, id)
			} else {
				callerDirectIDs = append(callerDirectIDs, id)
			}
		} else {
			indirectIDs = append(indirectIDs, id)
		}
	}

	totalAffectedCount := len(childDirectIDs) + len(callerDirectIDs) + len(indirectIDs)

	callerCount := len(callerDirectIDs) + len(indirectIDs)
	if callerCount > opts.MaxResults {
		type ranked struct {
			id   int64
			conf float64
		}
		all := make([]ranked, 0, callerCount)
		for _, id := range callerDirectIDs {
			all = append(all, ranked{id, pathConf[id]})
		}
		for _, id := range indirectIDs {
			all = append(all, ranked{id, pathConf[id]})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].conf > all[j].conf })
		all = all[:opts.MaxResults]

		kept := make(map[int64]struct{}, opts.MaxResults)
		for _, r := range all {
			kept[r.id] = struct{}{}
		}

		callerDirectIDs = filterIDs(callerDirectIDs, kept)
		indirectIDs = filterIDs(indirectIDs, kept)
	}

	directIDs := childDirectIDs
	directIDs = append(directIDs, callerDirectIDs...)

	// Hydrate both sets to model.Symbol in a single bulk read.

	allIDs := append([]int64{}, directIDs...)
	allIDs = append(allIDs, indirectIDs...)
	// Also hydrate predecessors so CallerHop.Via can reference them.
	// Predecessors are either the subject or another caller already
	// in allIDs; adding them defensively keeps the lookup map whole
	// without a second query.
	predIDs := map[int64]struct{}{}
	for _, predID := range predecessor {
		predIDs[predID] = struct{}{}
	}
	for id := range predIDs {
		if id == subject.ID {
			continue
		}
		if _, seen := visited[id]; seen {
			continue
		}
		allIDs = append(allIDs, id)
	}

	symbolsByID, err := loadSymbols(ctx, db, allIDs)
	if err != nil {
		return Result{}, fmt.Errorf("blast: hydrate callers: %w", err)
	}
	symbolsByID[subject.ID] = subject

	excludeSelf := subject.Kind == model.KindModule || subject.Kind == model.KindInterface
	isSelfMethod := func(sym model.Symbol) bool {
		if !excludeSelf {
			return false
		}
		if sym.ParentID == nil {
			return false
		}
		_, isSeed := seedSet[*sym.ParentID]
		return isSeed
	}

	excluded := 0
	directCallers := make([]model.Symbol, 0, len(directIDs))
	directTemporalIDs := map[int64]bool{}
	for _, id := range directIDs {
		sym, ok := symbolsByID[id]
		if !ok {
			continue
		}
		if isSelfMethod(sym) {
			excluded++
			continue
		}
		directCallers = append(directCallers, sym)
		if viaTemporal[id] {
			directTemporalIDs[id] = true
		}
	}
	sortSymbolsByID(directCallers)

	indirectCallers := make([]CallerHop, 0, len(indirectIDs))
	for _, id := range indirectIDs {
		sym, ok := symbolsByID[id]
		if !ok {
			continue
		}
		if isSelfMethod(sym) {
			excluded++
			continue
		}
		via := symbolsByID[predecessor[id]]
		indirectCallers = append(indirectCallers, CallerHop{
			Symbol:      sym,
			Via:         via,
			Hops:        visited[id],
			ViaTemporal: viaTemporal[id],
		})
	}
	sortHopsByID(indirectCallers)
	totalAffectedCount -= excluded

	affectedTests := []string{}
	if opts.IncludeTests {
		testIDs := append([]int64{subject.ID}, directIDs...)
		testIDs = append(testIDs, indirectIDs...)
		tests, err := loadTestsTargeting(ctx, db, testIDs)
		if err != nil {
			return Result{}, fmt.Errorf("blast: load tests: %w", err)
		}
		affectedTests = tests
	}

	hasTemporalEdge := len(directTemporalIDs) > 0
	if !hasTemporalEdge {
		for _, hop := range indirectCallers {
			if hop.ViaTemporal {
				hasTemporalEdge = true
				break
			}
		}
	}
	risk, reasons := classifyRisk(len(directCallers), hasTemporalEdge)

	symbolTiers := make(map[int64]Tier, len(directIDs)+len(indirectIDs))
	for _, id := range directIDs {
		if sym, ok := symbolsByID[id]; ok && !isSelfMethod(sym) {
			symbolTiers[id] = classifyTier(visitedKind[id])
		}
	}
	for _, id := range indirectIDs {
		if sym, ok := symbolsByID[id]; ok && !isSelfMethod(sym) {
			symbolTiers[id] = classifyTier(visitedKind[id])
		}
	}

	var subclasses, viaComposition, viaIncludes []model.Symbol
	for _, idSlice := range [2][]int64{directIDs, indirectIDs} {
		for _, id := range idSlice {
			sym, ok := symbolsByID[id]
			if !ok || isSelfMethod(sym) {
				continue
			}
			switch visitedKind[id] {
			case "inherits":
				subclasses = append(subclasses, sym)
			case "composes":
				viaComposition = append(viaComposition, sym)
			case "includes":
				viaIncludes = append(viaIncludes, sym)
			}
		}
	}

	return Result{
		Symbol:                 subject,
		Risk:                   risk,
		RiskReasons:            reasons,
		DirectCallers:          directCallers,
		IndirectCallers:        indirectCallers,
		AffectedTests:          affectedTests,
		TotalAffected:          totalAffectedCount,
		DirectTemporalIDs:      directTemporalIDs,
		AffectedSubclasses:     subclasses,
		AffectedViaComposition: viaComposition,
		AffectedViaIncludes:    viaIncludes,
		SymbolTiers:            symbolTiers,
		Truncated:              truncated,
	}, nil
}

// edgePair is the (source, target) shape one BFS hop returns. source
// is the caller we're learning about; target is the node we already
// visited and are expanding from.
type edgePair struct {
	source     int64
	target     int64
	kind       string
	confidence float64
}

// expandFrontier runs the BFS hop query: "which symbols reference
// anything in frontier via structural, temporal, or test edges?"
// Returns (source_id, target_id, kind, confidence) tuples so the
// outer loop can track predecessors, edge kinds, and cumulative
// confidence for grouped output and confidence decay.
//
// Large frontiers are chunked to stay under SQLite's default
// SQLITE_MAX_VARIABLE_NUMBER (999) — at pitch scale (~30K symbols)
// frontiers are typically small, but the chunking guard keeps the
// function robust if a hot subject produces an unusually wide hop.
func expandFrontier(ctx context.Context, db *sql.DB, frontier []int64) ([]edgePair, error) {
	const chunk = 500
	var out []edgePair
	for start := 0; start < len(frontier); start += chunk {
		end := start + chunk
		if end > len(frontier) {
			end = len(frontier)
		}
		batch := frontier[start:end]

		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]

		q := `SELECT source_id, target_id, kind, confidence FROM sense_edges
		      WHERE target_id IN (` + placeholders + `)
		        AND source_id IS NOT NULL
		        AND kind IN ('calls', 'composes', 'includes', 'inherits', 'temporal', 'tests')
		        AND confidence >= 0.1`

		args := make([]any, 0, len(batch))
		for _, id := range batch {
			args = append(args, id)
		}

		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p edgePair
			if err := rows.Scan(&p.source, &p.target, &p.kind, &p.confidence); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out = append(out, p)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// Risk tier labels. Exported so CLI / MCP consumers can compare
// against named values instead of string literals. The pitch fixes
// the three tiers as the complete classifier vocabulary; adding a
// fourth tier would require re-shaping the formula, not just adding
// a constant.
const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

// Risk tier thresholds. Kept as named constants so a reader scanning
// classifyRisk sees the pitch numbers without context — "10" alone
// is arbitrary; "riskHighThreshold" is policy. The pitch explicitly
// says to extend this formula only when a real case proves three
// tiers insufficient, so the thresholds are fixed until then.
const (
	riskHighThreshold   = 10
	riskMediumThreshold = 3
)

// classifyRisk implements the pitch's three-tier formula:
//
//	direct_callers >= 10  → high
//	direct_callers >= 3   → medium
//	otherwise             → low
//
// When temporal coupling edges are present, risk is bumped to at
// least medium — a symbol with 0 structural callers but temporal
// coupling has hidden dependencies.
func classifyRisk(directCallers int, hasTemporal bool) (string, []string) {
	reasons := []string{directCallersReason(directCallers)}
	if hasTemporal {
		reasons = append(reasons, "temporal coupling detected (git co-change history)")
	}
	risk := RiskLow
	switch {
	case directCallers >= riskHighThreshold:
		risk = RiskHigh
	case directCallers >= riskMediumThreshold:
		risk = RiskMedium
	}
	if hasTemporal && risk == RiskLow {
		risk = RiskMedium
	}
	return risk, reasons
}

// directCallersReason formats the direct-caller count as a human
// sentence — "1 direct caller" vs "12 direct callers" — so the
// slice reads naturally in CLI or MCP output.
func directCallersReason(n int) string {
	if n == 1 {
		return "1 direct caller"
	}
	return fmt.Sprintf("%d direct callers", n)
}

func loadChildIDs(ctx context.Context, db *sql.DB, parentIDs []int64) ([]int64, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(parentIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT id FROM sense_symbols WHERE parent_id IN (` + placeholders + `)`
	args := make([]any, 0, len(parentIDs))
	for _, id := range parentIDs {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// loadTestsTargeting returns the file paths of test files whose
// `tests` edges target any of the given symbol ids. Card 12's
// test-association extractor populates those edges; until then, this
// function naturally returns an empty slice because no tests edges
// exist yet.
func loadTestsTargeting(ctx context.Context, db *sql.DB, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return []string{}, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT DISTINCT f.path
	      FROM sense_edges e
	      LEFT JOIN sense_symbols s ON s.id = e.source_id
	      JOIN sense_files f ON f.id = COALESCE(s.file_id, e.file_id)
	      WHERE e.target_id IN (` + placeholders + `)
	        AND e.kind = 'tests'
	      ORDER BY f.path`
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}
