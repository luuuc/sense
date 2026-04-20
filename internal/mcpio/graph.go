package mcpio

import (
	"github.com/luuuc/sense/internal/model"
)

// FileLookup resolves a sense_files row id to its path. Used by
// BuildGraphResponse / BuildBlastResponse to hydrate edge endpoints.
// A miss returns ("", false); the builder then renders `File` as
// nil (CallEdgeRef / InheritEdgeRef) so the wire still distinguishes
// "external target, no indexed file" from "indexed file at path X".
type FileLookup func(fileID int64) (path string, ok bool)

// BuildGraphRequest carries the filters the CLI applied to the
// query. Today only Direction shapes the builder's output — the
// caller has already loaded the SymbolContext at the right depth
// from the DB layer.
type BuildGraphRequest struct {
	Direction string // "both", "callers", "callees"; "" == "both"
}

// BuildGraphResponse assembles the wire-shape response from the
// adapter's SymbolContext plus a file-path lookup. Direction
// semantics:
//
//   - "callers"  — inbound only (called_by + tests)
//   - "callees"  — outbound only (calls + inherits)
//   - "both"/"" — all four kinds
//
// The builder never returns an error: every miss maps to a
// defensible default. Call / inherit edges with an unknown file
// render as `"file": null` so the consumer still sees the symbol.
// Test edges with an unknown file are dropped entirely — the test
// row is (file, confidence) only, so without a file there is no
// row to emit.
func BuildGraphResponse(sc *model.SymbolContext, files FileLookup, req BuildGraphRequest) GraphResponse {
	resp := GraphResponse{
		Symbol: GraphSymbol{
			Name:      sc.Symbol.Name,
			Qualified: sc.Symbol.Qualified,
			File:      sc.File.Path,
			LineStart: sc.Symbol.LineStart,
			LineEnd:   sc.Symbol.LineEnd,
			Kind:      string(sc.Symbol.Kind),
		},
	}

	wantOutbound := req.Direction != "callers"
	wantInbound := req.Direction != "callees"

	if wantOutbound {
		for _, e := range sc.Outbound {
			switch e.Edge.Kind {
			case model.EdgeCalls:
				resp.Edges.Calls = append(resp.Edges.Calls, CallEdgeRef{
					Symbol:     qualifiedOrName(e.Target),
					File:       fileRefOrNil(e.Target.FileID, files),
					Confidence: Confidence(e.Edge.Confidence),
				})
			case model.EdgeInherits:
				resp.Edges.Inherits = append(resp.Edges.Inherits, InheritEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			}
		}
	}
	if wantInbound {
		for _, e := range sc.Inbound {
			switch e.Edge.Kind {
			case model.EdgeCalls:
				resp.Edges.CalledBy = append(resp.Edges.CalledBy, CallEdgeRef{
					Symbol:     qualifiedOrName(e.Target),
					File:       fileRefOrNil(e.Target.FileID, files),
					Confidence: Confidence(e.Edge.Confidence),
				})
			case model.EdgeTests:
				if path, ok := files(e.Target.FileID); ok {
					resp.Edges.Tests = append(resp.Edges.Tests, TestEdgeRef{
						File:       path,
						Confidence: Confidence(e.Edge.Confidence),
					})
				}
			}
		}
	}

	symbolsReturned := len(resp.Edges.Calls) + len(resp.Edges.CalledBy) +
		len(resp.Edges.Inherits) + len(resp.Edges.Tests)
	resp.SenseMetrics = GraphMetrics{
		SymbolsReturned: symbolsReturned,
		// EstimatedFileReadsAvoided / EstimatedTokensSaved stay nil —
		// the wire carries `null`. Pitch 01-05 chose honesty over a
		// heuristic here: "we do not yet measure this" is better
		// information for an agent than a plausible-looking number.
		// Pitch 04-03 replaces nil with real estimation math.
	}
	return resp
}

// qualifiedOrName prefers the qualified name but falls back to the
// bare name when qualified is empty — defensive against extractors
// that only emit unqualified identifiers for some kinds.
func qualifiedOrName(s model.Symbol) string {
	if s.Qualified != "" {
		return s.Qualified
	}
	return s.Name
}

// fileRefOrNil turns a FileID into a *string via FileLookup.
// Returning nil for unknown IDs lets the CallEdgeRef / InheritEdgeRef
// render `"file": null` for external targets (the documented shape
// for `Beacon.track`).
func fileRefOrNil(fileID int64, files FileLookup) *string {
	if path, ok := files(fileID); ok {
		return &path
	}
	return nil
}
