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
	EdgeCalls    EdgeKind = "calls"
	EdgeImports  EdgeKind = "imports"
	EdgeInherits EdgeKind = "inherits"
	EdgeIncludes EdgeKind = "includes"
	EdgeTests    EdgeKind = "tests"
	EdgeComposes EdgeKind = "composes"
	EdgeTemporal EdgeKind = "temporal"
)
