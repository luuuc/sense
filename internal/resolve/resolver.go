// Package resolve turns the qualified-name strings extractors emit on
// edges into concrete symbol_ids, applying scope-aware preference and
// per-language receiver rewrites along the way.
//
// The resolver is stateless across Resolve calls — it carries only
// the in-memory name index built from the adapter's SymbolRefs
// output. Callers (today only the scan harness; tomorrow potentially
// incremental-scan or watch modes) build one Index per resolution
// pass and reuse it across every pending edge.
package resolve

import (
	"strings"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// Index is an in-memory lookup over the symbol table optimised for
// the two resolution paths: exact qualified name, and trailing
// unqualified segment (the calls-edge fallback the pitch specifies).
//
// Both maps preserve insertion order within each bucket; because
// SymbolRefs is guaranteed to return rows by ascending id, a bucket's
// first element is the earliest-written match — the deterministic
// tie-break when no scope preference can decide.
type Index struct {
	byQualified map[string][]model.SymbolRef
	byName      map[string][]model.SymbolRef
}

// NewIndex builds an Index from the bulk SymbolRefs output. The input
// slice is expected to be ordered by ascending id; order is
// preserved in each map bucket.
func NewIndex(refs []model.SymbolRef) *Index {
	ix := &Index{
		byQualified: make(map[string][]model.SymbolRef, len(refs)),
		byName:      make(map[string][]model.SymbolRef, len(refs)),
	}
	for _, r := range refs {
		ix.byQualified[r.Qualified] = append(ix.byQualified[r.Qualified], r)
		name := unqualifiedName(r.Qualified)
		ix.byName[name] = append(ix.byName[name], r)
	}
	return ix
}

// Request carries everything the resolver needs to turn one pending
// edge's target-name string into a symbol_id. SourceQualified and
// SourceParentQualified enable receiver rewrites (`self.foo` /
// `Self::bar` ⇒ `Parent<sep>name`). SourceFileID enables the
// scope-aware preference when more than one candidate matches.
type Request struct {
	Target                string
	Kind                  model.EdgeKind
	SourceFileID          int64
	SourceQualified       string
	SourceParentQualified string
	BaseConfidence        float64
}

// Result is the output of a successful resolution. Ambiguous is set
// when resolution had to pick among more than one candidate at the
// same lookup site — callers can route those to a diagnostic stream
// without re-running the resolver. Unqualified-fallback matches are
// not Ambiguous unless the fallback itself had multiple candidates;
// low confidence and ambiguity are tracked independently.
type Result struct {
	SymbolID   int64
	Confidence float64
	Ambiguous  bool
}

// ambiguousConfidence is the ceiling applied whenever resolution has
// to pick among more than one candidate, or when an unqualified
// fallback succeeds. Sourced from the centralized extract.
// ConfidenceAmbiguous so the emit-side and resolve-side confidence
// policies stay in one place; see that const for the pitch rationale.
const ambiguousConfidence = extract.ConfidenceAmbiguous

// Resolve looks up req.Target and returns the best candidate. Returns
// ok=false when nothing matches — callers (scan) drop unresolved
// edges today; Card 8 may wire the unresolved set to diagnostic
// output.
//
// Algorithm:
//
//  1. Apply receiver rewrite (`self.` / `Self::` ⇒ parent-prefixed)
//     when the source symbol has a parent qualified name.
//  2. Exact match via byQualified. Single hit ⇒ BaseConfidence.
//     Multiple ⇒ same-file preferred, else lowest-id; confidence
//     clamped to ambiguousConfidence.
//  3. For calls edges only, fall back to unqualified-name match via
//     byName (pitch: "fall back to unqualified name, lower
//     confidence"). Same scope preference; confidence clamped to
//     ambiguousConfidence.
//  4. No match ⇒ ok=false.
func (ix *Index) Resolve(req Request) (Result, bool) {
	target := rewriteReceiver(req.Target, req.SourceQualified, req.SourceParentQualified)

	if matches := ix.byQualified[target]; len(matches) > 0 {
		return pickBest(matches, req.SourceFileID, req.BaseConfidence), true
	}

	if req.Kind == model.EdgeCalls {
		// Unqualified fallback: find symbols whose trailing segment
		// matches the target's trailing segment. Applies to bare
		// targets ("say" ⇒ byName["say"]) as well as dotted targets
		// whose full qualified form missed ("mod.Sprintf" ⇒
		// byName["Sprintf"]). byQualified and byName are different
		// indexes, so looking up the same key in both is not
		// duplicate work.
		if name := unqualifiedName(target); name != "" {
			if matches := ix.byName[name]; len(matches) > 0 {
				r := pickBest(matches, req.SourceFileID, req.BaseConfidence)
				if r.Confidence > ambiguousConfidence {
					r.Confidence = ambiguousConfidence
				}
				return r, true
			}
		}
	}

	return Result{}, false
}

// pickBest selects a winner among one or more candidates. A single
// match keeps BaseConfidence and is not flagged ambiguous. Any
// result drawn from more than one candidate — same-file or
// cross-file — is flagged Ambiguous and its confidence clamped to
// ambiguousConfidence, matching the pitch's rule that ambiguous
// resolution drops confidence regardless of how the tie was broken.
//
// Tie-break order: same-file first (by NewIndex's ascending-id
// contract, the earliest same-file match wins), then the lowest id
// across files.
func pickBest(matches []model.SymbolRef, sourceFileID int64, baseConfidence float64) Result {
	if len(matches) == 1 {
		return Result{SymbolID: matches[0].ID, Confidence: baseConfidence}
	}
	c := baseConfidence
	if c > ambiguousConfidence {
		c = ambiguousConfidence
	}
	for _, m := range matches {
		if m.FileID == sourceFileID {
			return Result{SymbolID: m.ID, Confidence: c, Ambiguous: true}
		}
	}
	return Result{SymbolID: matches[0].ID, Confidence: c, Ambiguous: true}
}

// rewriteReceiver turns `self.x` / `Self::x` into
// `<parent><separator>x` so extractor-emitted receiver-qualified
// targets resolve against the enclosing class / impl type.
//
// The separator is inferred from the source symbol's own qualified
// name relative to its parent — Ruby instance methods use `#`, Ruby
// singletons + Python + Go + TS/JS use `.`, Rust uses `::`. This keeps
// the resolver language-agnostic: it never needs to know which
// language emitted the edge.
//
// If the source has no parent (top-level function, or parent lookup
// failed), the target is returned unchanged and exact-match
// resolution will drop the edge.
func rewriteReceiver(target, sourceQualified, parentQualified string) string {
	if parentQualified == "" {
		return target
	}
	var suffix string
	switch {
	case strings.HasPrefix(target, "self."):
		suffix = target[len("self."):]
	case strings.HasPrefix(target, "Self::"):
		suffix = target[len("Self::"):]
	default:
		return target
	}
	sep := separator(sourceQualified, parentQualified)
	if sep == "" {
		return target
	}
	return parentQualified + sep + suffix
}

// separator returns the token between a symbol's parent qualified
// name and its own trailing segment: `.`, `#`, or `::`. Derived from
// the source's qualified name so the resolver doesn't need a
// language enum.
func separator(sourceQualified, parentQualified string) string {
	rest := strings.TrimPrefix(sourceQualified, parentQualified)
	switch {
	case strings.HasPrefix(rest, "::"):
		return "::"
	case strings.HasPrefix(rest, "#"):
		return "#"
	case strings.HasPrefix(rest, "."):
		return "."
	}
	return ""
}

// unqualifiedName returns the trailing segment of a qualified name
// using `.`, `#`, or `::` as separators. When no separator is
// present, the original string is returned (it's already bare).
func unqualifiedName(qualified string) string {
	best := -1
	bestSep := ""
	for _, s := range []string{"::", "#", "."} {
		if i := strings.LastIndex(qualified, s); i > best {
			best = i
			bestSep = s
		}
	}
	if best < 0 {
		return qualified
	}
	return qualified[best+len(bestSep):]
}
