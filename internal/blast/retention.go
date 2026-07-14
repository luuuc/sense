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
}

// retainedRoute is the laundering evidence for one holder: the via-interface
// its field is typed as, and the concrete carrier proving the interface can
// hold the subject.
type retainedRoute struct {
	via     int64
	carrier int64
}

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

// loadRetention computes the retention group for the subject: the carrier
// fixpoint (internal), one laundering round over it, then exclusion,
// hydration, ordering, and the result cap. Returns the holders, the full
// post-exclusion count (never reduced by the cap), and whether any cap
// truncated the computation.
func loadRetention(ctx context.Context, db *sql.DB, subject model.Symbol, seedIDs, directComposerIDs []int64, childSet, excludeGrouped map[int64]struct{}, isSelf selfMethodFn, maxResults int) ([]RetainedHolder, int, bool, error) {
	if !retentionSubjectKind(subject.Kind) {
		return nil, 0, false, nil
	}
	carriers, truncated, err := carrierClosure(ctx, db, seedIDs, directComposerIDs)
	if err != nil {
		return nil, 0, false, err
	}
	holderVia, err := launderOneRound(ctx, db, carriers)
	if err != nil {
		return nil, 0, false, err
	}
	kept := excludeRetained(holderVia, carriers, childSet, excludeGrouped)
	holders, fullCount, capped, err := hydrateRetained(ctx, db, kept, holderVia, isSelf, maxResults)
	if err != nil {
		return nil, 0, false, err
	}
	return holders, fullCount, truncated || capped, nil
}

// carrierClosure walks the reverse composes/includes fixpoint from the seeds:
// every non-interface type that reaches the subject through a chain of typed
// fields or embeds. Level 1 reuses the direct-composer IDs Compute already
// fetched for the composition group, plus the seeds' embedders; deeper levels
// query both edge kinds at once. The visited set terminates cycles; the
// level and size caps are backstops that flag truncation.
func carrierClosure(ctx context.Context, db *sql.DB, seedIDs, directComposerIDs []int64) (map[int64]struct{}, bool, error) {
	carriers := make(map[int64]struct{}, len(seedIDs))
	for _, id := range seedIDs {
		carriers[id] = struct{}{}
	}
	embedders, err := inboundHolderPairs(ctx, db, seedIDs, true)
	if err != nil {
		return nil, false, err
	}
	level, err := nonInterfaceIDs(ctx, db, mergeSources(directComposerIDs, embedders))
	if err != nil {
		return nil, false, err
	}

	admitted := 0
	truncated := false
	for depth := 0; len(level) > 0 && depth < retentionMaxLevels; depth++ {
		if err := ctx.Err(); err != nil {
			return nil, false, fmt.Errorf("blast: retention closure cancelled: %w", err)
		}
		var next []int64
		next, admitted, truncated = admitCarrierLevel(carriers, level, admitted)
		if truncated || len(next) == 0 {
			break
		}
		pairs, err := inboundHolderPairs(ctx, db, next, false)
		if err != nil {
			return nil, false, err
		}
		level = mergeSources(nil, pairs)
	}
	return carriers, truncated || liveFrontierRemains(carriers, level), nil
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
func launderOneRound(ctx context.Context, db *sql.DB, carriers map[int64]struct{}) (map[int64]retainedRoute, error) {
	base := sortedIDSet(carriers)
	ifaces, ifaceCarrier, err := forwardInterfaceTargets(ctx, db, base)
	if err != nil {
		return nil, err
	}
	if len(ifaces) == 0 {
		return nil, nil
	}
	// Screen BEFORE expanding composers — load-bearing ordering: a junk
	// interface like Closer can carry hundreds of composers that must never
	// be fetched, so the screen is the perf guard as well as the truth guard.
	ifaces, err = screenJunkInterfaces(ctx, db, ifaces)
	if err != nil {
		return nil, err
	}
	if len(ifaces) == 0 {
		return nil, nil
	}
	pairs, err := inboundHolderPairs(ctx, db, ifaces, false)
	if err != nil {
		return nil, err
	}
	holderVia := make(map[int64]retainedRoute, len(pairs))
	for _, p := range pairs {
		if route, ok := holderVia[p.source]; !ok || p.target < route.via {
			holderVia[p.source] = retainedRoute{via: p.target, carrier: ifaceCarrier[p.target]}
		}
	}
	return holderVia, nil
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
// subject's self-members, orders production-first then by ID, and applies the
// result cap. The returned count is the full post-exclusion size, never the
// capped one, so a capped list is self-evident to the consumer.
func hydrateRetained(ctx context.Context, db *sql.DB, kept []int64, holderVia map[int64]retainedRoute, isSelf selfMethodFn, maxResults int) ([]RetainedHolder, int, bool, error) {
	if len(kept) == 0 {
		return nil, 0, false, nil
	}
	allIDs := append([]int64{}, kept...)
	for _, id := range kept {
		allIDs = append(allIDs, holderVia[id].via, holderVia[id].carrier)
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
		holders = append(holders, RetainedHolder{Symbol: sym, Via: syms[route.via], Carrier: syms[route.carrier]})
	}
	fullCount := len(holders)
	orderRetained(ctx, db, holders)
	capped := false
	if len(holders) > maxResults {
		holders = holders[:maxResults]
		capped = true
	}
	return holders, fullCount, capped, nil
}

// orderRetained sorts holders production-first, then by symbol ID. A test
// fixture holding the subject rides the same may-retain claim as a production
// holder but matters less to a lifecycle audit, so it must not crowd the cap.
// testFileFlags is best-effort: a nil map degrades to pure ID order, still
// deterministic.
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

// forwardInterfaceTargets returns the distinct interface-kind symbols any of
// ids satisfies or implements (forward inherits edges). The filter is on the
// TARGET's symbol kind, never on edge confidence: Go satisfaction edges ride
// convention confidence but Rust/TS declared implements are full-confidence,
// and both launder.
// forwardInterfaceTargets returns the interface-kind inherit targets of ids,
// plus each interface's lowest-ID satisfying carrier, the laundering proof
// that rides into RetainedHolder.Carrier.
func forwardInterfaceTargets(ctx context.Context, db *sql.DB, ids []int64) ([]int64, map[int64]int64, error) {
	query := func(placeholders string) string {
		return `SELECT DISTINCT e.source_id, e.target_id FROM sense_edges e
		        JOIN sense_symbols s ON s.id = e.target_id
		        WHERE e.source_id IN (` + placeholders + `)
		          AND e.kind = 'inherits'
		          AND s.kind = 'interface'`
	}
	pairs, err := queryHolderPairs(ctx, db, ids, query)
	if err != nil {
		return nil, nil, err
	}
	set := make(map[int64]struct{}, len(pairs))
	carrier := make(map[int64]int64, len(pairs))
	for _, p := range pairs {
		set[p.target] = struct{}{}
		if lowest, ok := carrier[p.target]; !ok || p.source < lowest {
			carrier[p.target] = p.source
		}
	}
	return sortedIDSet(set), carrier, nil
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
