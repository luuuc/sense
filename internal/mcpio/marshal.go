package mcpio

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// MarshalGraph renders a GraphResponse as pretty-printed JSON bytes.
// Nil slices in the input are normalized to empty slices so the wire
// shape always has `[]`, never `null` — the policy stated on the
// types. Output has no trailing newline; callers choose their own
// framing (CLI appends "\n"; MCP wraps in a JSON-RPC envelope).
func MarshalGraph(r GraphResponse) ([]byte, error) {
	normalizeGraphResponse(&r)
	return marshalPretty(r)
}

// MarshalBlast renders a BlastResponse with the same normalization +
// pretty-print contract as MarshalGraph.
func MarshalBlast(r BlastResponse) ([]byte, error) {
	normalizeBlastResponse(&r)
	return marshalPretty(r)
}

// MarshalStatus renders a StatusResponse. No slice normalization is
// required — StatusResponse carries a map (Languages) that json
// already emits as `{}` for a nil value, and no []-typed fields.
func MarshalStatus(r StatusResponse) ([]byte, error) {
	if r.Languages == nil {
		r.Languages = map[string]StatusLanguage{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
	return marshalPretty(r)
}

// MarshalSearch renders a SearchResponse with the same normalization +
// pretty-print contract as MarshalGraph.
func MarshalSearch(r SearchResponse) ([]byte, error) {
	normalizeSearchResponse(&r)
	return marshalPretty(r)
}

// MarshalDeadCode renders a DeadCodeResponse with the same
// normalization + pretty-print contract as MarshalGraph.
func MarshalDeadCode(r DeadCodeResponse) ([]byte, error) {
	if r.DeadSymbols == nil {
		r.DeadSymbols = []DeadSymbolEntry{}
	}
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
	return marshalPretty(r)
}

// marshalPretty is the shared encoder: SetEscapeHTML(false) keeps
// identifier characters like `<`, `>`, `&` literal so goldens in
// card 3 pin the documented examples byte-for-byte. Two-space indent
// matches the documented examples.
func marshalPretty(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("mcpio: marshal: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
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
	if r.Edges.Composes == nil {
		r.Edges.Composes = []ComposeEdgeRef{}
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
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
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
	if r.NextSteps == nil {
		r.NextSteps = []NextStep{}
	}
}
