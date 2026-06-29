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
	// fileLang maps a file id to its language, so the unqualified fallback can
	// look up the source edge's language from req.SourceFileID without threading
	// it through every Request. Built from the same SymbolRefs as the name maps.
	fileLang map[int64]string
	// fileIsTest maps a file id to whether its path is a test file, so the
	// resolver can keep a production-source calls/references edge from binding to
	// a coincidental same-named symbol that lives in a test file. Built from the
	// same SymbolRefs; a file id absent from the map is treated as non-test.
	fileIsTest map[int64]bool
	// fileModelModule flags files that are framework model-definition modules,
	// populated by language-specific classifiers (see isDjangoModelModuleRef in
	// django.go). It lets a `composes` edge prefer a model-definition target over
	// a same-named non-model symbol; see preferDjangoModelComposes. A file id
	// absent from the map is treated as not a model module.
	fileModelModule map[int64]bool
	// ancestry maps a class's qualified name to its direct superclass qualified
	// names (as written on the `inherits` edge). It powers inherited-method
	// resolution: a call to `Sub#m` with no own `Sub#m` resolves to the nearest
	// `Ancestor#m` up the class chain. Empty (the default from NewIndex) makes
	// that step a no-op; WithInheritance populates it from the scan's pending
	// inherits edges.
	ancestry map[string][]string
}

// NewIndex builds an Index from the bulk SymbolRefs output. The input
// slice is expected to be ordered by ascending id; order is
// preserved in each map bucket.
func NewIndex(refs []model.SymbolRef) *Index {
	ix := &Index{
		byQualified:     make(map[string][]model.SymbolRef, len(refs)),
		byName:          make(map[string][]model.SymbolRef, len(refs)),
		fileLang:        make(map[int64]string),
		fileIsTest:      make(map[int64]bool),
		fileModelModule: make(map[int64]bool),
	}
	for _, r := range refs {
		ix.byQualified[r.Qualified] = append(ix.byQualified[r.Qualified], r)
		name := unqualifiedName(r.Qualified)
		ix.byName[name] = append(ix.byName[name], r)
		if r.Language != "" {
			ix.fileLang[r.FileID] = r.Language
		}
		if r.Path != "" {
			ix.fileIsTest[r.FileID] = isTestPath(r.Path)
		}
		if isDjangoModelModuleRef(r) {
			ix.fileModelModule[r.FileID] = true
		}
	}
	return ix
}

// WithInheritance attaches a class-ancestry map (child qualified name → direct
// superclass qualified names, as written on `inherits` edges) and returns the
// Index for chaining. It enables inherited-method resolution; passing nil or
// omitting the call leaves that step a no-op. Built by the scan harness from
// the pending inherits edges before the resolve pass.
func (ix *Index) WithInheritance(ancestry map[string][]string) *Index {
	ix.ancestry = ancestry
	return ix
}

// maxAncestryDepth bounds the superclass walk in inherited-method resolution.
// Real Ruby class chains are shallow (mastodon's deepest worker chain is
// StatusUpdate < Distribution < RawDistribution, depth 2); the cap is a
// cycle/runaway guard, not a real limit.
const maxAncestryDepth = 16

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

// nameCollisionConfidence is applied to a bare-name fallback that had to
// pick among multiple same-named symbols. It sits below blast's traversal
// floor so impact analysis ignores these guesses.
const nameCollisionConfidence = extract.ConfidenceNameCollision

// Resolve looks up req.Target and returns the best candidate. Returns
// ok=false when nothing matches — callers (scan) drop unresolved
// edges today; Card 8 may wire the unresolved set to diagnostic
// output.
//
// Algorithm:
//
//  1. Apply receiver rewrite (`self.` / `Self::` ⇒ parent-prefixed)
//     when the source symbol has a parent qualified name.
//
//  2. Exact match via byQualified. Single hit ⇒ BaseConfidence.
//     Multiple ⇒ same-file preferred, else lowest-id; confidence
//     clamped to ambiguousConfidence. For calls/tests/references the
//     exact match is gated the same way the fallback is — a cross-language
//     or production-into-test coincidence is dropped (synthetic cross-language
//     targets like `partial:` are exempt). A bare target the extractor
//     emitted at ConfidenceUnresolved (receiver unknown) skips the exact
//     shortcut entirely: its byQualified hit would be a leaf coincidence, so
//     it goes straight to the gated fallback.
//
//     Inherited-method step (between 2 and 3; calls/tests/references only):
//     a `Sub#m` / `Sub.m` dispatch with no own `Sub#m` resolves to the
//     nearest `Ancestor#m` up the class `inherits` chain (resolveInherited).
//     Verified against the real chain, so it is preferred over the leaf
//     fallback, which would demote the same hit as a cross-scope guess.
//     No-op without an ancestry map.
//
//  3. For calls, tests, and references edges, fall back to
//     unqualified-name match via byName. Candidates in a different code
//     language than the source are dropped (filterByLanguage), test-file
//     candidates are dropped for production sources (filterByTestDirection),
//     same scope preference applies, confidence is clamped to
//     ambiguousConfidence. A qualified target that matched only its leaf
//     (the namespace or receiver type was discarded) is demoted below
//     blast's floor as an unverified cross-scope guess unless the
//     qualifier can be verified — see isUnverifiedCrossScope.
//
//  4. No match ⇒ ok=false.
func (ix *Index) Resolve(req Request) (Result, bool) {
	target := rewriteReceiver(req.Target, req.SourceQualified, req.SourceParentQualified)
	gatedKind := req.Kind == model.EdgeCalls || req.Kind == model.EdgeTests || req.Kind == model.EdgeReferences

	// Step 1-2: exact qualified-name match. A terminal answer here (a hit, or a
	// coincidence-only match dropped) short-circuits; otherwise fall through.
	if r, ok, done := ix.resolveQualified(target, req, gatedKind); done {
		return r, ok
	}

	// Step 2b: inherits lexical-scope resolution. A superclass written relative to
	// the subclass's namespace (`class Sub < Base` where the real base is
	// `Outer::Base`) is emitted as the bare written text and misses the exact
	// match; resolve it the way Ruby/Rust constant lookup does, by walking the
	// subclass's enclosing scopes outward. inherits is not a gated kind, so this
	// is its only resolution path past the exact match — and it repairs the
	// `inherits` edges the ancestry map (resolveInherited) is built from.
	if req.Kind == model.EdgeInherits {
		if r, ok := ix.resolveLexicalInherits(target, req); ok {
			return r, true
		}
	}

	// Step 2.5: inherited-method resolution (gated kinds only — method dispatch,
	// same as the leaf fallback below). A `Sub#m` / `Sub.m` dispatch with no own
	// `Sub#m` symbol resolves to the nearest `Ancestor#m` up the class chain —
	// the real method the call runs (a worker subclass reaching an inherited run
	// method, e.g. `AccountRawDistributionWorker#perform` ⇒
	// `RawDistributionWorker#perform`). Verified against the actual `inherits`
	// chain, so it is a real edge, not a same-name guess — preferred over the
	// leaf fallback below, which would demote it as cross-scope.
	if gatedKind {
		if r, ok := ix.resolveInherited(target, req); ok {
			return r, true
		}

		// Step 3: unqualified leaf fallback.
		if r, ok := ix.resolveByLeaf(target, req); ok {
			return r, true
		}
	}

	return Result{}, false
}

// resolveInherited resolves a method-dispatch target (`Sub#m` / `Sub.m`) whose
// exact qualified form missed, by walking Sub's class-ancestry chain and
// binding to the nearest ancestor that defines the method. This is real Ruby
// (and general single-inheritance) method lookup: the call dispatches to the
// inherited definition. It is intentionally narrow:
//
//   - Only `#` (instance) and `.` (singleton) dispatch — never `::` namespace
//     or a bare leaf, which carry no receiver type to inherit through.
//   - Only when the receiver type is a known class in the ancestry map; a
//     receiver with no recorded superclass (or a non-class) yields nothing,
//     so a call into a gem/stdlib type is never guessed at.
//   - The first ancestor (nearest in MRO order) that defines `<sep>m` wins;
//     candidates are language/test-gated exactly like the exact-match path, and
//     confidence follows pickBest (a unique ancestor keeps BaseConfidence — the
//     dispatch is as certain as the inherits chain).
//
// The walk is breadth-first with a `seen` cycle guard because the ancestry map
// is slice-valued: a class reopened across files with divergent superclass
// clauses (rare, but real Ruby) yields multiple parents, and a future ancestry
// source (module includes) would too. Single inheritance collapses it to a
// linear walk. maxAncestryDepth bounds a pathological chain; `seen` handles
// cycles (`class A < B` / `class B < A`).
//
// Known limits (a wrong edge here is bounded and accepted, never 1.0):
//   - Ancestor names are matched as written on the `inherits` edge, so a parent
//     referenced by a short name from inside a namespace
//     (`class Foo < Bar` where the real parent is `NS::Bar`) won't match
//     `byQualified` and the step no-ops — the same limitation the rest of the
//     resolver carries. Fully-qualified superclasses (mastodon's `< AP::X`)
//     resolve correctly.
//   - Module `prepend`/MRO is not modelled: only class `inherits` ancestry is
//     walked, so a method intercepted by a prepended module still resolves to
//     the superclass definition.
//
// Returns ok=false when no ancestor defines the method, leaving the leaf
// fallback to try a same-name match.
func (ix *Index) resolveInherited(target string, req Request) (Result, bool) {
	if len(ix.ancestry) == 0 {
		return Result{}, false
	}
	leaf, sep := unqualifiedNameSep(target)
	if sep != "#" && sep != "." {
		return Result{}, false
	}
	recvType := strings.TrimSuffix(target, sep+leaf)
	if recvType == "" || len(ix.ancestry[recvType]) == 0 {
		return Result{}, false
	}

	// Breadth-first walk up the superclass chain, nearest depth first. Matches
	// are collected across the whole depth-level before deciding, so two parents
	// at the same hop that both define the method resolve through pickBest as
	// ambiguous (clamped + flagged) rather than silently taking the first.
	seen := map[string]bool{recvType: true}
	frontier := ix.ancestry[recvType]
	for depth := 0; depth < maxAncestryDepth && len(frontier) > 0; depth++ {
		var matches []model.SymbolRef
		var next []string
		for _, anc := range frontier {
			if seen[anc] {
				continue
			}
			seen[anc] = true
			matches = append(matches, ix.byQualified[anc+sep+leaf]...)
			next = append(next, ix.ancestry[anc]...)
		}
		matches = filterByLanguage(matches, ix.fileLang[req.SourceFileID])
		matches = filterByTestDirection(matches, ix.fileIsTest[req.SourceFileID], ix.fileIsTest)
		if len(matches) > 0 {
			return pickBest(matches, req.SourceFileID, req.BaseConfidence), true
		}
		frontier = next
	}
	return Result{}, false
}

// resolveLexicalInherits resolves an `inherits` target that missed the exact
// match by walking the subclass's enclosing scopes outward, mirroring Ruby/Rust
// constant lookup. A superclass named relative to the subclass's namespace
// (`class Sub < Base` two modules deep, where the real base is `Outer::Base`) is
// emitted by the extractor as the bare written text "Base", so the exact
// byQualified lookup misses. Trying `<enclosing-scope><sep>Base` from the
// innermost enclosing scope outward binds the same symbol the language would —
// the innermost match wins (Ruby's rule), and a name that exists at no enclosing
// scope resolves to nothing rather than a fabricated base (a wrong base misleads
// blast worse than a gap).
//
// Scoped to inherits on purpose: gated kinds already have resolveByLeaf, and a
// general scope walk for calls/references would reintroduce exactly the
// cross-scope guessing isUnverifiedCrossScope exists to demote. The separator is
// derived from the source's own qualified name so the walk stays language-
// agnostic; when it cannot be determined (no enclosing scope, e.g. a top-level
// subclass) the step is a no-op and the edge stays unresolved.
func (ix *Index) resolveLexicalInherits(target string, req Request) (Result, bool) {
	sep := separator(req.SourceQualified, req.SourceParentQualified)
	if sep == "" {
		return Result{}, false
	}
	// An absolute reference (leading separator, e.g. Ruby's `::Foo`) forces
	// top-level lookup: try the bare form only, never prepend an enclosing scope.
	if strings.HasPrefix(target, sep) {
		bare := target[len(sep):]
		if m := ix.byQualified[bare]; len(m) > 0 {
			return pickBest(m, req.SourceFileID, req.BaseConfidence), true
		}
		return Result{}, false
	}
	// Walk the enclosing scopes outward, innermost first. The bare-target case
	// (the "" scope) was already tried by the exact match, so it is skipped here.
	for scope := req.SourceParentQualified; scope != ""; scope = trimLastSegment(scope, sep) {
		if m := ix.byQualified[scope+sep+target]; len(m) > 0 {
			return pickBest(m, req.SourceFileID, req.BaseConfidence), true
		}
	}
	return Result{}, false
}

// trimLastSegment drops the trailing `<sep>segment` of a qualified name, or
// returns "" when no separator remains — one step in walking enclosing scopes
// outward.
func trimLastSegment(qualified, sep string) string {
	if i := strings.LastIndex(qualified, sep); i >= 0 {
		return qualified[:i]
	}
	return ""
}

// resolveQualified attempts an exact byQualified match. The bool done reports
// whether the lookup reached a terminal decision: true with a hit, true with a
// coincidence-only drop ({}, false), or false meaning "no exact entry — let the
// leaf fallback handle it".
func (ix *Index) resolveQualified(target string, req Request, gatedKind bool) (Result, bool, bool) {
	// A bare target emitted at ConfidenceUnresolved means the extractor could
	// not type the receiver, so the name is a leaf, not a qualified name. Any
	// exact byQualified hit on it is a coincidence with a same-named symbol;
	// skip the shortcut and let the gated fallback handle (and demote) it.
	if _, targetSep := unqualifiedNameSep(target); targetSep == "" && req.BaseConfidence <= extract.ConfidenceUnresolved {
		return Result{}, false, false
	}

	matches := ix.byQualified[target]
	if len(matches) == 0 {
		return Result{}, false, false
	}
	if gatedKind && !isSyntheticTarget(target) {
		matches = filterByLanguage(matches, ix.fileLang[req.SourceFileID])
		matches = filterByTestDirection(matches, ix.fileIsTest[req.SourceFileID], ix.fileIsTest)
	}
	if len(matches) > 0 {
		matches = ix.preferDjangoModelComposes(matches, req)
		return pickBest(matches, req.SourceFileID, req.BaseConfidence), true, true
	}
	// The qualified name matched only cross-language or test-only symbols: a
	// coincidence, not a real edge. Drop it rather than leaf-fallback into more
	// coincidences.
	return Result{}, false, true
}

// resolveByLeaf is the unqualified fallback: find symbols whose trailing segment
// matches the target's trailing segment. Applies to bare targets ("say" ⇒
// byName["say"]) as well as dotted targets whose full qualified form missed
// ("mod.Sprintf" ⇒ byName["Sprintf"]). byQualified and byName are different
// indexes, so looking up the same key in both is not duplicate work.
func (ix *Index) resolveByLeaf(target string, req Request) (Result, bool) {
	name, sep := unqualifiedNameSep(target)
	if name == "" {
		return Result{}, false
	}
	// A target still carrying a `self.`/`Self::` prefix here means
	// rewriteReceiver had no parent to bind it to (e.g. a template file-level
	// call). Its trailing `.` is the sentinel separator, not a class-method
	// dispatch, so the dispatch kind is unknown and receiver filtering must not
	// narrow on it.
	if strings.HasPrefix(target, "self.") || strings.HasPrefix(target, "Self::") {
		sep = ""
	}
	matches := ix.byName[name]
	matches = filterByLanguage(matches, ix.fileLang[req.SourceFileID])
	matches = filterByTestDirection(matches, ix.fileIsTest[req.SourceFileID], ix.fileIsTest)
	matches, receiverContradicted := filterByReceiver(matches, sep)
	if len(matches) == 0 {
		return Result{}, false
	}

	r := pickBest(matches, req.SourceFileID, req.BaseConfidence)
	if r.Confidence > ambiguousConfidence {
		r.Confidence = ambiguousConfidence
	}
	if r.Ambiguous {
		// Bare-name fallback among multiple same-named symbols is the weakest
		// resolution — no receiver type disambiguates it. Drop it below blast's
		// floor so impact analysis ignores the guess; it still counts for
		// dead-code liveness.
		r.Confidence = nameCollisionConfidence
	}
	if r.Confidence > nameCollisionConfidence &&
		ix.isUnverifiedCrossScope(target, name, sep, req.SourceFileID, req.BaseConfidence, receiverContradicted) {
		// The leaf bound but the qualifier (namespace or receiver type) could
		// not be verified — a cross-scope guess such as
		// `Stripe::StripeError#message` landing on a same-named test method.
		// Demote below blast's floor even on a single match, so impact analysis
		// ignores it while it still counts for dead-code liveness. See
		// isUnverifiedCrossScope for the per-separator rule.
		r.Confidence = nameCollisionConfidence
	}
	return r, true
}

// isUnverifiedCrossScope reports whether a successful unqualified-fallback
// match is a cross-scope guess that resolution could not verify, and so must
// be demoted below blast's floor. The fallback only runs after exact lookup
// missed, so a qualified target (sep != "") here matched on its trailing leaf
// alone — the qualifier (namespace or receiver type) was discarded.
//
//   - Bare target (sep ""): no qualifier to verify. A bare call the extractor
//     emitted with confidence above ConfidenceUnresolved is trustworthy — a
//     Go/Python top-level function (base 1.0) resolves legitimately by name. A
//     bare call emitted *at* ConfidenceUnresolved means the extractor could not
//     determine the receiver type at all (Ruby's `x.m` with unknown `x`), so
//     binding the leaf to a coincidental same-named symbol is a guess.
//   - "::" namespace: the full path already missed exact lookup, so the
//     namespace cannot be confirmed to contain this leaf
//     (`Stripe::Checkout::Session` landing on a local `User::Session`). Always
//     a guess.
//   - "#"/"." receiver dispatch: verifiable only if the receiver type itself
//     is an indexed symbol — then an inherited or reopened method is plausible
//     (`Child#m` resolving to `Parent#m`). An external or unknown receiver type
//     (a gem class like `Stripe::StripeError`, a stdlib package) is a
//     coincidence, as is a dispatch kind that contradicted every candidate.
//
// View-language sources are exempt from the receiver-dispatch check: templates
// dispatch into helpers and controllers loosely by leaf name, mirroring
// filterByLanguage's view carve-out.
func (ix *Index) isUnverifiedCrossScope(target, leaf, sep string, srcFileID int64, baseConfidence float64, receiverContradicted bool) bool {
	switch sep {
	case "":
		return baseConfidence <= extract.ConfidenceUnresolved
	case "::":
		return true
	}
	if isViewLanguage(ix.fileLang[srcFileID]) {
		return false
	}
	if receiverContradicted {
		return true
	}
	prefix := strings.TrimSuffix(target, sep+leaf)
	return len(ix.byQualified[prefix]) == 0
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
	name, _ := unqualifiedNameSep(qualified)
	return name
}

// unqualifiedNameSep returns the trailing segment of a qualified name and
// the separator that precedes it (`.`, `#`, `::`, or "" when the name is
// already bare). The separator carries the call's dispatch kind for Ruby —
// `#` is an instance call, `.` is a class/singleton call — which
// filterByReceiver uses to disambiguate same-named candidates.
func unqualifiedNameSep(qualified string) (name, sep string) {
	best := -1
	bestSep := ""
	for _, s := range []string{"::", "#", "."} {
		if i := strings.LastIndex(qualified, s); i > best {
			best = i
			bestSep = s
		}
	}
	if best < 0 {
		return qualified, ""
	}
	return qualified[best+len(bestSep):], bestSep
}

// filterByReceiver narrows unqualified-fallback candidates to those whose
// dispatch kind matches the call separator, so an instance call (`X#m`)
// cannot bind to a same-named singleton method (`Y.m`) and vice-versa.
//
// It only acts when (a) the separator maps to a receiver kind (`#` ⇒
// instance, `.` ⇒ singleton) and (b) at least one candidate declares a
// receiver — i.e. a Ruby method carrying the distinction. Candidates with
// an empty receiver (non-methods, other languages, top-level defs) are
// always kept, so resolution for languages that don't populate receiver is
// unchanged. If filtering would remove every candidate, the original set is
// returned rather than dropping the edge — the dispatch hint is a tie-break,
// not a hard gate — and the second return value reports that contradiction so
// the caller can demote the result: an instance call that finds only singleton
// candidates (or vice-versa) is a kind-mismatched guess, not a confident edge.
func filterByReceiver(matches []model.SymbolRef, sep string) (kept []model.SymbolRef, contradicted bool) {
	want := receiverForSeparator(sep)
	if want == "" {
		return matches, false
	}
	declared := false
	for _, m := range matches {
		if m.Receiver != "" {
			declared = true
			break
		}
	}
	if !declared {
		return matches, false
	}
	kept = make([]model.SymbolRef, 0, len(matches))
	for _, m := range matches {
		if m.Receiver == "" || m.Receiver == want {
			kept = append(kept, m)
		}
	}
	if len(kept) == 0 {
		return matches, true
	}
	return kept, false
}

// receiverForSeparator maps a call separator to the dispatch kind it implies:
// `#` ⇒ instance, `.` ⇒ singleton/class. `::` (namespace) and "" (bare,
// receiver unknown) carry no dispatch hint.
//
// IMPORTANT: this encodes *Ruby* dispatch semantics, where `.` is a
// singleton/class call and `#` an instance call. It is safe for other
// languages today only because Ruby is the sole extractor that populates
// SymbolRef.Receiver — filterByReceiver no-ops when no candidate declares a
// receiver, so a Go/Python `pkg.fn` target (also separated by `.`) is never
// narrowed. If another language begins populating Receiver, it must share this
// `.`=singleton / `#`=instance convention, or filterByReceiver must be gated
// by language — otherwise a `.`-dispatched instance call in that language
// would be wrongly filtered against same-named singletons.
func receiverForSeparator(sep string) string {
	switch sep {
	case "#":
		return extract.ReceiverInstance
	case ".":
		return extract.ReceiverSingleton
	}
	return ""
}

// filterByLanguage drops unqualified-fallback candidates that belong to a
// different programming language than the source edge. A bare-name match
// between two distinct code languages (a Ruby `application` call binding to a
// JS Stimulus `application`) is a same-name coincidence, never a real call.
//
// Two carve-outs keep it from dropping legitimate edges:
//
//   - When the source is a view/template language (ERB, …) the gate is OFF.
//     Templates legitimately dispatch into Ruby helpers and JS controllers by
//     bare name through this very fallback, so their cross-language matches are
//     real. (Synthetic-prefix view edges resolve via exact byQualified and never
//     reach here; this carve-out covers the embedded-helper bare-name calls.)
//   - An unknown language on the source (empty) or on a candidate is kept:
//     without both languages the gate cannot prove a mismatch, so it stays a
//     no-op (older indexes and unit tests that don't carry language are
//     unaffected). The gate fails open: srcLang is "" when the source file
//     contributed no symbol to fileLang (e.g. a file with zero indexed
//     symbols), so a real edge is never silently dropped for lack of language.
//
// Unlike filterByReceiver, a language mismatch is a hard exclusion, not a
// tie-break: if every candidate is cross-language the result is empty and the
// edge drops to unresolved rather than binding to a coincidence. (filterBy
// receiver, by contrast, returns its input untouched when filtering would
// empty the set, because dispatch kind is a hint rather than a gate.)
func filterByLanguage(matches []model.SymbolRef, srcLang string) []model.SymbolRef {
	if srcLang == "" || isViewLanguage(srcLang) {
		return matches
	}
	// Fast path: the overwhelmingly common case is every candidate sharing the
	// source language, so scan first and return the input untouched rather than
	// allocating a copy on every fallback in the hot resolve loop.
	crossLang := false
	for _, m := range matches {
		if m.Language != "" && m.Language != srcLang {
			crossLang = true
			break
		}
	}
	if !crossLang {
		return matches
	}
	kept := make([]model.SymbolRef, 0, len(matches))
	for _, m := range matches {
		if m.Language == "" || m.Language == srcLang {
			kept = append(kept, m)
		}
	}
	return kept
}

// isViewLanguage reports whether a language is a view/template layer that
// legitimately dispatches into other languages by bare name (so the
// cross-language fallback gate must not apply to edges originating in it).
// ERB is the only such language today; add others here as they gain extractors.
func isViewLanguage(lang string) bool {
	return lang == "erb"
}

// filterByTestDirection drops candidates that live in a test file when the
// source is production code. Production code never has a static calls or
// references edge into a test file — test files define stubs, doubles, and
// helpers that shadow real (often framework or gem) names, so a same-named
// match there is a coincidence (a Ruby helper's `url_for` binding to a
// `FiltersHelperTest#url_for`, a `count` call binding to a JS spec's `count`).
//
// The gate is one-directional: when the source itself is a test file it is a
// no-op, because tests legitimately reference both production and test symbols.
// A candidate whose file test-ness is unknown (path absent) is kept, so the
// gate fails open. Like filterByLanguage and unlike filterByReceiver, this is a
// hard exclusion: if every candidate is test-only the set empties and the edge
// drops to unresolved rather than binding to a coincidence.
func filterByTestDirection(matches []model.SymbolRef, sourceIsTest bool, fileIsTest map[int64]bool) []model.SymbolRef {
	if sourceIsTest {
		return matches
	}
	// Fast path: most candidates are production, so scan first and avoid
	// allocating a copy unless a test candidate is actually present.
	hasTest := false
	for _, m := range matches {
		if fileIsTest[m.FileID] {
			hasTest = true
			break
		}
	}
	if !hasTest {
		return matches
	}
	kept := make([]model.SymbolRef, 0, len(matches))
	for _, m := range matches {
		if !fileIsTest[m.FileID] {
			kept = append(kept, m)
		}
	}
	return kept
}

// isTestPath reports whether a file path is a test/spec file. It mirrors
// mcpio.IsTestPath (kept as a small local copy to avoid a dependency from this
// low-level package on the presentation layer); the conventions it encodes —
// `test/`/`spec/` directories, `_test`/`_spec`/`.test.` infixes, a `test_`
// prefix, and a `Test`/`Tests` filename suffix — are stable across the
// supported languages.
//
// testPathInfixes and testPathPrefixes are the path conventions isTestPath
// recognizes as a whole-path substring or a leading prefix, respectively. Held
// as tables so the check is a pair of loops rather than a long `||` chain.
var (
	testPathInfixes  = []string{"_test.", ".test.", "_spec.", "/test/", "/tests/", "/testdata/", "/spec/"}
	testPathPrefixes = []string{"test/", "tests/", "spec/"}
)

func isTestPath(path string) bool {
	for _, infix := range testPathInfixes {
		if strings.Contains(path, infix) {
			return true
		}
	}
	for _, prefix := range testPathPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return isTestBaseName(path)
}

// isTestBaseName reports whether a path's filename follows a test naming
// convention not anchored to a directory: a `test_` prefix or a `Test`/`Tests`
// stem suffix (e.g. `FooTest.java`).
func isTestBaseName(path string) bool {
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

// syntheticTargetPrefixes are the qualified-name prefixes the extractors use
// for intentional cross-language and framework edges (view partials, Turbo
// channels/frames, importmap entries, i18n keys, route helpers, ruby-core
// shims). A target carrying one of these is a designed cross-language link, not
// a same-name coincidence, so the exact-path language gate must not touch it.
//
// isSyntheticTarget reports whether a target name is one of these synthetic
// qualified names. The prefix set is owned by the extract package (the single
// source of truth shared with search, dead-code, and conventions).
func isSyntheticTarget(target string) bool {
	return extract.IsSyntheticQualified(target)
}
