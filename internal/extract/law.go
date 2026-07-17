package extract

// The receiver/confidence law (decision 0003) governs the bare-name
// fallback every language's resolver eventually reaches: a method call
// whose receiver type could not be established. Two clauses,
// cross-language:
//
//  1. A bare-name fallback edge carries at most ConfidenceNameCollision
//     (0.3) - strictly below blast's traversal floor (0.5) - so an
//     unresolved-receiver guess can never pollute impact analysis or
//     caller queries at default settings.
//  2. A bare-name edge targeting a common name (a language's
//     builtin-shadowing or utility names: get, set, filter, id, ...) is
//     not emitted at all without a type witness: at those names a
//     receiverless match is overwhelmingly false (Django accuracy
//     findings 1/6/12), and a wrong edge is worse than no edge.
//
// PHP is the first language written against the law; the Ruby (0.8
// bare-name stamps, finding 6) and Python (builtin-shadowing 0.8s,
// finding 12) retrofits are bench-gated follow-ups that adopt this same
// guard.

// BareNameEdge decides clause 2 for one candidate edge: it reports the
// confidence a bare-name fallback edge to name must carry, and whether
// the edge may be emitted at all. common is the emitting language's
// common-name set (nil permits every name). A caller holding a type
// witness for the receiver is not making a bare-name edge and must not
// route through this guard.
func BareNameEdge(name string, common map[string]bool) (conf float64, ok bool) {
	if common[name] {
		return 0, false
	}
	return ConfidenceNameCollision, true
}
