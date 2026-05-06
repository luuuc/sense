package mcpio

import (
	"strings"

	"github.com/luuuc/sense/internal/model"
)

const testCallerCollapseThreshold = 20

const (
	MaxGraphDepth = 3
	MaxPerHop     = 200
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
	Direction       model.Direction // DirectionBoth, DirectionCallers, DirectionCallees; "" == both
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

	resp.Edges = categorizeEdges(sc.Outbound, sc.Inbound, files, req.Direction)

	// Test-caller segmentation: split test callers out of CalledBy.
	var testCallers []CallEdgeRef
	if req.SegmentCallers && len(resp.Edges.CalledBy) > 0 {
		prod := resp.Edges.CalledBy[:0]
		for _, ref := range resp.Edges.CalledBy {
			if ref.File != nil && IsTestPath(*ref.File) {
				testCallers = append(testCallers, ref)
			} else {
				prod = append(prod, ref)
			}
		}
		resp.Edges.CalledBy = prod
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

// BuildFullGraphResponse builds the complete response for a multi-hop
// graph result, including root edges and any transitive layers.
// Metrics are recomputed after layers are added so they account for
// the full traversal, not just depth-1 edges.
func BuildFullGraphResponse(gr *model.GraphResult, files FileLookup, req BuildGraphRequest) GraphResponse {
	resp := BuildGraphResponse(&gr.Root, files, req)
	for i, layer := range gr.Layers {
		resp.Layers = append(resp.Layers, BuildGraphLayer(layer, i+2, files, req))
	}
	resp.Truncated = gr.Truncated

	if len(resp.Layers) > 0 {
		for _, l := range resp.Layers {
			resp.SenseMetrics.SymbolsReturned += countEdgeSymbols(l.Edges)
		}
		uniqueFiles := countUniqueEdgeFiles(resp, nil)
		resp.SenseMetrics.EstimatedFileReadsAvoided = uniqueFiles
		resp.SenseMetrics.EstimatedTokensSaved = uniqueFiles * AvgTokensPerFile
	}
	return resp
}

// BuildGraphLayer converts a model.HopEdges into a wire-format
// GraphLayer for the given hop depth.
func BuildGraphLayer(hop model.HopEdges, depth int, files FileLookup, req BuildGraphRequest) GraphLayer {
	return GraphLayer{
		Depth: depth,
		Edges: categorizeEdges(hop.Outbound, hop.Inbound, files, req.Direction),
	}
}

// categorizeEdges maps model edge refs into the wire-format GraphEdges
// shape, dispatching each edge kind to the right bucket. Temporal edges
// and test-caller segmentation are root-only concerns handled by
// BuildGraphResponse on top of this.
func categorizeEdges(outbound, inbound []model.EdgeRef, files FileLookup, direction model.Direction) GraphEdges {
	var edges GraphEdges

	if direction != model.DirectionCallers {
		for _, e := range outbound {
			switch e.Edge.Kind {
			case model.EdgeCalls:
				edges.Calls = append(edges.Calls, CallEdgeRef{
					Symbol:     qualifiedOrName(e.Target),
					File:       fileRefOrNil(e.Target.FileID, files),
					Confidence: Confidence(e.Edge.Confidence),
				})
			case model.EdgeInherits:
				edges.Inherits = append(edges.Inherits, InheritEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeComposes:
				edges.Composes = append(edges.Composes, ComposeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeIncludes:
				edges.Includes = append(edges.Includes, IncludeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeImports:
				edges.Imports = append(edges.Imports, ImportEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			default:
			}
		}
	}

	if direction != model.DirectionCallees {
		for _, e := range inbound {
			switch e.Edge.Kind {
			case model.EdgeCalls:
				edges.CalledBy = append(edges.CalledBy, CallEdgeRef{
					Symbol:     qualifiedOrName(e.Target),
					File:       fileRefOrNil(e.Target.FileID, files),
					Confidence: Confidence(e.Edge.Confidence),
				})
			case model.EdgeComposes:
				edges.Composes = append(edges.Composes, ComposeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeIncludes:
				edges.Includes = append(edges.Includes, IncludeEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeImports:
				edges.Imports = append(edges.Imports, ImportEdgeRef{
					Symbol: qualifiedOrName(e.Target),
					File:   fileRefOrNil(e.Target.FileID, files),
				})
			case model.EdgeTests:
				if path, ok := files(e.Target.FileID); ok {
					edges.Tests = append(edges.Tests, TestEdgeRef{
						File:       path,
						Confidence: Confidence(e.Edge.Confidence),
					})
				}
			default:
			}
		}
	}

	return edges
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

func countEdgeSymbols(edges GraphEdges) int {
	return len(edges.Calls) + len(edges.CalledBy) +
		len(edges.Inherits) + len(edges.Composes) +
		len(edges.Includes) + len(edges.Imports) +
		len(edges.Tests) + len(edges.Temporal)
}

func countUniqueEdgeFiles(resp GraphResponse, testCallers []CallEdgeRef) int {
	seen := map[string]struct{}{}
	collectEdgeFiles(resp.Edges, seen)
	for _, l := range resp.Layers {
		collectEdgeFiles(l.Edges, seen)
	}
	for _, e := range testCallers {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	return len(seen)
}

func collectEdgeFiles(edges GraphEdges, seen map[string]struct{}) {
	for _, e := range edges.Calls {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.CalledBy {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Inherits {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Composes {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Includes {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Imports {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Tests {
		seen[e.File] = struct{}{}
	}
	for _, e := range edges.Temporal {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
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
	if strings.Contains(path, "_test.") ||
		strings.Contains(path, ".test.") ||
		strings.Contains(path, "/test/") ||
		strings.Contains(path, "/tests/") ||
		strings.Contains(path, "/testdata/") ||
		strings.Contains(path, "/spec/") ||
		strings.HasPrefix(path, "test/") ||
		strings.HasPrefix(path, "tests/") ||
		strings.HasPrefix(path, "spec/") {
		return true
	}
	base := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		base = path[i+1:]
	}
	if strings.HasPrefix(base, "test_") {
		return true
	}
	if dot := strings.LastIndex(base, "."); dot > 0 {
		name := base[:dot]
		if strings.HasSuffix(name, "Test") || strings.HasSuffix(name, "Tests") {
			return true
		}
	}
	return false
}
