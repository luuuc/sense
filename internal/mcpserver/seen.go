package mcpserver

import "github.com/luuuc/sense/internal/mcpio"

// Per-session seen-symbol dedup. The handler tracks every symbol id it has
// already returned to this MCP session (search results, graph edge targets,
// blast callers) in seenSymbols. A later tool collapses entries the session
// already holds instead of re-sending them: sense_search blanks the snippet
// (search.go), sense_blast collapses already-seen direct callers into a
// seen_elsewhere summary (blast.go), and sense_graph collapses already-seen
// targets in its deeper BFS layers (graph.go). This file is the one home
// for the read (seenPredicate) and write (markSeen) sides of that shared
// state. The model assumes shown ≈ retained: ids are marked when a response
// is built, not when delivery is confirmed (MCP has no ack), so a transport
// drop after marking degrades to a collapse the agent must recover via a
// fresh root query — roots never collapse.

// seenPredicate returns a SeenFunc the mcpio builders consult to decide
// which entries were already returned this session. The predicate locks per
// lookup, so it reflects the set as it stands when the builder runs — a
// call records its own rendered entries only afterwards (markSeen), so it
// never collapses against itself.
func (h *handlers) seenPredicate() mcpio.SeenFunc {
	return func(id int64) bool {
		h.seenMu.Lock()
		defer h.seenMu.Unlock()
		return h.seenSymbols[id]
	}
}

// markSeen records ids as returned to this session so a later collapsing tool
// dedups against them. sense_graph and sense_blast MARK what they render;
// sense_blast COLLAPSES already-seen direct callers, sense_search blanks
// already-seen snippets, and sense_graph collapses already-seen targets in
// its deeper BFS layers (never in the root's depth-1 edges — those are the
// direct answer to the question asked). The common flows include
// graph→blast, blast→blast, blast→graph, and the sibling fan-walk
// graph→graph, where each call's depth-2 layer repeats the same hub
// callers. A nil seenSymbols map (a handler built without session
// tracking) disables the dedup rather than panicking — the zero value is
// "track nothing".
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
