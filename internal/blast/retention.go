// Retention closure (pitch 31-12, ledger G-11): surfaces the interface-
// laundered may-retain holders — structs that hold the subject only behind an
// interface-typed field whose concrete satisfier is a carrier of the subject.
//
// The BFS cannot reach them: it walks reverse edges only, and the laundering
// path needs one FORWARD satisfaction hop (carrier →inherits→ interface)
// before the reverse composition hop (interface ←composes/includes← holder).
// This pass walks exactly that: a reverse composes/includes fixpoint from the
// subject (the typed-field carrier closure), then ONE laundering round over
// it. One round is the auditable claim — the may-retain qualifier does not
// compose across interface indirections, and the measured mutual fixpoint on
// dolt grew 47→357 holders with junk-shaped extras (pitch design table). An
// agent that needs the deeper tail blasts a returned holder.

package blast

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/luuuc/sense/internal/model"
)

// RetainedHolder is one interface-laundered may-retain row: a struct that
// holds the subject only behind an interface-typed field whose concrete
// satisfier is a carrier of the subject. Via is the interface the holder's
// field is typed as — when a holder reaches the subject through more than
// one interface, the lowest-ID interface is recorded so output is stable
// run to run.
//
// The claim is MAY-retain: an interface field can legally receive a
// non-carrier satisfier elsewhere, so these rows are surfaced as a weaker,
// separately-counted group and never feed TotalAffected.
type RetainedHolder struct {
	Symbol model.Symbol
	Via    model.Symbol
	// Carrier is one concrete satisfier of Via that carries the subject:
	// the laundering round's own proof, surfaced so the consumer does not
	// re-derive it with a per-interface graph join. When several carriers
	// satisfy Via, the lowest-ID one is recorded (mirrors the Via rule).
	Carrier model.Symbol
	// ViaSatisfiers is how many concrete types satisfy Via index-wide: the
	// bare promiscuity fact, no threshold and no verdict. A row on an
	// interface hundreds of unrelated types satisfy is far likelier to be a
	// coincidence of shape than one on a two-implementation contract, and the
	// consumer is the one who can judge which. It is never a reason to DROP a
	// row: real retention rides generic interfaces too (measured on pebble,
	// whose paid-win ring runs through InternalIterator).
	ViaSatisfiers int
	// Chain is a declared containment path from Carrier down to the
	// subject (inclusive on both ends): every hop is a composes/includes
	// edge the index holds, so the whole path is a statable structural
	// fact. The tree records one deterministic path among possibly
	// several. Only the runtime choice of satisfier stays a may-claim.
	Chain []model.Symbol
}

// retainedRoute is the laundering evidence for one holder: the via-interface
// its field is typed as, and the concrete carrier proving the interface can
// hold the subject.
type retainedRoute struct {
	via     int64
	carrier int64
}

// RetentionPurity records the satisfiers the ring refused to launder through:
// carriers declared in test files. A test-only satisfier proves nothing about
// production retention, and one such struct embedding both the subject and a
// dozen interfaces fabricates every composer of all twelve as a may-retain
// holder (measured on temporal: 52 of 100 rows). Excluded is how many distinct
// satisfiers were refused; Names carries up to retentionExcludedNamesCap of
// them so the exclusion is auditable rather than a silent filter.
type RetentionPurity struct {
	Excluded int
	Names    []string
}

// retentionExcludedNamesCap bounds the excluded-satisfier names disclosed in
// the note. Five names is enough to recognise the shape (one test harness
// struct, a couple of fakes) without spending the ring's token budget on an
// exclusion list the consumer never acts on; Excluded carries the true count.
const retentionExcludedNamesCap = 5

const (
	// retentionMaxLevels bounds the carrier fixpoint depth. Measured worst
	// case across the four biggest Go bench indexes: 7 levels (pebble
	// base.DiskFileNum); dolt DoltDB converges in 5. Termination comes from
	// the visited set — this cap is a pathological-index backstop.
	retentionMaxLevels = 20
	// retentionMaxCarriers bounds the carrier set. Measured worst closure
	// over the top-500 hubs of teleport/dolt/gitea/pebble: 245 structs
	// (dolt hash.Hash); ~8x headroom.
	retentionMaxCarriers = 2000
	// retentionCommonNameThreshold drives the F-31-09b INTERIM junk screen:
	// a via-interface with exactly one distinct direct member whose name
	// occurs as a method name more than this many times index-wide is junk —
	// its satisfaction edges are fabricated by name+arity matching on a
	// common member (Next/Close/Get/String), not by a real contract.
	// Measured margins: every genuine single-member via-interface across six
	// Go bench indexes sits at ≤25 (dolt VisitGCRoots 14, HasMany 15; pebble
	// Stat 25); every fabricating interface at ≥49 (Get 49, Next 135,
	// Close 248, CheckAndSetDefaults 551). Junk iff STRICTLY above. On a
	// small index common names fall under the threshold and junk can show —
	// the safe side for an advisory may-retain group. Delete this screen
	// when satisfaction matching goes param-type-aware (F-31-09b's real fix).
	retentionCommonNameThreshold = 25
)

// retentionSubjectKind reports whether a subject can be retained through a
// typed field at all. Functions and methods cannot — gating here means a
// function blast (most blasts) pays zero retention queries.
func retentionSubjectKind(kind model.SymbolKind) bool {
	switch kind {
	case model.KindClass, model.KindType, model.KindInterface:
		return true
	default:
		return false
	}
}

// retentionOutcome is one retention computation's full answer: the shown
// holders plus everything the consumer needs to judge them: the full count
// behind the shown slice, whether a cap truncated the computation, and the
// purity exclusions the ring applied.
type retentionOutcome struct {
	holders     []RetainedHolder
	count       int
	offset      int
	truncated   bool
	purity      RetentionPurity
	fingerprint string
}

// loadRetention computes the retention group for the subject: the carrier
// fixpoint (internal), one laundering round over it, then exclusion,
// hydration, ordering, and the result cap. The returned count is the full
// post-exclusion size, never reduced by the cap.
func loadRetention(ctx context.Context, db *sql.DB, subject model.Symbol, seedIDs, directComposerIDs []int64, childSet, excludeGrouped map[int64]struct{}, isSelf selfMethodFn, page retentionPage) (retentionOutcome, error) {
	if !retentionSubjectKind(subject.Kind) {
		return retentionOutcome{}, nil
	}
	carriers, parents, truncated, err := carrierClosure(ctx, db, seedIDs, directComposerIDs)
	if err != nil {
		return retentionOutcome{}, err
	}
	holderVia, purity, err := launderOneRound(ctx, db, carriers)
	if err != nil {
		return retentionOutcome{}, err
	}
	kept := excludeRetained(holderVia, carriers, childSet, excludeGrouped)
	holders, fullCount, capped, err := hydrateRetained(ctx, db, kept, holderVia, newChainTree(parents, seedIDs), isSelf, page)
	if err != nil {
		return retentionOutcome{}, err
	}
	out := retentionOutcome{
		holders:   holders,
		count:     fullCount,
		offset:    page.offset,
		truncated: truncated || capped,
		purity:    purity,
	}
	if fullCount > 0 {
		// Only a ring that exists can be paged, and only a paged ring needs
		// the generation stamp: a subject with no holders never pays for it.
		out.fingerprint = indexFingerprint(ctx, db)
	}
	return out, nil
}

// retentionPage is the window the caller wants of the ring: how many rows to
// skip, and how many to return. The ring's order is total (see orderRetained),
// so consecutive windows over one index generation cover the set exactly.
type retentionPage struct {
	offset int
	limit  int
}

// indexFingerprint identifies the index generation a page was cut from: the
// file count plus the newest indexed_at stamp, which the watch daemon moves on
// every rescan that touches anything. Two pages carrying the same fingerprint
// were cut from the same graph; a mismatch means the ring shifted underneath
// the cursor and the pages cannot be unioned: the exact silent-incompleteness
// class the ring must never produce, so it is disclosed, never papered over.
//
// Best-effort like the other advisory lookups here: an unreadable stamp yields
// an empty fingerprint, which claims nothing rather than claiming sameness.
func indexFingerprint(ctx context.Context, db *sql.DB) string {
	var files int
	var newest sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(indexed_at) FROM sense_files`).Scan(&files, &newest)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("f%d@%s", files, newest.String)
}

// carrierClosure walks the reverse composes/includes fixpoint from the seeds:
// every non-interface type that reaches the subject through a chain of typed
// fields or embeds. Level 1 reuses the direct-composer IDs Compute already
// fetched for the composition group, plus the seeds' embedders; deeper levels
// query both edge kinds at once. The visited set terminates cycles; the
// level and size caps are backstops that flag truncation.
func carrierClosure(ctx context.Context, db *sql.DB, seedIDs, directComposerIDs []int64) (map[int64]struct{}, map[int64]int64, bool, error) {
	carriers := make(map[int64]struct{}, len(seedIDs))
	for _, id := range seedIDs {
		carriers[id] = struct{}{}
	}
	// parents records which already-admitted node each carrier declaredly
	// contains (the pair target that admitted it): the containment tree the
	// chain field renders. Level-1 composers hang off the primary seed;
	// with a multi-seed subject the edge may land on a sibling seed, but
	// siblings share one qualified name, so the rendered hop is unchanged.
	parents := make(map[int64]int64)
	embedders, err := inboundHolderPairs(ctx, db, seedIDs, true)
	if err != nil {
		return nil, nil, false, err
	}
	seedParent := int64(0)
	if len(seedIDs) > 0 {
		seedParent = seedIDs[0]
	}
	for _, id := range directComposerIDs {
		recordParent(parents, carriers, id, seedParent)
	}
	for _, p := range embedders {
		recordParent(parents, carriers, p.source, p.target)
	}
	level, err := nonInterfaceIDs(ctx, db, mergeSources(directComposerIDs, embedders))
	if err != nil {
		return nil, nil, false, err
	}

	admitted := 0
	truncated := false
	for depth := 0; len(level) > 0 && depth < retentionMaxLevels; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, false, fmt.Errorf("blast: retention closure cancelled: %w", err)
		}
		var next []int64
		next, admitted, truncated = admitCarrierLevel(carriers, level, admitted)
		if truncated || len(next) == 0 {
			break
		}
		pairs, err := inboundHolderPairs(ctx, db, next, false)
		if err != nil {
			return nil, nil, false, err
		}
		for _, p := range pairs {
			recordParent(parents, carriers, p.source, p.target)
		}
		level = mergeSources(nil, pairs)
	}
	return carriers, parents, truncated || liveFrontierRemains(carriers, level), nil
}

// recordParent stores the containment parent for a node not yet admitted to
// the carrier set. The choice is earliest-level first (the admitted guard
// refuses re-parenting, so chains are shortest-path), then lowest-ID within
// a level, making chains stable run to run with no ordering assumption on
// the pair queries.
func recordParent(parents map[int64]int64, carriers map[int64]struct{}, node, parent int64) {
	if _, seen := carriers[node]; seen {
		return
	}
	if cur, ok := parents[node]; !ok || parent < cur {
		parents[node] = parent
	}
}

// chainTree carries what hydration needs to render a containment chain:
// the parent tree and the seed set the chain terminates on.
type chainTree struct {
	parents map[int64]int64
	seeds   map[int64]struct{}
}

// newChainTree builds the walkable tree once so per-row walks pay no setup.
func newChainTree(parents map[int64]int64, seedIDs []int64) chainTree {
	seeds := make(map[int64]struct{}, len(seedIDs))
	for _, s := range seedIDs {
		seeds[s] = struct{}{}
	}
	return chainTree{parents: parents, seeds: seeds}
}

// chainIDs walks the parent tree from start down to the first seed hit,
// returning the full path including start and the terminal seed. The walk is
// bounded by the closure's own depth cap; a break in the tree returns the
// partial path rather than guessing.
func (c chainTree) chainIDs(start int64) []int64 {
	path := []int64{start}
	cur := start
	for range retentionMaxLevels + 2 {
		if _, done := c.seeds[cur]; done {
			return path
		}
		next, ok := c.parents[cur]
		if !ok {
			return path
		}
		path = append(path, next)
		cur = next
	}
	return path
}

// admitCarrierLevel admits one fixpoint level into the carrier set in
// ID-ascending order, so a size-cap truncation is order-defined rather than
// map-defined. Returns the newly admitted IDs, the running admission count,
// and whether the cap tripped.
func admitCarrierLevel(carriers map[int64]struct{}, level []int64, admitted int) ([]int64, int, bool) {
	var next []int64
	for _, id := range level {
		if _, seen := carriers[id]; seen {
			continue
		}
		if admitted >= retentionMaxCarriers {
			return next, admitted, true
		}
		carriers[id] = struct{}{}
		admitted++
		next = append(next, id)
	}
	return next, admitted, false
}

// liveFrontierRemains reports whether the level the depth cap cut still held
// unvisited carriers — the closure is then incomplete and must say so.
func liveFrontierRemains(carriers map[int64]struct{}, level []int64) bool {
	for _, id := range level {
		if _, seen := carriers[id]; !seen {
			return true
		}
	}
	return false
}

// launderOneRound performs the single laundering round: the interfaces any
// carrier satisfies (forward inherits, interface-kind targets only — the
// kind gate is what keeps languages without interface symbols out), then
// those interfaces' reverse composers/embedders. Returns holder → lowest-ID
// via-interface so a holder reachable through several interfaces appears
// once, deterministically.
// The satisfaction pairs are purified first: a satisfier declared in a test
// file is never nominated as a carrier, and an interface left with no
// production satisfier stops laundering entirely: its composers are
// fabrications of the test harness, not of the production graph.
func launderOneRound(ctx context.Context, db *sql.DB, carriers map[int64]struct{}) (map[int64]retainedRoute, RetentionPurity, error) {
	base := sortedIDSet(carriers)
	satisfies, err := forwardInterfacePairs(ctx, db, base)
	if err != nil {
		return nil, RetentionPurity{}, err
	}
	satisfies, purity, err := dropTestSatisfiers(ctx, db, satisfies)
	if err != nil {
		return nil, RetentionPurity{}, err
	}
	ifaces, ifaceCarrier := nominateCarriers(satisfies)
	if len(ifaces) == 0 {
		return nil, purity, nil
	}
	// Screen BEFORE expanding composers — load-bearing ordering: a junk
	// interface like Closer can carry hundreds of composers that must never
	// be fetched, so the screen is the perf guard as well as the truth guard.
	ifaces, err = screenJunkInterfaces(ctx, db, ifaces)
	if err != nil {
		return nil, purity, err
	}
	if len(ifaces) == 0 {
		return nil, purity, nil
	}
	pairs, err := inboundHolderPairs(ctx, db, ifaces, false)
	if err != nil {
		return nil, purity, err
	}
	holderVia := make(map[int64]retainedRoute, len(pairs))
	for _, p := range pairs {
		if route, ok := holderVia[p.source]; !ok || p.target < route.via {
			holderVia[p.source] = retainedRoute{via: p.target, carrier: ifaceCarrier[p.target]}
		}
	}
	return holderVia, purity, nil
}

// dropTestSatisfiers removes satisfaction pairs whose satisfier is declared in
// a test file, and reports which ones were refused. A test double satisfies
// interfaces it will never satisfy in production, so laundering through it
// fabricates holders; the refusal is disclosed rather than silent because the
// consumer cannot otherwise tell a purified ring from a small one.
//
// The flag lookup is best-effort like everywhere else in this package: a
// failed query yields a nil map, which refuses nothing and degrades to the
// unpurified ring instead of failing the blast.
func dropTestSatisfiers(ctx context.Context, db *sql.DB, pairs []holderPair) ([]holderPair, RetentionPurity, error) {
	if len(pairs) == 0 {
		return pairs, RetentionPurity{}, nil
	}
	sources := make(map[int64]struct{}, len(pairs))
	for _, p := range pairs {
		sources[p.source] = struct{}{}
	}
	testFlags := testFileFlags(ctx, db, sortedIDSet(sources))
	kept := make([]holderPair, 0, len(pairs))
	excluded := make(map[int64]struct{})
	for _, p := range pairs {
		if testFlags[p.source] {
			excluded[p.source] = struct{}{}
			continue
		}
		kept = append(kept, p)
	}
	if len(excluded) == 0 {
		return kept, RetentionPurity{}, nil
	}
	names, err := excludedSatisfierNames(ctx, db, sortedIDSet(excluded))
	if err != nil {
		return nil, RetentionPurity{}, err
	}
	return kept, RetentionPurity{Excluded: len(excluded), Names: names}, nil
}

// excludedSatisfierNames hydrates the first retentionExcludedNamesCap refused
// satisfiers (ID-ascending, so the sample is stable run to run) into display
// names. A satisfier that failed to hydrate is skipped rather than rendered as
// a blank name: the count already carries the magnitude.
func excludedSatisfierNames(ctx context.Context, db *sql.DB, ids []int64) ([]string, error) {
	sample := ids
	if len(sample) > retentionExcludedNamesCap {
		sample = sample[:retentionExcludedNamesCap]
	}
	syms, err := loadSymbols(ctx, db, sample)
	if err != nil {
		return nil, fmt.Errorf("blast: excluded satisfiers: %w", err)
	}
	names := make([]string, 0, len(sample))
	for _, id := range sample {
		if s, ok := syms[id]; ok {
			names = append(names, s.Name)
		}
	}
	return names, nil
}

// nominateCarriers reduces purified satisfaction pairs to the candidate
// via-interfaces plus each interface's nominated carrier: the lowest-ID
// satisfier, the laundering proof that rides into RetainedHolder.Carrier.
func nominateCarriers(pairs []holderPair) ([]int64, map[int64]int64) {
	set := make(map[int64]struct{}, len(pairs))
	carrier := make(map[int64]int64, len(pairs))
	for _, p := range pairs {
		set[p.target] = struct{}{}
		if lowest, ok := carrier[p.target]; !ok || p.source < lowest {
			carrier[p.target] = p.source
		}
	}
	return sortedIDSet(set), carrier
}

// excludeRetained drops holders that already have a stronger home: carriers
// (which includes the seeds — a carrier belongs to the composition group),
// the subject's own members, and symbols already placed in another edge-kind
// group (the one-node-one-group invariant).
func excludeRetained(holderVia map[int64]retainedRoute, carriers, childSet, excludeGrouped map[int64]struct{}) []int64 {
	kept := make([]int64, 0, len(holderVia))
	for id := range holderVia {
		if _, isCarrier := carriers[id]; isCarrier {
			continue
		}
		if _, isChild := childSet[id]; isChild {
			continue
		}
		if _, grouped := excludeGrouped[id]; grouped {
			continue
		}
		kept = append(kept, id)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i] < kept[j] })
	return kept
}

// hydrateRetained loads the holder and via-interface symbols, filters the
// subject's self-members, orders production-first then by ID, and cuts the
// requested page. The returned count is the full post-exclusion size, never
// the page's, so a partial page is self-evident to the consumer.
func hydrateRetained(ctx context.Context, db *sql.DB, kept []int64, holderVia map[int64]retainedRoute, chains chainTree, isSelf selfMethodFn, page retentionPage) ([]RetainedHolder, int, bool, error) {
	if len(kept) == 0 {
		return nil, 0, false, nil
	}
	allIDs := append([]int64{}, kept...)
	chainByHolder := make(map[int64][]int64, len(kept))
	for _, id := range kept {
		allIDs = append(allIDs, holderVia[id].via, holderVia[id].carrier)
		path := chains.chainIDs(holderVia[id].carrier)
		chainByHolder[id] = path
		allIDs = append(allIDs, path...)
	}
	syms, err := loadSymbols(ctx, db, allIDs)
	if err != nil {
		return nil, 0, false, fmt.Errorf("blast: hydrate retained: %w", err)
	}
	holders := make([]RetainedHolder, 0, len(kept))
	for _, id := range kept {
		sym, ok := syms[id]
		if !ok || isSelf(sym) {
			continue
		}
		route := holderVia[id]
		holders = append(holders, RetainedHolder{Symbol: sym, Via: syms[route.via], Carrier: syms[route.carrier], Chain: assembleChain(syms, chainByHolder[id])})
	}
	fullCount := len(holders)
	orderRetained(ctx, db, holders)
	holders, capped := cutRetentionPage(holders, page)
	// Stamped after the cap so the count query is scoped to the vias actually
	// shown: on a hub subject the pre-cap ring can be several hundred rows.
	if err := stampViaSatisfiers(ctx, db, holders); err != nil {
		return nil, 0, false, err
	}
	return holders, fullCount, capped, nil
}

// cutRetentionPage takes the requested window out of the ordered ring and
// reports whether rows exist past its end. An offset beyond the ring yields no
// rows rather than an error: the count and the note still tell the consumer
// where the ring actually ends, which is more useful than a rejected call.
func cutRetentionPage(holders []RetainedHolder, page retentionPage) ([]RetainedHolder, bool) {
	if page.offset >= len(holders) {
		// Past the end. There is more ring behind this window whenever the
		// window skipped a non-empty ring: the caller over-shot the cursor.
		overshot := page.offset > 0 && len(holders) > 0
		return nil, overshot
	}
	holders = holders[page.offset:]
	if page.limit > 0 && len(holders) > page.limit {
		return holders[:page.limit], true
	}
	return holders, false
}

// stampViaSatisfiers annotates each shown row with how many concrete types
// satisfy its via-interface index-wide (see RetainedHolder.ViaSatisfiers).
// Interface-kind sources are not counted: an interface embedding an interface
// extends a contract, it does not satisfy one, so counting it would inflate
// the stamp with symbols that can never be the runtime value of the field.
func stampViaSatisfiers(ctx context.Context, db *sql.DB, holders []RetainedHolder) error {
	vias := make(map[int64]struct{}, len(holders))
	for _, h := range holders {
		if h.Via.ID != 0 {
			vias[h.Via.ID] = struct{}{}
		}
	}
	if len(vias) == 0 {
		return nil
	}
	query := func(placeholders string) string {
		return `SELECT e.target_id, COUNT(DISTINCT e.source_id) FROM sense_edges e
		        JOIN sense_symbols s ON s.id = e.source_id
		        WHERE e.target_id IN (` + placeholders + `)
		          AND e.kind = 'inherits'
		          AND s.kind != 'interface'
		        GROUP BY e.target_id`
	}
	pairs, err := queryHolderPairs(ctx, db, sortedIDSet(vias), query)
	if err != nil {
		return fmt.Errorf("blast: via satisfier counts: %w", err)
	}
	counts := make(map[int64]int64, len(pairs))
	for _, p := range pairs {
		counts[p.source] = p.target
	}
	for i := range holders {
		holders[i].ViaSatisfiers = int(counts[holders[i].Via.ID])
	}
	return nil
}

// assembleChain hydrates a chain's IDs whole or not at all: splicing over a
// hop whose symbol failed to hydrate (index churn between the edge walk and
// hydration) would fabricate a containment edge the index does not hold, so
// a single missing hop drops the entire chain (mirrors the zero-value
// carrier policy).
func assembleChain(syms map[int64]model.Symbol, ids []int64) []model.Symbol {
	chain := make([]model.Symbol, 0, len(ids))
	for _, id := range ids {
		cs, ok := syms[id]
		if !ok {
			return nil
		}
		chain = append(chain, cs)
	}
	return chain
}

// orderRetained sorts holders production-first, then by symbol ID. A test
// fixture holding the subject rides the same may-retain claim as a production
// holder but matters less to a lifecycle audit, so it must not crowd the cap.
// testFileFlags is best-effort: a nil map degrades to pure ID order, still
// deterministic.
//
// This is a TOTAL order and paging depends on it : symbol IDs are
// unique, so no two rows can compare equal and no pair can swap between two
// calls on one index: a cursor over the ring therefore covers the set exactly,
// with no dup and no gap. Blast output order has a recorded nondeterminism
// history (project ledger), so the property is pinned by an explicit
// exact-sequence test, not assumed from the comparator's shape. Any new key
// must be inserted ABOVE the ID tiebreak and re-pinned: adding one below it is
// dead code, and adding one above reorders every shipped ring.
func orderRetained(ctx context.Context, db *sql.DB, holders []RetainedHolder) {
	ids := make([]int64, len(holders))
	for i, h := range holders {
		ids[i] = h.Symbol.ID
	}
	testFlags := testFileFlags(ctx, db, ids)
	sort.SliceStable(holders, func(i, j int) bool {
		ti, tj := testFlags[holders[i].Symbol.ID], testFlags[holders[j].Symbol.ID]
		if ti != tj {
			return !ti
		}
		return holders[i].Symbol.ID < holders[j].Symbol.ID
	})
}

// screenJunkInterfaces drops the F-31-09b junk stratum from the candidate
// via-interfaces: exactly one distinct direct member whose name is common
// index-wide (see retentionCommonNameThreshold). Multi-member and zero-member
// (embedded-only) interfaces always pass — their method-SET match is the
// signal. The frequency lookup is scoped to the candidate member names, never
// a whole-index scan, and only runs when a single-member candidate exists.
func screenJunkInterfaces(ctx context.Context, db *sql.DB, ifaces []int64) ([]int64, error) {
	soleMember, err := soleMemberNames(ctx, db, ifaces)
	if err != nil {
		return nil, err
	}
	if len(soleMember) == 0 {
		return ifaces, nil
	}
	names := make([]string, 0, len(soleMember))
	seen := map[string]struct{}{}
	for _, name := range soleMember {
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
			names = append(names, name)
		}
	}
	sort.Strings(names)
	freq, err := methodNameCounts(ctx, db, names)
	if err != nil {
		return nil, err
	}
	kept := make([]int64, 0, len(ifaces))
	for _, id := range ifaces {
		if name, single := soleMember[id]; single && freq[name] > retentionCommonNameThreshold {
			continue
		}
		kept = append(kept, id)
	}
	return kept, nil
}

// soleMemberNames returns, for each interface that declares exactly one
// distinct direct method name, that name.
func soleMemberNames(ctx context.Context, db *sql.DB, ifaces []int64) (map[int64]string, error) {
	members := map[int64]map[string]struct{}{}
	query := func(placeholders string) string {
		return `SELECT parent_id, name FROM sense_symbols
		        WHERE parent_id IN (` + placeholders + `) AND kind = 'method'`
	}
	err := queryIDStringRows(ctx, db, ifaces, query, func(id int64, name string) {
		if members[id] == nil {
			members[id] = map[string]struct{}{}
		}
		members[id][name] = struct{}{}
	})
	if err != nil {
		return nil, err
	}
	sole := make(map[int64]string, len(members))
	for id, names := range members {
		if len(names) == 1 {
			for name := range names {
				sole[id] = name
			}
		}
	}
	return sole, nil
}

// methodNameCounts returns the index-wide method-symbol count per name,
// scoped to the given names.
func methodNameCounts(ctx context.Context, db *sql.DB, names []string) (map[string]int, error) {
	out := make(map[string]int, len(names))
	const chunk = 500
	for start := 0; start < len(names); start += chunk {
		end := start + chunk
		if end > len(names) {
			end = len(names)
		}
		batch := names[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(batch))
		for i, n := range batch {
			args[i] = n
		}
		q := `SELECT name, COUNT(*) FROM sense_symbols
		      WHERE kind = 'method' AND name IN (` + placeholders + `) GROUP BY name`
		rows, err := db.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			var n int
			if err := rows.Scan(&name, &n); err != nil {
				_ = rows.Close()
				return nil, err
			}
			out[name] = n
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// queryIDStringRows runs an (int64, string) two-column query with one IN
// clause over ids, chunked, feeding each row to visit.
func queryIDStringRows(ctx context.Context, db *sql.DB, ids []int64, build func(placeholders string) string, visit func(int64, string)) error {
	const chunk = 500
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, build(placeholders), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var s string
			if err := rows.Scan(&id, &s); err != nil {
				_ = rows.Close()
				return err
			}
			visit(id, s)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	return nil
}

// holderPair is one (holder, held) row from the reverse composition/embedding
// queries: source holds target through a named field or an embed.
type holderPair struct {
	source int64
	target int64
}

// mergeSources unions a plain ID list with the source column of pairs,
// deduplicated and ID-ascending for deterministic traversal order.
func mergeSources(ids []int64, pairs []holderPair) []int64 {
	set := make(map[int64]struct{}, len(ids)+len(pairs))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	for _, p := range pairs {
		set[p.source] = struct{}{}
	}
	return sortedIDSet(set)
}

// sortedIDSet flattens a membership set into an ascending ID slice.
func sortedIDSet(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// inboundHolderPairs returns the (source, target) rows of reverse holding
// edges onto ids: includes edges always, composes edges too unless
// includesOnly (level 1 reuses the composition group's already-fetched
// composers, so only embedders are missing there). Interface-kind sources are
// excluded — an interface embedding an interface declares a contract, it
// holds nothing.
func inboundHolderPairs(ctx context.Context, db *sql.DB, ids []int64, includesOnly bool) ([]holderPair, error) {
	kinds := `'composes','includes'`
	if includesOnly {
		kinds = `'includes'`
	}
	query := func(placeholders string) string {
		return `SELECT DISTINCT e.source_id, e.target_id FROM sense_edges e
		        JOIN sense_symbols s ON s.id = e.source_id
		        WHERE e.target_id IN (` + placeholders + `)
		          AND e.kind IN (` + kinds + `)
		          AND e.source_id IS NOT NULL
		          AND s.kind != 'interface'`
	}
	return queryHolderPairs(ctx, db, ids, query)
}

// forwardInterfacePairs returns the (satisfier, interface) rows for every
// interface-kind symbol any of ids satisfies or implements (forward inherits
// edges). The filter is on the TARGET's symbol kind, never on edge confidence:
// Go satisfaction edges ride convention confidence but Rust/TS declared
// implements are full-confidence, and both launder. The pairs stay unreduced
// so purity can refuse individual satisfiers before any carrier is nominated.
func forwardInterfacePairs(ctx context.Context, db *sql.DB, ids []int64) ([]holderPair, error) {
	query := func(placeholders string) string {
		return `SELECT DISTINCT e.source_id, e.target_id FROM sense_edges e
		        JOIN sense_symbols s ON s.id = e.target_id
		        WHERE e.source_id IN (` + placeholders + `)
		          AND e.kind = 'inherits'
		          AND s.kind = 'interface'`
	}
	return queryHolderPairs(ctx, db, ids, query)
}

// nonInterfaceIDs filters ids down to symbols whose kind is not interface.
// Level 1 of the closure reuses the composition group's composer IDs, which
// carry no kind information; deeper levels filter in SQL.
func nonInterfaceIDs(ctx context.Context, db *sql.DB, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query := func(placeholders string) string {
		return `SELECT id, id FROM sense_symbols
		        WHERE id IN (` + placeholders + `) AND kind != 'interface'`
	}
	pairs, err := queryHolderPairs(ctx, db, ids, query)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, p.source)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// queryHolderPairs runs a two-int64-column query with one IN clause over ids,
// chunked under SQLITE_MAX_VARIABLE_NUMBER like every other blast query.
func queryHolderPairs(ctx context.Context, db *sql.DB, ids []int64, build func(placeholders string) string) ([]holderPair, error) {
	const chunk = 500
	var out []holderPair
	for start := 0; start < len(ids); start += chunk {
		end := start + chunk
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[start:end]
		placeholders := strings.Repeat("?,", len(batch))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, err := db.QueryContext(ctx, build(placeholders), args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var p holderPair
			if err := rows.Scan(&p.source, &p.target); err != nil {
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
