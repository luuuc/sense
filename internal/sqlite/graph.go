package sqlite

import (
	"context"
	"fmt"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// ReadSymbolGraph performs a multi-hop BFS from the given symbol.
// Depth 1 behaves identically to ReadSymbol. At depth 2+, the BFS
// expands the frontier in the requested direction, deduplicating
// against already-visited nodes. MaxPerHop caps new symbols per hop;
// zero means unlimited.
func (a *Adapter) ReadSymbolGraph(ctx context.Context, id int64, depth int, direction model.Direction, maxPerHop int) (*model.GraphResult, error) {
	root, err := a.ReadSymbol(ctx, id)
	if err != nil {
		return nil, err
	}
	result := &model.GraphResult{Root: *root}
	if err := a.foldRootMembers(ctx, &result.Root, direction); err != nil {
		return nil, err
	}
	if depth <= 1 {
		return result, nil
	}

	visited := map[int64]struct{}{id: {}}
	frontier := graphFrontier(&result.Root, direction, visited)

	for hop := 2; hop <= depth; hop++ {
		if len(frontier) == 0 {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("graph depth: cancelled at hop %d: %w", hop, err)
		}
		next, err := a.expandGraphHop(ctx, hop, frontier, direction, maxPerHop, visited, result)
		if err != nil {
			return nil, err
		}
		if result.Truncated {
			break
		}
		frontier = next
	}
	return result, nil
}

// foldRootMembers enriches the root symbol's edges with its members' callers
// and callees, in whichever directions the query asks for. For a container
// symbol a "who calls this" question is best answered by who calls the
// container's members, not only who names the type; the symmetric callee fold
// keeps "what does this class call" from reading as "depends on nothing".
func (a *Adapter) foldRootMembers(ctx context.Context, root *model.SymbolContext, direction model.Direction) error {
	if direction != model.DirectionCallees {
		if err := a.foldMemberCallers(ctx, root); err != nil {
			return err
		}
	}
	if direction != model.DirectionCallers {
		if err := a.foldMemberCallees(ctx, root); err != nil {
			return err
		}
	}
	return nil
}

// graphHop carries the mutable accumulator for expanding one BFS hop: the
// edges admitted this hop, the next frontier, how many new symbols have been
// taken against maxPerHop, and whether that cap truncated the hop. Grouping
// the hop's state into one value keeps the per-direction expand step a method
// rather than a closure that captures four locals.
type graphHop struct {
	layer        model.HopEdges
	nextFrontier []int64
	hopSymbols   int
	maxPerHop    int
	truncated    bool
}

// capped reports whether this hop has reached its per-hop symbol cap. A
// non-positive cap means unlimited.
func (h *graphHop) capped() bool {
	return h.maxPerHop > 0 && h.hopSymbols >= h.maxPerHop
}

// hasEdges reports whether the hop admitted any edge in either direction.
func (h *graphHop) hasEdges() bool {
	return len(h.layer.Inbound) > 0 || len(h.layer.Outbound) > 0
}

// expand loads adjacency for symID in one direction and admits expandable,
// unvisited edges into dest, recording each new target in the next frontier.
// It sets truncated and stops once maxPerHop new symbols have been taken.
func (h *graphHop) expand(ctx context.Context, a *Adapter, hop int, symID int64, outbound bool, dest *[]model.EdgeRef, visited map[int64]struct{}) error {
	refs, err := a.loadEdges(ctx, symID, outbound)
	if err != nil {
		return fmt.Errorf("graph depth hop %d: %w", hop, err)
	}
	for _, e := range refs {
		if !expandableEdge(e, visited) {
			continue
		}
		visited[e.Target.ID] = struct{}{}
		*dest = append(*dest, e)
		h.nextFrontier = append(h.nextFrontier, e.Target.ID)
		h.hopSymbols++
		if h.capped() {
			h.truncated = true
			return nil
		}
	}
	return nil
}

// expandGraphHop expands one BFS hop: for every symbol in the frontier it loads
// adjacency in the requested directions, admits expandable edges into a new
// layer, and returns the next frontier. maxPerHop caps new symbols admitted
// this hop; reaching the cap sets result.Truncated and stops the hop early.
func (a *Adapter) expandGraphHop(ctx context.Context, hop int, frontier []int64, direction model.Direction, maxPerHop int, visited map[int64]struct{}, result *model.GraphResult) ([]int64, error) {
	h := &graphHop{maxPerHop: maxPerHop}
	for _, symID := range frontier {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("graph depth: cancelled at hop %d: %w", hop, err)
		}
		if h.capped() {
			h.truncated = true
			break
		}
		if direction != model.DirectionCallees {
			if err := h.expand(ctx, a, hop, symID, false, &h.layer.Inbound, visited); err != nil {
				return nil, err
			}
			if h.truncated {
				break
			}
		}
		if direction != model.DirectionCallers {
			if err := h.expand(ctx, a, hop, symID, true, &h.layer.Outbound, visited); err != nil {
				return nil, err
			}
			if h.truncated {
				break
			}
		}
	}
	if h.truncated {
		result.Truncated = true
	}
	if h.hasEdges() {
		result.Layers = append(result.Layers, h.layer)
	}
	return h.nextFrontier, nil
}

// expandableEdge returns true if the edge should be followed during
// BFS — filters out zero-ID targets, temporal (co-change) edges, and
// already-visited nodes.
func expandableEdge(e model.EdgeRef, visited map[int64]struct{}) bool {
	if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
		return false
	}
	_, seen := visited[e.Target.ID]
	return !seen
}

// graphFrontier extracts the initial BFS frontier from the root's
// edges, filtered by direction. Discovered IDs are added to visited.
func graphFrontier(sc *model.SymbolContext, direction model.Direction, visited map[int64]struct{}) []int64 {
	var frontier []int64
	if direction != model.DirectionCallees {
		for _, e := range sc.Inbound {
			if expandableEdge(e, visited) {
				visited[e.Target.ID] = struct{}{}
				frontier = append(frontier, e.Target.ID)
			}
		}
	}
	if direction != model.DirectionCallers {
		for _, e := range sc.Outbound {
			if expandableEdge(e, visited) {
				visited[e.Target.ID] = struct{}{}
				frontier = append(frontier, e.Target.ID)
			}
		}
	}
	return frontier
}

// isContainerExpandKind reports the symbol kinds whose member callers are
// folded into the symbol's own inbound edges by foldMemberCallers. Matches
// blast's child-expansion set: concrete types whose methods carry the real
// callers (modules/interfaces are excluded — their children are definitions,
// not usage).
func isContainerExpandKind(k model.SymbolKind) bool {
	return k == model.KindClass || k == model.KindType
}

// isUsageEdge reports whether an edge kind represents a symbol being used
// (called or referenced) rather than a structural relationship
// (inherits/composes/includes) or co-change noise (temporal).
func isUsageEdge(k model.EdgeKind) bool {
	return k == model.EdgeCalls || k == model.EdgeReferences
}

// hasUsageEdge reports whether edges contains at least one above-floor
// usage edge — a real call/reference signal, as opposed to a structural
// edge or a low-confidence resolution guess. Used by both fold paths:
// against Inbound it means "has a real caller", against Outbound it means
// "has a real callee".
func hasUsageEdge(edges []model.EdgeRef) bool {
	for _, e := range edges {
		if isUsageEdge(e.Edge.Kind) && e.Edge.Confidence >= extract.ConfidenceUnresolved {
			return true
		}
	}
	return false
}

// foldMemberCallers enriches a container symbol's inbound edges with the
// callers of its own members. Without it, "who calls Order" returns only the
// edges that name the type (references/inherits), missing every caller that
// goes through Order's methods. The container and its own members are
// excluded as callers so internal method-to-method calls don't masquerade as
// external usage; results are deduped against the existing inbound set.
func (a *Adapter) foldMemberCallers(ctx context.Context, sc *model.SymbolContext) error {
	if !isContainerExpandKind(sc.Symbol.Kind) {
		return nil
	}
	// Only enrich when the container has no real direct caller of its own —
	// the "a referenced class looks unused" case (graph returned [] while
	// blast found callers via the class's methods). A structural edge
	// (inherits/composes) or a low-confidence name-collision guess is not a
	// real caller, so those do not suppress enrichment.
	if hasUsageEdge(sc.Inbound) {
		return nil
	}
	childIDs, err := a.childSymbolIDs(ctx, sc.Symbol.ID)
	if err != nil {
		return err
	}
	if len(childIDs) == 0 {
		return nil
	}
	exclude := make(map[int64]struct{}, len(childIDs)+1)
	exclude[sc.Symbol.ID] = struct{}{}
	for _, cid := range childIDs {
		exclude[cid] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(sc.Inbound))
	for _, e := range sc.Inbound {
		seen[e.Target.ID] = struct{}{}
	}
	for _, cid := range childIDs {
		refs, err := a.loadEdges(ctx, cid, false)
		if err != nil {
			return err
		}
		for _, e := range refs {
			if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
				continue
			}
			// Don't fold low-confidence guesses (e.g. name-collision
			// fallbacks stamped below the traversal floor) into the class's
			// callers — that would re-admit exactly what blast filters out.
			if e.Edge.Confidence < extract.ConfidenceUnresolved {
				continue
			}
			if _, skip := exclude[e.Target.ID]; skip {
				continue
			}
			if _, dup := seen[e.Target.ID]; dup {
				continue
			}
			seen[e.Target.ID] = struct{}{}
			sc.Inbound = append(sc.Inbound, e)
		}
	}
	return nil
}

// foldMemberCallees enriches a container symbol's outbound edges with the
// callees of its own members. Without it, "what does PriceValue call" returns
// only the edges the type itself names (usually none), so calls renders as
// `[]` — read as "depends on nothing" when the class in fact reaches Money.new,
// money.format, and friends through its methods. Calls back into the container
// or its own siblings are excluded so internal method-to-method traffic doesn't
// masquerade as an external dependency; results are deduped by target against
// the existing outbound set and each other.
func (a *Adapter) foldMemberCallees(ctx context.Context, sc *model.SymbolContext) error {
	if !isContainerExpandKind(sc.Symbol.Kind) {
		return nil
	}
	// Only enrich when the container has no real callee of its own — the
	// "a class's dependencies look empty" case. A structural edge
	// (inherits/composes) or a low-confidence guess is not a real callee, so
	// those do not suppress enrichment.
	if hasUsageEdge(sc.Outbound) {
		return nil
	}
	childIDs, err := a.childSymbolIDs(ctx, sc.Symbol.ID)
	if err != nil {
		return err
	}
	if len(childIDs) == 0 {
		return nil
	}
	exclude := make(map[int64]struct{}, len(childIDs)+1)
	exclude[sc.Symbol.ID] = struct{}{}
	for _, cid := range childIDs {
		exclude[cid] = struct{}{}
	}
	seen := make(map[int64]struct{}, len(sc.Outbound))
	for _, e := range sc.Outbound {
		seen[e.Target.ID] = struct{}{}
	}
	for _, cid := range childIDs {
		refs, err := a.loadEdges(ctx, cid, true)
		if err != nil {
			return err
		}
		for _, e := range refs {
			if e.Target.ID == 0 || e.Edge.Kind == model.EdgeTemporal {
				continue
			}
			// Don't fold low-confidence guesses (e.g. the 0.3 ERB/i18n
			// edges) into the class's callees — that would re-admit exactly
			// what blast and the graph confidence floor filter out.
			if e.Edge.Confidence < extract.ConfidenceUnresolved {
				continue
			}
			if _, skip := exclude[e.Target.ID]; skip {
				continue
			}
			if _, dup := seen[e.Target.ID]; dup {
				continue
			}
			// First member edge to a given target wins; the displayed
			// confidence is first-seen (by edge id), not the max across members.
			seen[e.Target.ID] = struct{}{}
			sc.Outbound = append(sc.Outbound, e)
		}
	}
	return nil
}

// childSymbolIDs returns the ids of symbols whose parent is parentID.
func (a *Adapter) childSymbolIDs(ctx context.Context, parentID int64) ([]int64, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT id FROM sense_symbols WHERE parent_id = ?`, parentID)
	if err != nil {
		return nil, fmt.Errorf("sqlite childSymbolIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite childSymbolIDs scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
