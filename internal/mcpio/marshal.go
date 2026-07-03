package mcpio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"

	"github.com/luuuc/sense/internal/model"
)

// MarshalGraph renders a GraphResponse as pretty-printed JSON bytes
// for the CLI surface. Nil slices in the input are normalized to
// empty slices so the wire shape always has `[]`, never `null` — the
// policy stated on the types. Output has no trailing newline;
// callers choose their own framing (CLI appends "\n"; MCP wraps in a
// JSON-RPC envelope).
//
// MCP callers should prefer MarshalGraphCompact: both forms decode to
// equal Go values, but the compact form drops indentation/newlines
// and saves ~30-50% bytes on sparse responses (pitch 25-05).
func MarshalGraph(r GraphResponse) ([]byte, error) {
	normalizeGraphResponse(&r)
	return marshalPretty(r)
}

// MarshalGraphCompact is MarshalGraph's compact-JSON sibling for MCP
// transport. Same normalization, no indentation.
func MarshalGraphCompact(r GraphResponse) ([]byte, error) {
	normalizeGraphResponse(&r)
	return marshalCompact(r)
}

// MarshalGraphCompactDirectional is the MCP-transport encoder for a
// direction-filtered sense_graph query. Edge buckets the request
// excluded (e.g. `calls` and `inherits` when direction == callers)
// are dropped from the wire entirely rather than emitting noisy `[]`.
// In-scope buckets still emit `[]` to preserve the
// "looked, found nothing" semantics documented in types.go.
//
// Direction-symmetric queries (Both / empty) defer to
// MarshalGraphCompact — the directional prune only activates when the
// agent explicitly narrowed the question.
func MarshalGraphCompactDirectional(r GraphResponse, direction model.Direction) ([]byte, error) {
	omits := outOfScopeEdgeBuckets(direction)
	if len(omits) == 0 {
		return MarshalGraphCompact(r)
	}
	normalizeGraphResponseScoped(&r, omits)
	raw, err := marshalCompact(r)
	if err != nil {
		return nil, err
	}
	return pruneNullEdgeBuckets(raw, omits), nil
}

// outOfScopeEdgeBuckets reports the GraphEdges JSON keys that are
// never populated for a one-sided direction filter. categorizeEdges
// in graph.go controls which buckets get filled per direction; this
// table is derived from that logic. Returns nil for direction == Both
// (or empty), meaning "render every bucket as today".
//
//	Callers — outbound (calls, composes) excluded.
//	Callees — inbound-only (called_by, composed_by, tests) excluded.
//	calls/called_by and composes/composed_by are directional pairs: the
//	outbound member is dropped for a callers query, the inbound member for
//	a callees query.
//	Inherits / InheritedBy are a directional pair (like calls/called_by):
//	the outbound member (supertypes) is dropped for a callers query, the
//	inbound member (subtypes) for a callees query.
//	Includes / Imports — populated by both paths (outbound = "what this
//	includes / imports", inbound = "what includes / imports this") into one
//	bucket, never excluded.
//	Temporal — bidirectional, never excluded.
func outOfScopeEdgeBuckets(direction model.Direction) []string {
	switch direction {
	case model.DirectionCallers:
		return []string{"calls", "composes", "inherits"}
	case model.DirectionCallees:
		return []string{"called_by", "composed_by", "tests", "inherited_by"}
	default:
		return nil
	}
}

// MarshalBlast renders a BlastResponse with the same normalization +
// pretty-print contract as MarshalGraph.
func MarshalBlast(r BlastResponse) ([]byte, error) {
	normalizeBlastResponse(&r)
	return marshalPretty(r)
}

// MarshalBlastCompact is MarshalBlast's compact-JSON sibling for MCP
// transport.
func MarshalBlastCompact(r BlastResponse) ([]byte, error) {
	normalizeBlastResponse(&r)
	return marshalCompact(r)
}

// MarshalStatus renders a StatusResponse. No slice normalization is
// required — StatusResponse carries a map (Languages) that json
// already emits as `{}` for a nil value, and no []-typed fields.
func MarshalStatus(r StatusResponse) ([]byte, error) {
	normalizeStatusResponse(&r)
	return marshalPretty(r)
}

// MarshalStatusCompact is MarshalStatus's compact-JSON sibling for MCP
// transport.
func MarshalStatusCompact(r StatusResponse) ([]byte, error) {
	normalizeStatusResponse(&r)
	return marshalCompact(r)
}

// MarshalSearch renders a SearchResponse with the same normalization +
// pretty-print contract as MarshalGraph.
func MarshalSearch(r SearchResponse) ([]byte, error) {
	normalizeSearchResponse(&r)
	return marshalPretty(r)
}

// MarshalSearchCompact is MarshalSearch's compact-JSON sibling for MCP
// transport.
func MarshalSearchCompact(r SearchResponse) ([]byte, error) {
	normalizeSearchResponse(&r)
	return marshalCompact(r)
}

// marshalPretty is the shared CLI encoder: SetEscapeHTML(false) keeps
// identifier characters like `<`, `>`, `&` literal so goldens pin the
// documented examples byte-for-byte. Two-space indent matches the
// documented examples.
func marshalPretty(v any) ([]byte, error) {
	return marshalJSON(v, true)
}

// marshalCompact is the shared MCP encoder: same escaping rules as
// marshalPretty but with no indentation. encoding/json still emits
// a single inter-token separator between map keys and array elements,
// so the output is valid JSON readable by every consumer; only the
// human-oriented whitespace is gone.
func marshalCompact(v any) ([]byte, error) {
	return marshalJSON(v, false)
}

func marshalJSON(v any, pretty bool) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("mcpio: marshal: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func estimateJSONTokens(v any) int {
	out, err := marshalPretty(v)
	if err != nil {
		return math.MaxInt
	}
	return len(out) / 4
}

// normalizeGraphResponse replaces nil slice fields with empty
// non-nil slices so encoding/json emits `[]` rather than `null`. The
// wire contract treats present-and-empty as "Sense looked, found
// nothing"; `null` would be "this emitter forgot a field," which is
// strictly worse semantics for consumers.
func normalizeGraphResponse(r *GraphResponse) {
	if r.Edges.Calls == nil {
		r.Edges.Calls = []CallEdgeRef{}
	}
	if r.Edges.CalledBy == nil {
		r.Edges.CalledBy = []CallEdgeRef{}
	}
	if r.Edges.Inherits == nil {
		r.Edges.Inherits = []InheritEdgeRef{}
	}
	if r.Edges.InheritedBy == nil {
		r.Edges.InheritedBy = []InheritEdgeRef{}
	}
	if r.Edges.Composes == nil {
		r.Edges.Composes = []ComposeEdgeRef{}
	}
	if r.Edges.ComposedBy == nil {
		r.Edges.ComposedBy = []ComposeEdgeRef{}
	}
	if r.Edges.Includes == nil {
		r.Edges.Includes = []IncludeEdgeRef{}
	}
	if r.Edges.Imports == nil {
		r.Edges.Imports = []ImportEdgeRef{}
	}
	if r.Edges.Tests == nil {
		r.Edges.Tests = []TestEdgeRef{}
	}
	if r.Edges.Temporal == nil {
		r.Edges.Temporal = []TemporalEdgeRef{}
	}
	if r.TestCallerSummary != nil && r.TestCallerSummary.Examples == nil {
		r.TestCallerSummary.Examples = []string{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}

// normalizeGraphResponseScoped is normalizeGraphResponse with a
// list of edge-bucket JSON keys to skip filling. Skipped buckets
// stay nil so encoding/json emits `"key":null`; the caller then
// strips those segments to omit the key entirely. omits must use
// the same JSON-key spelling as GraphEdges' struct tags.
//
// Layers carry their own per-hop Edges; the same scoped fill is
// applied recursively so deeper hops match the top-level shape.
func normalizeGraphResponseScoped(r *GraphResponse, omits []string) {
	skip := make(map[string]struct{}, len(omits))
	for _, k := range omits {
		skip[k] = struct{}{}
	}
	normalizeGraphEdgesScoped(&r.Edges, skip)
	for i := range r.Layers {
		normalizeGraphEdgesScoped(&r.Layers[i].Edges, skip)
	}
	if r.TestCallerSummary != nil && r.TestCallerSummary.Examples == nil {
		r.TestCallerSummary.Examples = []string{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}

func normalizeGraphEdgesScoped(e *GraphEdges, skip map[string]struct{}) {
	if _, ok := skip["calls"]; !ok && e.Calls == nil {
		e.Calls = []CallEdgeRef{}
	}
	if _, ok := skip["called_by"]; !ok && e.CalledBy == nil {
		e.CalledBy = []CallEdgeRef{}
	}
	if _, ok := skip["inherits"]; !ok && e.Inherits == nil {
		e.Inherits = []InheritEdgeRef{}
	}
	if _, ok := skip["inherited_by"]; !ok && e.InheritedBy == nil {
		e.InheritedBy = []InheritEdgeRef{}
	}
	if _, ok := skip["composes"]; !ok && e.Composes == nil {
		e.Composes = []ComposeEdgeRef{}
	}
	if _, ok := skip["composed_by"]; !ok && e.ComposedBy == nil {
		e.ComposedBy = []ComposeEdgeRef{}
	}
	if _, ok := skip["includes"]; !ok && e.Includes == nil {
		e.Includes = []IncludeEdgeRef{}
	}
	if _, ok := skip["imports"]; !ok && e.Imports == nil {
		e.Imports = []ImportEdgeRef{}
	}
	if _, ok := skip["tests"]; !ok && e.Tests == nil {
		e.Tests = []TestEdgeRef{}
	}
	if _, ok := skip["temporal"]; !ok && e.Temporal == nil {
		e.Temporal = []TemporalEdgeRef{}
	}
}

// pruneNullEdgeBuckets removes `"key":null` entries for the named
// edge buckets from compact JSON output. Operates on raw bytes
// because encoding/json offers no per-key conditional omission that
// keeps `[]` for in-scope buckets while dropping `null` for
// out-of-scope ones. Compact JSON has no whitespace, so the search
// pattern is unambiguous.
//
// Each pattern is searched in three positional forms:
//   - `"key":null,` (mid-object)
//   - `,"key":null` (last entry, preceding comma to consume)
//   - `"key":null`  (only entry — unusual but handled)
//
// The function operates only on the first occurrence of each key
// inside the edges block. GraphEdges keys are unique within the
// response, so first-match is correct.
func pruneNullEdgeBuckets(raw []byte, keys []string) []byte {
	for _, k := range keys {
		mid := []byte(`"` + k + `":null,`)
		raw = bytes.ReplaceAll(raw, mid, nil)
		tail := []byte(`,"` + k + `":null`)
		raw = bytes.ReplaceAll(raw, tail, nil)
		only := []byte(`"` + k + `":null`)
		raw = bytes.ReplaceAll(raw, only, nil)
	}
	return raw
}

// normalizeSearchResponse replaces nil Results with an empty slice.
func normalizeSearchResponse(r *SearchResponse) {
	if r.Results == nil {
		r.Results = []SearchResultEntry{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}

// normalizeStatusResponse fills the map/slice fields that
// StatusResponse expects to render as `{}` / `[]` instead of `null`.
func normalizeStatusResponse(r *StatusResponse) {
	if r.Languages == nil {
		r.Languages = map[string]StatusLanguage{}
	}
	if r.Structure != nil {
		if r.Structure.TopNamespaces == nil {
			r.Structure.TopNamespaces = []StatusNamespace{}
		}
		if r.Structure.HubSymbols == nil {
			r.Structure.HubSymbols = []StatusHub{}
		}
		if r.Structure.EntryPoints == nil {
			r.Structure.EntryPoints = []StatusEntryPoint{}
		}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}

// MarshalUnreferenced renders an UnreferencedResponse with the shared
// CLI pretty-print contract.
func MarshalUnreferenced(r UnreferencedResponse) ([]byte, error) {
	normalizeUnreferencedResponse(&r)
	return marshalPretty(r)
}

// MarshalUnreferencedCompact is MarshalUnreferenced's compact-JSON sibling
// for MCP transport.
func MarshalUnreferencedCompact(r UnreferencedResponse) ([]byte, error) {
	normalizeUnreferencedResponse(&r)
	return marshalCompact(r)
}

// normalizeUnreferencedResponse fills every slice on UnreferencedResponse and
// its nested groups so the wire always emits `[]` rather than `null` for an
// empty list (the "Sense looked, found nothing" invariant).
func normalizeUnreferencedResponse(r *UnreferencedResponse) {
	if r.Unreferenced.Dead == nil {
		r.Unreferenced.Dead = []DeadEntry{}
	}
	if r.Unreferenced.PossiblyDead == nil {
		r.Unreferenced.PossiblyDead = []PossiblyDeadGroup{}
	}
	for i := range r.Unreferenced.PossiblyDead {
		if r.Unreferenced.PossiblyDead[i].Symbols == nil {
			r.Unreferenced.PossiblyDead[i].Symbols = []PossiblyDeadSymbol{}
		}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}

// normalizeBlastResponse mirrors normalizeGraphResponse for every
// slice on BlastResponse.
func normalizeBlastResponse(r *BlastResponse) {
	if r.RiskFactors == nil {
		r.RiskFactors = []string{}
	}
	if r.DirectCallers == nil {
		r.DirectCallers = []BlastCaller{}
	}
	if r.IndirectCallers == nil {
		r.IndirectCallers = []BlastIndirect{}
	}
	if r.AffectedTests == nil {
		r.AffectedTests = []string{}
	}
	if r.AffectedSubclasses == nil {
		r.AffectedSubclasses = []BlastCaller{}
	}
	if r.AffectedViaComposition == nil {
		r.AffectedViaComposition = []BlastCaller{}
	}
	if r.AffectedViaIncludes == nil {
		r.AffectedViaIncludes = []BlastCaller{}
	}
	if r.References.Examples == nil {
		r.References.Examples = []BlastCaller{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}
