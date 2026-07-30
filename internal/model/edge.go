package model

// Edge mirrors the sense_edges table: one row per directed relationship.
//
// Line is a pointer because the underlying column is nullable for edges that
// aren't tied to a specific source line (e.g. file-level imports).
type Edge struct {
	ID         int64
	SourceID   *int64 // nil for file-level edges (e.g. routes, describe blocks)
	TargetID   int64
	Kind       EdgeKind
	FileID     int64
	Line       *int
	Confidence float64
}

// Int64Ptr returns a pointer to v. Convenience for constructing Edge literals.
func Int64Ptr(v int64) *int64 { return &v }

// EdgeKind enumerates the relationship categories the schema recognises.
// See 03-data-model.md for the canonical list.
type EdgeKind string

const (
	EdgeCalls      EdgeKind = "calls"
	EdgeImports    EdgeKind = "imports"
	EdgeInherits   EdgeKind = "inherits"
	EdgeIncludes   EdgeKind = "includes"
	EdgeTests      EdgeKind = "tests"
	EdgeComposes   EdgeKind = "composes"
	EdgeTemporal   EdgeKind = "temporal"
	EdgeReferences EdgeKind = "references"
)

// AllEdgeKinds is every kind the schema recognises, in declaration order. It exists so
// a CONSUMER can be tested for parity against the vocabulary instead of hardcoding a
// subset in a SQL string and silently ignoring the rest.
//
// The failure this guards against happened four times in one day: a producer gained a
// value and a consumer's hardcoded list never learned about it. The PHP extractor
// introduced `imports` two months after blast's traversal list was last touched, so
// blast never traversed it and a facade's real dependents went unreported. The same
// shape hit an index-caveat language switch, a bench scorer's file extensions, and a
// ledger key contract.
//
// Adding a kind here without updating every consumer's declared list (or its documented
// exemption) fails TestEdgeKindConsumerParity.
var AllEdgeKinds = []EdgeKind{
	EdgeCalls,
	EdgeImports,
	EdgeInherits,
	EdgeIncludes,
	EdgeTests,
	EdgeComposes,
	EdgeTemporal,
	EdgeReferences,
}

// SQLKindList renders kinds as a quoted, comma-separated SQL IN-list body, so a query
// builds its filter FROM the vocabulary rather than restating it as a literal. Values
// are package constants, never user input.
func SQLKindList(kinds []EdgeKind) string {
	out := make([]byte, 0, len(kinds)*12)
	for i, k := range kinds {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '\'')
		out = append(out, k...)
		out = append(out, '\'')
	}
	return string(out)
}
