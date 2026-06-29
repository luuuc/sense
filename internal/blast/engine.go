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
	case "calls", "temporal", "member", "references":
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
// EdgeSite records the file and line of an edge for snippet generation.
type EdgeSite struct {
	FileID *int64
	Line   *int
}

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

	// DirectEdgeSites records the edge file/line for each direct caller,
	// keyed by symbol ID. Used for call-site snippet generation.
	DirectEdgeSites map[int64]EdgeSite

	// DirectConfidence records the cumulative path confidence for each
	// direct caller, keyed by symbol ID. This is the same score the
	// engine ranks by at the MaxResults cap boundary (capResults). Response
	// shapers use it to enumerate the highest-confidence subset inline when
	// the full direct list is capped: only enumerated callers carry
	// file:line and are citable, so the enumerated slice must be the most
	// relevant callers, not an arbitrary (ID-ordered) prefix.
	DirectConfidence map[int64]float64

	EdgesTraversed    int
	SubjectHasCallees bool
	// ViewReached is true when a view template (ERB) reaches the subject via a
	// calls/references edge. Such edges have a NULL source_id (the source is a
	// template, not a symbol), so they never appear in DirectCallers — the BFS
	// only traverses symbol→symbol edges. ViewReached is the only place a
	// view-helper or Stimulus-dispatched symbol's view reachability surfaces.
	ViewReached bool

	// Edge-kind groups. A node appears in at most one group. Subclasses and
	// includes are filtered views over the capped DirectCallers/IndirectCallers,
	// bucketed by the edge kind that discovered each node first in BFS order.
	// AffectedViaComposition is computed independently from the edge table (the
	// complete reverse-composition set), so it may surface composers the result
	// cap evicted from the caller lists — that is the point on high-fan-out hubs.
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
	opts = opts.withDefaults()

	subject, err := loadSymbol(ctx, db, symbolIDs[0])
	if err != nil {
		return Result{}, fmt.Errorf("blast: load subject %d: %w", symbolIDs[0], err)
	}

	hasCallees := subjectHasCallees(ctx, db, symbolIDs)
	viewReached := subjectViewReached(ctx, db, symbolIDs)

	state, err := seedFrontier(ctx, db, subject, symbolIDs)
	if err != nil {
		return Result{}, err
	}

	for hop := 1; hop <= opts.MaxHops; hop++ {
		if err := ctx.Err(); err != nil {
			return Result{}, fmt.Errorf("blast: cancelled at hop %d: %w", hop, err)
		}
		if err := state.expandOneHop(ctx, db, hop, opts); err != nil {
			return Result{}, err
		}
		if len(state.frontier) == 0 {
			break
		}
	}

	// Split visited into caller-direct (hop ≤1, from edges) and indirect
	// (hop >1). Seeds and children are skipped from output — children seed
	// the BFS frontier so callers of methods are found, but they are not
	// part of the blast radius (they are the symbol's own members).
	seedSet := make(map[int64]struct{}, len(symbolIDs))
	for _, id := range symbolIDs {
		seedSet[id] = struct{}{}
	}
	directIDs, indirectIDs := state.partition(seedSet)
	totalAffectedCount := len(directIDs) + len(indirectIDs)
	// Snapshot the uncapped affected set so reverse-composition dependents the BFS
	// pruned by confidence (surfaced below from the edge table) can be reconciled
	// into total_affected rather than appearing only in the composition list.
	affectedSet := idSet(directIDs, indirectIDs)
	directIDs, indirectIDs = state.capResults(directIDs, indirectIDs, opts.MaxResults)

	symbolsByID, err := state.hydrate(ctx, db, subject, directIDs, indirectIDs)
	if err != nil {
		return Result{}, fmt.Errorf("blast: hydrate callers: %w", err)
	}

	isSelf := selfMethodPredicate(subject, seedSet)
	directCallers, directTemporalIDs, directEdgeSites, directConfidence, excludedD := state.buildDirectCallers(directIDs, symbolsByID, isSelf)
	indirectCallers, excludedI := state.buildIndirectCallers(indirectIDs, symbolsByID, isSelf)
	totalAffectedCount -= excludedD + excludedI

	affectedTests := []string{}
	if opts.IncludeTests {
		affectedTests, err = state.loadAffectedTests(ctx, db, subject, directIDs, indirectIDs)
		if err != nil {
			return Result{}, fmt.Errorf("blast: load tests: %w", err)
		}
	}

	risk, reasons := classifyRisk(len(directCallers), hasTemporal(directTemporalIDs, indirectCallers))
	symbolTiers := state.buildTiers(directIDs, indirectIDs, symbolsByID, isSelf)
	subclasses, viaIncludes := state.buildEdgeKindGroups(directIDs, indirectIDs, symbolsByID, isSelf)

	// Composition is derived from the edge table directly rather than from the
	// capped/visitedKind-bucketed callers: on a high-fan-out hub the reverse
	// composers (e.g. every Django model holding a ForeignKey to this one) ride
	// low-confidence composes edges that capResults evicts beneath the 1.0
	// call/test callers, and a composer that also calls the subject is recorded
	// under the calls edge and never bucketed here. excludeGrouped keeps the
	// one-node-one-group invariant against the inherits/includes buckets above.
	excludeGrouped := symbolIDSet(subclasses, viaIncludes)
	viaComposition, err := state.loadReverseComposition(ctx, db, symbolIDs, seedSet, excludeGrouped, isSelf, opts.MaxResults)
	if err != nil {
		return Result{}, fmt.Errorf("blast: reverse composition: %w", err)
	}
	// Count any composer the BFS did not visit (pruned by confidence) into the
	// total so total_affected never under-reports what the response surfaces.
	totalAffectedCount += countUnvisited(viaComposition, affectedSet)

	return Result{
		Symbol:                 subject,
		Risk:                   risk,
		RiskReasons:            reasons,
		DirectCallers:          directCallers,
		IndirectCallers:        indirectCallers,
		AffectedTests:          affectedTests,
		TotalAffected:          totalAffectedCount,
		EdgesTraversed:         state.edgesTraversed,
		SubjectHasCallees:      hasCallees,
		ViewReached:            viewReached,
		DirectTemporalIDs:      directTemporalIDs,
		DirectEdgeSites:        directEdgeSites,
		DirectConfidence:       directConfidence,
		AffectedSubclasses:     subclasses,
		AffectedViaComposition: viaComposition,
		AffectedViaIncludes:    viaIncludes,
		SymbolTiers:            symbolTiers,
		Truncated:              state.truncated,
	}, nil
}

// bfsState carries the mutable traversal state of one blast computation: the
// visited hop-distance map and the per-node path metadata the BFS records
// (predecessor, edge kind, cumulative confidence, edge site, and whether the
// node was reached only via a temporal edge), the type's own members that seed
// the frontier but are excluded from output, the current frontier, and the
// running edge/truncation counters.
type bfsState struct {
	visited        map[int64]int
	predecessor    map[int64]int64
	viaTemporal    map[int64]bool
	visitedKind    map[int64]string
	pathConf       map[int64]float64
	edgeSites      map[int64]EdgeSite
	frontier       []int64
	childSet       map[int64]struct{}
	truncated      bool
	edgesTraversed int
}

// withDefaults fills the zero-value option fields with their defaults: MaxHops
// 0 ⇒ three hops, MinConfidence 0 ⇒ 0.5, MaxResults 0 ⇒ 100.
func (o Options) withDefaults() Options {
	if o.MaxHops <= 0 {
		o.MaxHops = defaultMaxHops
	}
	if o.MinConfidence <= 0 {
		o.MinConfidence = defaultMinConfidence
	}
	if o.MaxResults <= 0 {
		o.MaxResults = defaultMaxResults
	}
	return o
}

// seedFrontier builds the initial BFS state: the subject IDs seed the frontier
// at hop 0, and for concrete types the type's own methods are added as members
// (so callers of methods are discoverable) tracked in childSet so they can be
// excluded from the radius output.
func seedFrontier(ctx context.Context, db *sql.DB, subject model.Symbol, symbolIDs []int64) (*bfsState, error) {
	s := &bfsState{
		visited:     map[int64]int{},
		predecessor: map[int64]int64{},
		viaTemporal: map[int64]bool{},
		visitedKind: map[int64]string{},
		pathConf:    map[int64]float64{},
		edgeSites:   map[int64]EdgeSite{},
		frontier:    make([]int64, 0, len(symbolIDs)),
		childSet:    map[int64]struct{}{},
	}
	for _, id := range symbolIDs {
		s.visited[id] = 0
		s.pathConf[id] = 1.0
		s.frontier = append(s.frontier, id)
	}

	if shouldExpandChildren(subject.Kind) {
		childIDs, err := loadChildIDs(ctx, db, symbolIDs)
		if err != nil {
			return nil, fmt.Errorf("blast: load children: %w", err)
		}
		for _, id := range childIDs {
			if _, seen := s.visited[id]; !seen {
				s.visited[id] = 0
				s.pathConf[id] = 1.0
				s.visitedKind[id] = "member"
				s.frontier = append(s.frontier, id)
				s.childSet[id] = struct{}{}
			}
		}
	}
	return s, nil
}

// expandOneHop advances the BFS by one hop: it expands the current frontier,
// groups qualifying in-edges per source, admits each source via its chosen
// path (the temporal-sink decision lives in chooseHopCand), caps the frontier
// width, and replaces the frontier with the surviving structural expansions.
func (s *bfsState) expandOneHop(ctx context.Context, db *sql.DB, hop int, opts Options) error {
	pairs, err := expandFrontier(ctx, db, s.frontier)
	if err != nil {
		return fmt.Errorf("blast: hop %d: %w", hop, err)
	}
	s.edgesTraversed += len(pairs)

	bySource, order := s.groupBySource(pairs, opts)
	next := s.admitSources(order, bySource, hop)
	s.frontier = s.capFrontier(next)
	return nil
}

// groupBySource collects this hop's qualifying in-edges by source node, so the
// expand-vs-sink and predecessor decisions are made per-node rather than
// per-edge in iteration order. An edge qualifies when its source is unvisited
// and the cumulative path confidence clears the threshold (a lower structural
// floor lets ORM chains survive two hops). order preserves first-seen source
// order for deterministic output.
func (s *bfsState) groupBySource(pairs []edgePair, opts Options) (map[int64][]hopCand, []int64) {
	bySource := map[int64][]hopCand{}
	var order []int64
	for _, pair := range pairs {
		if _, seen := s.visited[pair.source]; seen {
			continue
		}
		cumConf := s.pathConf[pair.target] * pair.confidence * kindDecay(pair.kind)
		minConf := opts.MinConfidence
		if pair.kind == "composes" || pair.kind == "inherits" || pair.kind == "includes" {
			minConf = StructuralMinConf
		}
		if cumConf < minConf {
			continue
		}
		if _, ok := bySource[pair.source]; !ok {
			order = append(order, pair.source)
		}
		bySource[pair.source] = append(bySource[pair.source], hopCand{
			target:  pair.target,
			kind:    pair.kind,
			cumConf: cumConf,
			fileID:  pair.fileID,
			line:    pair.line,
		})
	}
	return bySource, order
}

// chooseHopCand picks the edge a node is recorded as reached by, and reports
// whether the node has any structural in-edge (and therefore expands). A
// non-temporal edge is preferred when one exists — temporal is only the
// recorded route when nothing else reaches the node — then highest cumulative
// confidence. This is the temporal-sink rule: a node reached this hop ONLY via
// temporal edges has hasStructural=false, so it is recorded but never expanded,
// and a temporal hop cannot launder git co-change into a transitive structural
// path.
func chooseHopCand(cands []hopCand) (hopCand, bool) {
	hasStructural := false
	for _, c := range cands {
		if c.kind != "temporal" {
			hasStructural = true
			break
		}
	}
	var chosen hopCand
	chosenSet := false
	for _, c := range cands {
		if hasStructural && c.kind == "temporal" {
			continue
		}
		if !chosenSet || c.cumConf > chosen.cumConf {
			chosen = c
			chosenSet = true
		}
	}
	return chosen, hasStructural
}

// admitSources records each grouped source at this hop via its chosen path and
// returns the nodes that expand next (those with a structural in-edge). A node
// reached only via temporal edges is recorded as a sink but not returned.
func (s *bfsState) admitSources(order []int64, bySource map[int64][]hopCand, hop int) []int64 {
	var next []int64
	for _, src := range order {
		chosen, hasStructural := chooseHopCand(bySource[src])
		s.visited[src] = hop
		s.predecessor[src] = chosen.target
		s.visitedKind[src] = chosen.kind
		s.pathConf[src] = chosen.cumConf
		s.edgeSites[src] = EdgeSite{FileID: chosen.fileID, Line: chosen.line}
		if chosen.kind == "temporal" {
			s.viaTemporal[src] = true
		}
		if hasStructural {
			next = append(next, src)
		}
	}
	return next
}

// capFrontier caps the next frontier to MaxFrontierWidth, keeping the
// highest-confidence nodes. Evicted nodes are removed from visited so they may
// be re-discovered via a stronger path in a later hop — this preserves the
// highest-confidence paths. It records truncation when eviction happens.
func (s *bfsState) capFrontier(next []int64) []int64 {
	if len(next) <= MaxFrontierWidth {
		return next
	}
	sort.Slice(next, func(i, j int) bool {
		return s.pathConf[next[i]] > s.pathConf[next[j]]
	})
	evicted := next[MaxFrontierWidth:]
	next = next[:MaxFrontierWidth]
	for _, id := range evicted {
		delete(s.visited, id)
		delete(s.predecessor, id)
		delete(s.visitedKind, id)
		delete(s.pathConf, id)
		delete(s.viaTemporal, id)
	}
	s.truncated = true
	return next
}

// partition splits the visited set into direct callers (hop ≤1) and indirect
// callers (hop >1). Seeds and the subject's own members are skipped: members
// seed the frontier so callers of methods are found, but they are not part of
// the blast radius.
func (s *bfsState) partition(seedSet map[int64]struct{}) (direct, indirect []int64) {
	for id, hops := range s.visited {
		if _, isSeed := seedSet[id]; isSeed {
			continue
		}
		if _, isChild := s.childSet[id]; isChild {
			continue
		}
		if hops <= 1 {
			direct = append(direct, id)
		} else {
			indirect = append(indirect, id)
		}
	}
	return direct, indirect
}

// capResults trims the caller sets to maxResults total, keeping the
// highest-confidence callers across both sets.
func (s *bfsState) capResults(directIDs, indirectIDs []int64, maxResults int) ([]int64, []int64) {
	callerCount := len(directIDs) + len(indirectIDs)
	if callerCount <= maxResults {
		return directIDs, indirectIDs
	}
	type ranked struct {
		id     int64
		conf   float64
		direct bool
	}
	all := make([]ranked, 0, callerCount)
	for _, id := range directIDs {
		all = append(all, ranked{id, s.pathConf[id], true})
	}
	for _, id := range indirectIDs {
		all = append(all, ranked{id, s.pathConf[id], false})
	}
	// Rank by confidence, then prefer direct callers, then break ties on
	// symbol ID. The two tie-breakers matter on high-fan-out hubs where
	// almost every path is a 1.0 call edge, so callers cluster at one
	// confidence and the cap cutoff lands among the ties:
	//   - direct-over-indirect keeps the "what breaks?" signal — a direct
	//     caller must not be evicted to make room for a weaker indirect one;
	//   - the ID tie-break makes the kept set deterministic, so repeated
	//     blasts of the same symbol return the same callers (without it an
	//     unstable sort keeps an arbitrary subset that varies run to run and
	//     "audit every dependent" is unreproducible).
	sort.Slice(all, func(i, j int) bool {
		if all[i].conf != all[j].conf {
			return all[i].conf > all[j].conf
		}
		if all[i].direct != all[j].direct {
			return all[i].direct
		}
		return all[i].id < all[j].id
	})
	all = all[:maxResults]

	kept := make(map[int64]struct{}, maxResults)
	for _, r := range all {
		kept[r.id] = struct{}{}
	}
	return filterIDs(directIDs, kept), filterIDs(indirectIDs, kept)
}

// hydrate bulk-reads the caller symbols and their predecessors (so CallerHop.Via
// can reference them) in a single query, then pins the subject into the map.
func (s *bfsState) hydrate(ctx context.Context, db *sql.DB, subject model.Symbol, directIDs, indirectIDs []int64) (map[int64]model.Symbol, error) {
	allIDs := append([]int64{}, directIDs...)
	allIDs = append(allIDs, indirectIDs...)
	// Also hydrate predecessors so CallerHop.Via can reference them.
	// Predecessors are either the subject or another caller already
	// in allIDs; adding them defensively keeps the lookup map whole
	// without a second query.
	predIDs := map[int64]struct{}{}
	for _, predID := range s.predecessor {
		predIDs[predID] = struct{}{}
	}
	for id := range predIDs {
		if id == subject.ID {
			continue
		}
		if _, seen := s.visited[id]; seen {
			continue
		}
		allIDs = append(allIDs, id)
	}

	symbolsByID, err := loadSymbols(ctx, db, allIDs)
	if err != nil {
		return nil, err
	}
	symbolsByID[subject.ID] = subject
	return symbolsByID, nil
}

// selfMethodFn reports whether a symbol is a member of the subject itself
// (excluded from the radius when the subject is a module or interface).
type selfMethodFn func(model.Symbol) bool

// selfMethodPredicate returns a predicate that, for a module/interface subject,
// reports whether a symbol is one of the subject's own members. For other
// subject kinds it always returns false (nothing is excluded as self).
func selfMethodPredicate(subject model.Symbol, seedSet map[int64]struct{}) selfMethodFn {
	excludeSelf := subject.Kind == model.KindModule || subject.Kind == model.KindInterface
	return func(sym model.Symbol) bool {
		if !excludeSelf {
			return false
		}
		if sym.ParentID == nil {
			return false
		}
		_, isSeed := seedSet[*sym.ParentID]
		return isSeed
	}
}

// buildDirectCallers hydrates the direct-caller IDs into symbols (skipping the
// subject's own members), recording which were reached via a temporal edge,
// each one's edge site, and its cumulative path confidence. Returns the
// callers, the temporal-ID set, the edge-site map, the confidence map, and the
// count excluded as self-members.
func (s *bfsState) buildDirectCallers(directIDs []int64, symbolsByID map[int64]model.Symbol, isSelf selfMethodFn) ([]model.Symbol, map[int64]bool, map[int64]EdgeSite, map[int64]float64, int) {
	excluded := 0
	callers := make([]model.Symbol, 0, len(directIDs))
	temporalIDs := map[int64]bool{}
	edgeSites := make(map[int64]EdgeSite, len(directIDs))
	confidence := make(map[int64]float64, len(directIDs))
	for _, id := range directIDs {
		sym, ok := symbolsByID[id]
		if !ok {
			continue
		}
		if isSelf(sym) {
			excluded++
			continue
		}
		callers = append(callers, sym)
		if s.viaTemporal[id] {
			temporalIDs[id] = true
		}
		if es, ok := s.edgeSites[id]; ok {
			edgeSites[id] = es
		}
		confidence[id] = s.pathConf[id]
	}
	sortSymbolsByID(callers)
	return callers, temporalIDs, edgeSites, confidence, excluded
}

// buildIndirectCallers hydrates the indirect-caller IDs into CallerHops
// (skipping self-members), attaching each hop's predecessor, hop distance, and
// temporal flag. Returns the hops and the count excluded as self-members.
func (s *bfsState) buildIndirectCallers(indirectIDs []int64, symbolsByID map[int64]model.Symbol, isSelf selfMethodFn) ([]CallerHop, int) {
	excluded := 0
	callers := make([]CallerHop, 0, len(indirectIDs))
	for _, id := range indirectIDs {
		sym, ok := symbolsByID[id]
		if !ok {
			continue
		}
		if isSelf(sym) {
			excluded++
			continue
		}
		via := symbolsByID[s.predecessor[id]]
		callers = append(callers, CallerHop{
			Symbol:      sym,
			Via:         via,
			Hops:        s.visited[id],
			ViaTemporal: s.viaTemporal[id],
		})
	}
	sortHopsByID(callers)
	return callers, excluded
}

// loadAffectedTests collects the test files targeting the subject, its members,
// and the affected callers. Members are included so tests directly targeting
// the subject's methods surface even though members are excluded from output.
func (s *bfsState) loadAffectedTests(ctx context.Context, db *sql.DB, subject model.Symbol, directIDs, indirectIDs []int64) ([]string, error) {
	testIDs := make([]int64, 0, 1+len(s.childSet)+len(directIDs)+len(indirectIDs))
	testIDs = append(testIDs, subject.ID)
	for id := range s.childSet {
		testIDs = append(testIDs, id)
	}
	testIDs = append(testIDs, directIDs...)
	testIDs = append(testIDs, indirectIDs...)
	return loadTestsTargeting(ctx, db, testIDs)
}

// hasTemporal reports whether any affected caller — direct or indirect — was
// reached via a temporal (git co-change) edge, the signal that bumps risk.
func hasTemporal(directTemporalIDs map[int64]bool, indirectCallers []CallerHop) bool {
	if len(directTemporalIDs) > 0 {
		return true
	}
	for _, hop := range indirectCallers {
		if hop.ViaTemporal {
			return true
		}
	}
	return false
}

// buildTiers classifies each affected caller (skipping self-members) into its
// relevance tier, keyed by symbol ID.
func (s *bfsState) buildTiers(directIDs, indirectIDs []int64, symbolsByID map[int64]model.Symbol, isSelf selfMethodFn) map[int64]Tier {
	tiers := make(map[int64]Tier, len(directIDs)+len(indirectIDs))
	for _, ids := range [2][]int64{directIDs, indirectIDs} {
		for _, id := range ids {
			if sym, ok := symbolsByID[id]; ok && !isSelf(sym) {
				tiers[id] = classifyTier(s.visitedKind[id])
			}
		}
	}
	return tiers
}

// idSet collects two id slices into a membership set.
func idSet(a, b []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(a)+len(b))
	for _, id := range a {
		set[id] = struct{}{}
	}
	for _, id := range b {
		set[id] = struct{}{}
	}
	return set
}

// symbolIDSet collects the ids of two symbol slices into a membership set.
func symbolIDSet(a, b []model.Symbol) map[int64]struct{} {
	set := make(map[int64]struct{}, len(a)+len(b))
	for _, sym := range a {
		set[sym.ID] = struct{}{}
	}
	for _, sym := range b {
		set[sym.ID] = struct{}{}
	}
	return set
}

// countUnvisited returns how many of syms are absent from visited — the
// composers the BFS pruned by confidence but the edge-table query still surfaced.
func countUnvisited(syms []model.Symbol, visited map[int64]struct{}) int {
	n := 0
	for _, sym := range syms {
		if _, seen := visited[sym.ID]; !seen {
			n++
		}
	}
	return n
}

// buildEdgeKindGroups partitions the affected callers (skipping self-members)
// into the structural edge-kind views — subclasses (inherits) and includes — by
// the edge kind that first discovered each node. Composition is NOT derived here:
// the visitedKind bucketing under-reports it (a composer also reached by a
// higher-confidence calls edge is recorded as a caller, and low-confidence
// composers are evicted by the result cap), so it is computed separately from
// the edge table — see loadReverseComposition.
func (s *bfsState) buildEdgeKindGroups(directIDs, indirectIDs []int64, symbolsByID map[int64]model.Symbol, isSelf selfMethodFn) (subclasses, viaIncludes []model.Symbol) {
	for _, idSlice := range [2][]int64{directIDs, indirectIDs} {
		for _, id := range idSlice {
			sym, ok := symbolsByID[id]
			if !ok || isSelf(sym) {
				continue
			}
			switch s.visitedKind[id] {
			case "inherits":
				subclasses = append(subclasses, sym)
			case "includes":
				viaIncludes = append(viaIncludes, sym)
			}
		}
	}
	return subclasses, viaIncludes
}

// edgePair is the (source, target) shape one BFS hop returns. source
// is the caller we're learning about; target is the node we already
// visited and are expanding from.
type edgePair struct {
	source     int64
	target     int64
	kind       string
	confidence float64
	fileID     *int64
	line       *int
}

// hopCand is a qualifying in-edge to one node within a single BFS hop,
// carrying the cumulative path confidence (not the raw edge confidence) so
// the per-node expand-vs-sink decision can pick the strongest route.
type hopCand struct {
	target  int64
	kind    string
	cumConf float64
	fileID  *int64
	line    *int
}

func subjectHasCallees(ctx context.Context, db *sql.DB, symbolIDs []int64) bool {
	placeholders := strings.Repeat("?,", len(symbolIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT 1 FROM sense_edges WHERE source_id IN (` + placeholders + `) AND kind = 'calls' LIMIT 1`
	args := make([]any, len(symbolIDs))
	for i, id := range symbolIDs {
		args[i] = id
	}
	var one int
	return db.QueryRowContext(ctx, q, args...).Scan(&one) == nil
}

// subjectViewReached reports whether a view template (ERB) reaches any of the
// subject symbols via a calls/references edge. These edges carry a NULL
// source_id, so they are invisible to the symbol→symbol BFS; this direct
// edge-table check is the only way the subject's view reachability surfaces.
func subjectViewReached(ctx context.Context, db *sql.DB, symbolIDs []int64) bool {
	placeholders := strings.Repeat("?,", len(symbolIDs))
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT 1 FROM sense_edges e
		JOIN sense_files f ON e.file_id = f.id
		WHERE e.target_id IN (` + placeholders + `)
		AND e.kind IN ('calls', 'references')
		AND f.path LIKE '%.erb' LIMIT 1`
	args := make([]any, len(symbolIDs))
	for i, id := range symbolIDs {
		args[i] = id
	}
	var one int
	return db.QueryRowContext(ctx, q, args...).Scan(&one) == nil
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

		q := `SELECT source_id, target_id, kind, confidence, file_id, line FROM sense_edges
		      WHERE target_id IN (` + placeholders + `)
		        AND source_id IS NOT NULL
		        AND kind IN ('calls', 'composes', 'includes', 'inherits', 'temporal', 'tests', 'references')
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
			if err := rows.Scan(&p.source, &p.target, &p.kind, &p.confidence, &p.fileID, &p.line); err != nil {
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
