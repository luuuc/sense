package mcpserver

import "github.com/luuuc/sense/internal/mcpio"

// Per-session seen-symbol dedup. The handler tracks every symbol id it has
// already returned to this MCP session (search results, graph edge targets,
// blast callers) in seenSymbols. A later tool collapses entries the session
// already holds instead of re-sending them: sense_search blanks the snippet
// (search.go), sense_blast collapses already-seen direct callers into a
// seen_elsewhere summary (blast.go). This file is the one home for the read
// (seenPredicate) and write (markSeen) sides of that shared state.

// seenPredicate returns a SeenFunc the mcpio blast builders consult to decide
// which direct callers were already returned this session. The predicate
// locks per lookup, so it reflects the set as it stands when the builder runs
// — a blast records its own callers only afterwards (markSeen), so it never
// collapses against itself.
func (h *handlers) seenPredicate() mcpio.SeenFunc {
	return func(id int64) bool {
		h.seenMu.Lock()
		defer h.seenMu.Unlock()
		return h.seenSymbols[id]
	}
}

// markSeen records ids as returned to this session so a later collapsing tool
// dedups against them. The dedup is directional: sense_graph and sense_blast
// MARK what they return, but only sense_blast COLLAPSES already-seen direct
// callers (and sense_search blanks already-seen snippets) — graph never
// collapses. So the practical flows are graph→blast and blast→blast; a blast
// after a graph is the common one. A nil seenSymbols map (a handler built
// without session tracking) disables the dedup rather than panicking — the
// zero value is "track nothing".
func (h *handlers) markSeen(ids []int64) {
	if len(ids) == 0 {
		return
	}
	h.seenMu.Lock()
	defer h.seenMu.Unlock()
	if h.seenSymbols == nil {
		return
	}
	for _, id := range ids {
		h.seenSymbols[id] = true
	}
}
