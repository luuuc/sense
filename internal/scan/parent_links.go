package scan

import (
	"fmt"
	"path/filepath"

	"github.com/luuuc/sense/internal/sqlite"
)

// pendingParent is one symbol whose ParentQualified missed the per-file
// map in writeFileInner: its parent lives in another file (the cross-file
// receiver-method shape), or later in the same file. resolveParentLinks
// closes these after every file has been written — the same asymmetry
// resolveAndWriteEdges already closes for edges.
type pendingParent struct {
	SymbolID        int64
	ParentQualified string
	FileID          int64
	Dir             string // filepath.Dir of the symbol's file (relative path)
}

// pickParent selects the container a pending symbol binds to, from the
// candidates sharing its ParentQualified. The rule, for every language:
// same file, else same directory, else nothing. Cross-directory binds are
// refused — Go qualified names are package-name based, so an identical
// qualified name in another directory is a different package sharing a
// name, and Rust impl blocks qualify through the in-file module chain,
// so their qualified names carry no path component at all: a global
// fallback would happily bind a `Money` impl in one subtree to a same-
// named type in another. Lexically-scoped languages (Ruby, Python) can
// never emit a cross-file parent in the first place (the enclosing scope
// is in the symbol's own file by construction).
//
// Within a tier the winner is the smallest (path, line, id). Path and
// line are content-derived, so two indexes of the same tree pick the same
// parent regardless of write history or candidate order; each pending
// symbol resolves independently against the full candidate set.
func pickParent(p pendingParent, candidates []sqlite.ContainerRef) (int64, bool) {
	var best *sqlite.ContainerRef
	bestTier := 2
	for i := range candidates {
		c := &candidates[i]
		var tier int
		switch {
		case c.FileID == p.FileID:
			tier = 0
		case filepath.Dir(c.Path) == p.Dir:
			tier = 1
		default:
			continue
		}
		if best == nil || tier < bestTier || (tier == bestTier && lessContainerRef(c, best)) {
			best, bestTier = c, tier
		}
	}
	if best == nil {
		return 0, false
	}
	return best.ID, true
}

// lessContainerRef orders candidates by (path, line, id) ascending. The
// id is a last-resort total-order key; ties above it mean the same
// declaration site.
func lessContainerRef(a, b *sqlite.ContainerRef) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.ID < b.ID
}

// resolvePhase drains the two finalize resolutions in their required
// order — edges, then parent links — so the full and incremental
// pipelines cannot drift apart. Parent links must land before
// satisfyInterfaces (full scan only), which re-reads symbols from the DB.
func (h *harness) resolvePhase() error {
	if err := h.resolveAndWriteEdges(); err != nil {
		return err
	}
	return h.resolveParentLinks()
}

// resolveParentLinks drains pendingParents after the walk: load every
// container-kind symbol once, resolve each pending parent independently,
// and persist the links in one transaction. Runs in both the full-scan
// and incremental pipelines — the incremental leg is load-bearing, not
// symmetry: WriteSymbol's upsert clobbers parent_id on every rescan of a
// child's file, and only this pass restores the cross-file links.
func (h *harness) resolveParentLinks() error {
	if len(h.pendingParents) == 0 {
		return nil
	}
	refs, err := h.idx.ContainerRefs(h.ctx)
	if err != nil {
		return fmt.Errorf("load containers for parent resolution: %w", err)
	}
	byQualified := make(map[string][]sqlite.ContainerRef, len(refs))
	for _, r := range refs {
		byQualified[r.Qualified] = append(byQualified[r.Qualified], r)
	}

	type link struct{ symbolID, parentID int64 }
	var links []link
	for _, p := range h.pendingParents {
		if id, ok := pickParent(p, byQualified[p.ParentQualified]); ok {
			links = append(links, link{symbolID: p.SymbolID, parentID: id})
		}
	}
	if len(links) == 0 {
		return nil
	}
	return h.idx.InTx(h.ctx, func() error {
		for _, l := range links {
			if err := h.idx.UpdateSymbolParent(h.ctx, l.symbolID, l.parentID); err != nil {
				return err
			}
		}
		return nil
	})
}
