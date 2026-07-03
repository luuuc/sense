package mcpio

import (
	"context"
	"fmt"
	"strings"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

const testCallerCollapseThreshold = 20

// ApplyGraphBudget trims a graph response until its estimated token count
// fits within budget. It sheds the least-relevant content first — deeper
// BFS layers, then the longest edge list one chunk at a time — keeping
// the focal symbol and the highest-signal edges. Dropped edges are
// counted in OmittedEdges and set Truncated, so the consumer knows the
// view is partial. A non-positive budget disables trimming.
//
// Applied on the MCP path only, matching ApplyBlastBudget: the CLI emits
// the full response for scripting, the MCP surface stays within context.
func ApplyGraphBudget(resp *GraphResponse, budget int) {
	if budget <= 0 {
		return
	}
	// 1. Deeper layers are the weakest signal — drop whole hops first.
	for estimateJSONTokens(resp) > budget && len(resp.Layers) > 0 {
		dropped := countEdgeSymbols(resp.Layers[len(resp.Layers)-1].Edges)
		resp.Layers = resp.Layers[:len(resp.Layers)-1]
		resp.OmittedEdges += dropped
		resp.Truncated = true
	}
	// 2. Trim the longest root edge list, a chunk at a time, until it fits.
	for estimateJSONTokens(resp) > budget && trimLongestEdgeList(&resp.Edges, resp) {
		resp.Truncated = true
	}
}

// trimLongestEdgeList drops a chunk from whichever root edge slice is
// longest, keeping at least one entry per kind so each relationship type
// stays represented. Returns false when nothing more can be trimmed.
func trimLongestEdgeList(e *GraphEdges, resp *GraphResponse) bool {
	type slot struct {
		n    int
		drop func(int)
	}
	slots := []slot{
		{len(e.CalledBy), func(k int) { e.CalledBy = e.CalledBy[:len(e.CalledBy)-k] }},
		{len(e.Calls), func(k int) { e.Calls = e.Calls[:len(e.Calls)-k] }},
		{len(e.Composes), func(k int) { e.Composes = e.Composes[:len(e.Composes)-k] }},
		{len(e.ComposedBy), func(k int) { e.ComposedBy = e.ComposedBy[:len(e.ComposedBy)-k] }},
		{len(e.Inherits), func(k int) { e.Inherits = e.Inherits[:len(e.Inherits)-k] }},
		{len(e.InheritedBy), func(k int) { e.InheritedBy = e.InheritedBy[:len(e.InheritedBy)-k] }},
		{len(e.Includes), func(k int) { e.Includes = e.Includes[:len(e.Includes)-k] }},
		{len(e.Imports), func(k int) { e.Imports = e.Imports[:len(e.Imports)-k] }},
		{len(e.Temporal), func(k int) { e.Temporal = e.Temporal[:len(e.Temporal)-k] }},
		{len(e.Tests), func(k int) { e.Tests = e.Tests[:len(e.Tests)-k] }},
	}
	best := -1
	for i, s := range slots {
		if best == -1 || s.n > slots[best].n {
			best = i
		}
	}
	if best == -1 || slots[best].n <= 1 {
		return false
	}
	drop := trimStep(slots[best].n - 1)
	slots[best].drop(drop)
	resp.OmittedEdges += drop
	return true
}

const (
	MaxGraphDepth = 3
	MaxPerHop     = 200
)

// graphConfidenceFloor hides usage edges (calls / references) whose
// resolution confidence is below blast's traversal floor. It tracks the same
// constant the sqlite fold uses (extract.ConfidenceUnresolved == 0.5) so the
// display floor and the fold floor move together if the ladder is retuned.
// Below it live bare-name and basic-tier guesses — e.g. the 0.3 ERB i18n edges
// that point at a Ruby method's own definition line, which read as real
// dependencies in raw graph output and carry a misattributed snippet. blast
// already filters them; sense_graph now matches. Structural edges
// (inherits/composes/includes/imports) and tests are never confidence-floored
// — they're syntactically explicit.
const graphConfidenceFloor = extract.ConfidenceUnresolved

// GraphConfidenceFloor is the exported default usage-edge floor for
// sense_graph — the value min_confidence defaults to when the parameter is
// absent. It mirrors graphConfidenceFloor so callers outside this package
// (the MCP handler) resolve the same default the builder uses.
const GraphConfidenceFloor = graphConfidenceFloor

// MinGraphConfidence is the smallest positive floor the MCP handler clamps an
// explicit min_confidence to. It keeps an explicit 0.0 ("show everything")
// distinguishable from an unset field in BuildGraphRequest.edgeFloor, whose
// zero-value guard resolves to the default floor. The lowest real usage-edge
// confidence is 0.3 (name collision), so any floor at or below it surfaces
// every stored caller.
const MinGraphConfidence = 0.01

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
	Direction      model.Direction // DirectionBoth, DirectionCallers, DirectionCallees; "" == both
	SegmentCallers bool
	Snippets       *SnippetReader
	// MinConfidence is the floor below which usage edges (calls/references)
	// are hidden and counted in LowConfidenceHidden. A zero value means
	// "unset" and resolves to graphConfidenceFloor, so CLI and benchmark
	// callers that leave it blank keep the default 0.5 floor. The MCP handler
	// passes an explicit value (default 0.5, lowered via the min_confidence
	// parameter to surface name-collision and other below-floor callers).
	MinConfidence float64
}

// edgeFloor resolves the request's usage-edge confidence floor, treating a
// zero/unset MinConfidence as the default graphConfidenceFloor.
func (r BuildGraphRequest) edgeFloor() float64 {
	if r.MinConfidence <= 0 {
		return graphConfidenceFloor
	}
	return r.MinConfidence
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
func BuildGraphResponse(ctx context.Context, sc *model.SymbolContext, files FileLookup, req BuildGraphRequest) GraphResponse {
	resp := GraphResponse{
		Symbol: GraphSymbol{
			Name:      sc.Symbol.Name,
			Qualified: sc.Symbol.Qualified,
			File:      sc.File.Path,
			LineStart: sc.Symbol.LineStart,
			LineEnd:   sc.Symbol.LineEnd,
			Kind:      string(sc.Symbol.Kind),
			Ref:       FormatRef(sc.File.Path, sc.Symbol.LineStart),
		},
	}

	var hidden int
	resp.Edges, hidden = categorizeEdges(ctx, sc.Outbound, sc.Inbound, files, req.Direction, req.Snippets, req.edgeFloor())
	resp.LowConfidenceHidden = hidden
	resp.SnippetsTruncated = req.Snippets.Truncated(len(sc.Outbound) + len(sc.Inbound))

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
		fp := fileRefOrNil(e.Target.FileID, files)
		resp.Edges.Temporal = append(resp.Edges.Temporal, TemporalEdgeRef{
			Symbol:    qualifiedOrName(e.Target),
			File:      fp,
			LineStart: e.Target.LineStart,
			LineEnd:   e.Target.LineEnd,
			Ref:       FormatRefPtr(fp, e.Target.LineStart),
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
		fp := fileRefOrNil(e.Target.FileID, files)
		resp.Edges.Temporal = append(resp.Edges.Temporal, TemporalEdgeRef{
			Symbol:    qualifiedOrName(e.Target),
			File:      fp,
			LineStart: e.Target.LineStart,
			LineEnd:   e.Target.LineEnd,
			Ref:       FormatRefPtr(fp, e.Target.LineStart),
			CoChanges: coChanges,
			Strength:  Confidence(e.Edge.Confidence),
		})
	}

	if len(testCallers) > 0 {
		resp.TestCallerSummary = buildTestCallerSummary(testCallers)
	}

	resp.VerifyHint = graphVerifyHint(resp)
	resp.IndexCaveat = graphIndexCaveat(resp)
	resp.ViewEdges = viewEdgesSignal(sc.File.Path, anyViewTemplate(inboundEdgeFiles(sc.Inbound, files)))

	symbolsReturned := len(resp.Edges.Calls) + len(resp.Edges.CalledBy) + len(testCallers) +
		len(resp.Edges.Inherits) + len(resp.Edges.InheritedBy) + len(resp.Edges.Composes) +
		len(resp.Edges.Includes) + len(resp.Edges.Imports) + len(resp.Edges.Tests) +
		len(resp.Edges.Temporal)

	uniqueFiles := countUniqueEdgeFiles(resp, testCallers)
	resp.SenseMetrics = GraphMetrics{
		SymbolsReturned:           symbolsReturned,
		EstimatedFileReadsAvoided: uniqueFiles,
		EstimatedTokensSaved:      uniqueFiles * AvgTokensPerFile,
	}
	resp.Completeness = graphCompleteness(&resp, symbolsReturned)
	return resp
}

// graphCompleteness builds the consolidated stop/verify verdict for a
// graph response, reusing the existing
// low_confidence_hidden / omitted_edges / truncated signals so the agent
// can branch on ONE field. "complete" means nothing was filtered or
// dropped; otherwise "partial" with the hidden count and how to widen.
// Dynamic-dispatch residual stays in index_caveat, never folded in here.
func graphCompleteness(resp *GraphResponse, symbolsReturned int) *Completeness {
	hidden := resp.LowConfidenceHidden + resp.OmittedEdges
	if resp.Truncated || hidden > 0 {
		advice := "Partial: re-run with min_confidence=0.3 or a specific direction to see the rest."
		if resp.OmittedEdges > 0 {
			advice = "Partial: edges dropped for token budget — narrow with a direction or a specific symbol."
		}
		return &Completeness{Verdict: "partial", Resolved: symbolsReturned, Hidden: hidden, Advice: advice}
	}
	return &Completeness{
		Verdict:  "complete",
		Resolved: symbolsReturned,
		Advice:   "Complete resolvable edge set — act on it, do not re-grep. Dynamic-dispatch residual, if any, is in index_caveat.",
	}
}

// BuildFullGraphResponse builds the complete response for a multi-hop
// graph result, including root edges and any transitive layers.
// Metrics are recomputed after layers are added so they account for
// the full traversal, not just depth-1 edges.
func BuildFullGraphResponse(ctx context.Context, gr *model.GraphResult, files FileLookup, req BuildGraphRequest) GraphResponse {
	resp := BuildGraphResponse(ctx, &gr.Root, files, req)
	for i, layer := range gr.Layers {
		resp.Layers = append(resp.Layers, BuildGraphLayer(ctx, layer, i+2, files, req))
	}
	resp.Truncated = gr.Truncated
	totalEdges := len(gr.Root.Outbound) + len(gr.Root.Inbound)
	for _, l := range gr.Layers {
		totalEdges += len(l.Outbound) + len(l.Inbound)
	}
	resp.SnippetsTruncated = req.Snippets.Truncated(totalEdges)

	if len(resp.Layers) > 0 {
		for _, l := range resp.Layers {
			resp.SenseMetrics.SymbolsReturned += countEdgeSymbols(l.Edges)
		}
		uniqueFiles := countUniqueEdgeFiles(resp, nil)
		resp.SenseMetrics.EstimatedFileReadsAvoided = uniqueFiles
		resp.SenseMetrics.EstimatedTokensSaved = uniqueFiles * AvgTokensPerFile
	}
	// Layer truncation is known only here — recompute the verdict so a
	// multi-hop trim downgrades "complete" honestly.
	resp.Completeness = graphCompleteness(&resp, resp.SenseMetrics.SymbolsReturned)
	return resp
}

// BuildGraphLayer converts a model.HopEdges into a wire-format
// GraphLayer for the given hop depth.
func BuildGraphLayer(ctx context.Context, hop model.HopEdges, depth int, files FileLookup, req BuildGraphRequest) GraphLayer {
	edges, _ := categorizeEdges(ctx, hop.Outbound, hop.Inbound, files, req.Direction, req.Snippets, req.edgeFloor())
	return GraphLayer{
		Depth: depth,
		Edges: edges,
	}
}

// categorizeEdges maps model edge refs into the wire-format GraphEdges
// shape, dispatching each edge kind to the right bucket. Temporal edges
// and test-caller segmentation are root-only concerns handled by
// BuildGraphResponse on top of this. Usage edges (calls / references)
// below floor are dropped and counted in the returned hidden total so the
// caller can report them rather than silently omit. floor is the resolved
// usage-edge confidence floor (graphConfidenceFloor by default; lower to
// surface name-collision and other below-floor callers).
func categorizeEdges(ctx context.Context, outbound, inbound []model.EdgeRef, files FileLookup, direction model.Direction, snippets *SnippetReader, floor float64) (GraphEdges, int) {
	var edges GraphEdges
	var hidden int

	readSnippet := func(e model.EdgeRef) *CallSite {
		if e.Edge.Line == nil {
			return nil
		}
		edgeFile, ok := files(e.Edge.FileID)
		if !ok {
			return nil
		}
		return snippets.Read(ctx, edgeFile, *e.Edge.Line)
	}

	if direction != model.DirectionCallers {
		for _, e := range outbound {
			switch e.Edge.Kind {
			case model.EdgeCalls, model.EdgeReferences:
				if e.Edge.Confidence < floor {
					hidden++
					continue
				}
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Calls = append(edges.Calls, CallEdgeRef{
					ID:         e.Target.ID,
					Symbol:     qualifiedOrName(e.Target),
					File:       fp,
					LineStart:  e.Target.LineStart,
					LineEnd:    e.Target.LineEnd,
					Ref:        FormatRefPtr(fp, e.Target.LineStart),
					Confidence: Confidence(e.Edge.Confidence),
					CallSite:   readSnippet(e),
				})
			case model.EdgeInherits:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Inherits = append(edges.Inherits, InheritEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeComposes:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Composes = append(edges.Composes, ComposeEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeIncludes:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Includes = append(edges.Includes, IncludeEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeImports:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Imports = append(edges.Imports, ImportEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			default:
			}
		}
	}

	if direction != model.DirectionCallees {
		for _, e := range inbound {
			switch e.Edge.Kind {
			case model.EdgeCalls, model.EdgeReferences:
				if e.Edge.Confidence < floor {
					hidden++
					continue
				}
				sym := qualifiedOrName(e.Target)
				fp := fileRefOrNil(e.Target.FileID, files)
				lineStart, lineEnd := e.Target.LineStart, e.Target.LineEnd
				if e.Target.ID == 0 {
					// The source didn't resolve to an indexed symbol, so the
					// LEFT-joined source columns are empty. In practice this is a
					// view-origin edge — a template (ERB data-controller /
					// data-action / render / i18n) calling into the subject — but
					// the branch fires for ANY unresolved-source calls/references
					// edge. The edge's own file id always points at the true
					// emitting file, so surface it (and the call-site line) instead
					// of a blank {symbol:"", file:null} stub. Note this means
					// Symbol may carry a file path rather than a qualified name for
					// such callers.
					if path, ok := files(e.Edge.FileID); ok {
						sym = path
						fp = fileRefOrNil(e.Edge.FileID, files)
						// An unresolved target already carries zero lines; lift the
						// edge's own call-site line when it has one.
						if e.Edge.Line != nil {
							lineStart, lineEnd = *e.Edge.Line, *e.Edge.Line
						}
					}
				}
				edges.CalledBy = append(edges.CalledBy, CallEdgeRef{
					ID:         e.Target.ID,
					Symbol:     sym,
					File:       fp,
					LineStart:  lineStart,
					LineEnd:    lineEnd,
					Ref:        FormatRefPtr(fp, lineStart),
					Confidence: Confidence(e.Edge.Confidence),
					CallSite:   readSnippet(e),
				})
			case model.EdgeInherits:
				// Inheritors of this symbol (subclasses, trait
				// implementors) route to InheritedBy — distinct from
				// the outbound Inherits bucket (supertypes), so "what
				// extends me" is not conflated with "what I extend".
				// Mirrors the Composes / ComposedBy split.
				//
				// Skip edges whose source didn't resolve to an
				// indexed symbol (Target.ID == 0). For inbound
				// loads, Target carries the source side via a
				// LEFT JOIN, so unresolved-source rows surface here
				// as empty stubs. Common in Rust blanket impls
				// like `impl Trait for F` where F is a generic
				// type parameter, not a defined symbol.
				if e.Target.ID == 0 {
					continue
				}
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.InheritedBy = append(edges.InheritedBy, InheritEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeComposes:
				// Inbound: a symbol that composes THIS one (holds a has-a
				// relationship to it). Routed to ComposedBy, distinct from the
				// outbound Composes bucket, so the reverse fan-out a Django model's
				// FK dependents form is not conflated with what the model owns.
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.ComposedBy = append(edges.ComposedBy, ComposeEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeIncludes:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Includes = append(edges.Includes, IncludeEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
				})
			case model.EdgeImports:
				fp := fileRefOrNil(e.Target.FileID, files)
				edges.Imports = append(edges.Imports, ImportEdgeRef{
					Symbol:    qualifiedOrName(e.Target),
					File:      fp,
					LineStart: e.Target.LineStart,
					LineEnd:   e.Target.LineEnd,
					Ref:       FormatRefPtr(fp, e.Target.LineStart),
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

	return edges, hidden
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
		len(edges.Inherits) + len(edges.InheritedBy) +
		len(edges.Composes) + len(edges.ComposedBy) +
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
	for _, e := range edges.InheritedBy {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.Composes {
		if e.File != nil {
			seen[*e.File] = struct{}{}
		}
	}
	for _, e := range edges.ComposedBy {
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

// FormatRef builds a copy-paste-ready "file:line" reference string.
func FormatRef(file string, lineStart int) string {
	if file == "" || lineStart == 0 {
		return ""
	}
	return fmt.Sprintf("%s:%d", file, lineStart)
}

// FormatRefPtr is FormatRef for nullable file pointers (edge endpoints).
func FormatRefPtr(file *string, lineStart int) string {
	if file == nil {
		return ""
	}
	return FormatRef(*file, lineStart)
}

// graphIndexCaveat emits a language-specific list of relationship classes
// the index may miss. Suppressed for trivial responses (no relationships
// found) — there's nothing to caveat — and when the symbol has no file
// (external/unknown). The hint is otherwise unconditional: even a complete-
// looking caller list can omit dynamic dispatch the agent must verify.
func graphIndexCaveat(resp GraphResponse) string {
	if resp.Symbol.File == "" {
		return ""
	}
	hasEdges := len(resp.Edges.Calls)+len(resp.Edges.CalledBy)+
		len(resp.Edges.Inherits)+len(resp.Edges.InheritedBy)+
		len(resp.Edges.Composes)+len(resp.Edges.Includes)+len(resp.Edges.Imports) > 0
	if !hasEdges {
		return ""
	}
	return IndexCaveat(resp.Symbol.File)
}

// inboundEdgeFiles returns the file each inbound usage edge was emitted from.
// This is the edge's own file_id, NOT the caller symbol's file: a view edge
// (ERB → Ruby/JS) is stored with source_id NULL — the source is a template,
// not a symbol — so the caller-symbol file is absent, but the edge's file_id
// is the ERB template. Checking the edge file is the only way view-reach
// surfaces. Only usage edges (calls / references) count.
func inboundEdgeFiles(inbound []model.EdgeRef, files FileLookup) []string {
	out := make([]string, 0, len(inbound))
	for _, e := range inbound {
		if e.Edge.Kind != model.EdgeCalls && e.Edge.Kind != model.EdgeReferences {
			continue
		}
		if p, ok := files(e.Edge.FileID); ok {
			out = append(out, p)
		}
	}
	return out
}

func graphVerifyHint(resp GraphResponse) string {
	if len(resp.Edges.CalledBy) > 0 {
		return ""
	}
	kind := resp.Symbol.Kind
	if kind != string(model.KindConstant) && kind != string(model.KindFunction) {
		return ""
	}
	if len(resp.Edges.Calls) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Zero callers found in the index. Constants and short functions may have callers not captured by static analysis. Verify with: grep -rn '%s' .",
		resp.Symbol.Name,
	)
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
