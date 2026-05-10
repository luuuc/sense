package blast

import (
	"context"
	"database/sql"
)

// RollupParents adds parent-class symbols to DirectCallers for every
// method-level caller (direct or indirect) whose parent is not already
// present in the result. Parents are appended to DirectCallers and
// TotalAffected is updated to include the new entries.
//
// This is a post-processing step — the BFS engine is unchanged. The
// rollup ensures blast output includes class-level granularity expected
// by downstream scorers while preserving method-level detail.
func RollupParents(ctx context.Context, db *sql.DB, r *Result) error {
	// Mark every symbol already in the result, plus the subject itself,
	// so parents are deduplicated and the subject never appears in its
	// own caller list.
	seen := make(map[int64]struct{}, len(r.DirectCallers)+len(r.IndirectCallers)+1)
	for _, s := range r.DirectCallers {
		seen[s.ID] = struct{}{}
	}
	for _, hop := range r.IndirectCallers {
		seen[hop.Symbol.ID] = struct{}{}
	}
	seen[r.Symbol.ID] = struct{}{}

	var parentIDs []int64
	for _, s := range r.DirectCallers {
		if s.ParentID != nil {
			if _, ok := seen[*s.ParentID]; !ok {
				parentIDs = append(parentIDs, *s.ParentID)
				seen[*s.ParentID] = struct{}{}
			}
		}
	}
	for _, hop := range r.IndirectCallers {
		if hop.Symbol.ParentID != nil {
			if _, ok := seen[*hop.Symbol.ParentID]; !ok {
				parentIDs = append(parentIDs, *hop.Symbol.ParentID)
				seen[*hop.Symbol.ParentID] = struct{}{}
			}
		}
	}

	if len(parentIDs) == 0 {
		return nil
	}

	parents, err := loadSymbols(ctx, db, parentIDs)
	if err != nil {
		return err
	}

	for _, p := range parents {
		r.DirectCallers = append(r.DirectCallers, p)
	}
	sortSymbolsByID(r.DirectCallers)
	r.TotalAffected = len(r.DirectCallers) + len(r.IndirectCallers)
	return nil
}
