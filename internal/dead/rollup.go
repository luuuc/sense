package dead

// Rollup collapses parent-child dead symbols: when a class/module and
// all its methods are dead, report only the class. When a class is
// alive but some methods are dead, report the dead methods individually.
func Rollup(symbols []Symbol) []Symbol {
	deadIDs := make(map[int64]struct{}, len(symbols))
	for _, s := range symbols {
		deadIDs[s.ID] = struct{}{}
	}

	// Build parent → children index for dead symbols.
	childrenOf := map[int64][]Symbol{}
	for _, s := range symbols {
		if s.ParentID != nil {
			childrenOf[*s.ParentID] = append(childrenOf[*s.ParentID], s)
		}
	}

	// Identify containers where ALL children in the dead set should be
	// suppressed (the container subsumes them).
	suppressChildren := map[int64]struct{}{}
	for _, s := range symbols {
		if s.Kind != "class" && s.Kind != "module" {
			continue
		}
		children := childrenOf[s.ID]
		if len(children) == 0 {
			continue
		}
		// Container is dead and has dead children → collapse.
		suppressChildren[s.ID] = struct{}{}
	}

	var out []Symbol
	for _, s := range symbols {
		if s.ParentID != nil {
			if _, suppress := suppressChildren[*s.ParentID]; suppress {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}
