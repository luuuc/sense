package mcpio

import (
	"strings"

	"github.com/luuuc/sense/internal/model"
)

const testCallerCollapseThreshold = 20

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
	Direction       string // "both", "callers", "callees"; "" == "both"
	SegmentCallers  bool   // split callers into production vs test
}

// BuildGraphResponse assembles the wire-shape response from the
// adapter's SymbolContext plus a file-path lookup. Direction
// semantics:
//
//   - "callers"  — inbound only (called_by + composes + tests)
//   - "callees"  — outbound only (calls + inherits + composes)
//   - "both"/"" — all edge kinds
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
			case model.EdgeComposes:
				resp.Edges.Composes = append(resp.Edges.Composes, ComposeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeIncludes:
				resp.Edges.Includes = append(resp.Edges.Includes, IncludeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeImports:
				resp.Edges.Imports = append(resp.Edges.Imports, ImportEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			}
		}
	}
	var testCallers []CallEdgeRef
	if wantInbound {
		for _, e := range sc.Inbound {
			switch e.Edge.Kind {
			case model.EdgeCalls:
				ref := CallEdgeRef{
					Symbol:     qualifiedOrName(e.Target),
					File:       fileRefOrNil(e.Target.FileID, files),
					Confidence: Confidence(e.Edge.Confidence),
				}
				if req.SegmentCallers && ref.File != nil && IsTestPath(*ref.File) {
					testCallers = append(testCallers, ref)
				} else {
					resp.Edges.CalledBy = append(resp.Edges.CalledBy, ref)
				}
			case model.EdgeComposes:
				resp.Edges.Composes = append(resp.Edges.Composes, ComposeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeIncludes:
				resp.Edges.Includes = append(resp.Edges.Includes, IncludeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeImports:
				resp.Edges.Imports = append(resp.Edges.Imports, ImportEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
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

	// Temporal edges are bidirectional — collect from outbound to get one
	// entry per partner, regardless of direction filter.
	temporalSeen := map[int64]struct{}{}
	for _, e := range sc.Outbound {
		if e.Edge.Kind != model.EdgeTemporal {
			continue
		}
		if _, dup := temporalSeen[e.Target.ID]; dup {
			continue
		}
		temporalSeen[e.Target.ID] = struct{}{}
		coChanges := 0
		if e.Edge.Line != nil {
			coChanges = *e.Edge.Line
		}
		resp.Edges.Temporal = append(resp.Edges.Temporal, TemporalEdgeRef{
			Symbol:    qualifiedOrName(e.Target),
			File:      fileRefOrNil(e.Target.FileID, files),
			CoChanges: coChanges,
			Strength:  Confidence(e.Edge.Confidence),
		})
	}
	for _, e := range sc.Inbound {
		if e.Edge.Kind != model.EdgeTemporal {
			continue
		}
		if _, dup := temporalSeen[e.Target.ID]; dup {
			continue
		}
		temporalSeen[e.Target.ID] = struct{}{}
		coChanges := 0
		if e.Edge.Line != nil {
			coChanges = *e.Edge.Line
		}
		resp.Edges.Temporal = append(resp.Edges.Temporal, TemporalEdgeRef{
			Symbol:    qualifiedOrName(e.Target),
			File:      fileRefOrNil(e.Target.FileID, files),
			CoChanges: coChanges,
			Strength:  Confidence(e.Edge.Confidence),
		})
	}

	if len(testCallers) > 0 {
		resp.TestCallerSummary = buildTestCallerSummary(testCallers)
	}

	symbolsReturned := len(resp.Edges.Calls) + len(resp.Edges.CalledBy) + len(testCallers) +
		len(resp.Edges.Inherits) + len(resp.Edges.Composes) +
		len(resp.Edges.Includes) + len(resp.Edges.Imports) + len(resp.Edges.Tests) +
		len(resp.Edges.Temporal)

	uniqueFiles := countUniqueEdgeFiles(resp, testCallers)
	resp.SenseMetrics = GraphMetrics{
		SymbolsReturned:           symbolsReturned,
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
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

func countUniqueEdgeFiles(resp GraphResponse, testCallers []CallEdgeRef) int {
	seen := map[string]struct{}{}
	for _, e := range resp.Edges.Calls {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.CalledBy {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range testCallers {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.Inherits {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.Composes {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.Includes {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.Imports {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range resp.Edges.Tests {
		seen[e.File] = struct{}{}
	}
	for _, e := range resp.Edges.Temporal {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	return len(seen)
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

// buildTestCallerSummary creates a TestCallerSummary from a list of
// test callers. When the count exceeds the collapse threshold, only
// 3 unique file path examples are kept.
func buildTestCallerSummary(callers []CallEdgeRef) *TestCallerSummary {
	seen := map[string]struct{}{}
	var examples []string
	for _, c := range callers {
		if c.File == nil {
			continue
		}
		if _, dup := seen[*c.File]; dup {
			continue
		}
		seen[*c.File] = struct{}{}
		examples = append(examples, *c.File)
	}
	summary := &TestCallerSummary{
		Count:    len(callers),
		Examples: examples,
	}
	if len(callers) > testCallerCollapseThreshold && len(examples) > 3 {
		summary.Examples = examples[:3]
	}
	return summary
}

// IsTestPath returns true if the file path matches common test
// directory or filename conventions.
func IsTestPath(path string) bool {
	return strings.Contains(path, "_test.") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/spec/") ||
		strings.HasPrefix(path, "test/") ||
		strings.HasPrefix(path, "tests/") ||
		strings.HasPrefix(path, "spec/")
}
