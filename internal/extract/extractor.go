// Package extract defines the extractor contract and registry that turn a
// parsed tree-sitter tree into Sense symbols and intra-file edges. Concrete
// language extractors live in sibling packages (internal/extract/ruby, …)
// and register themselves at init time.
//
// Extractors emit via callback rather than returning slices: the scan
// harness can stream rows into a per-file SQLite transaction without ever
// materialising a full per-file buffer. This is the same shape tree-sitter
// itself prefers for walking trees and matches the "writer batches inserts"
// note in the pitch.
package extract

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/model"
)

// Tier mirrors the language-support tiers from .doc/definition/05-languages.md.
type Tier string

const (
	TierBasic    Tier = "basic"
	TierStandard Tier = "standard"
	TierFull     Tier = "full"
)

var languageTiers = map[string]Tier{
	"ruby":       TierFull,
	"go":         TierFull,
	"typescript": TierFull,
	"javascript": TierFull,
	"python":     TierStandard,
	"java":       TierStandard,
	"rust":       TierStandard,
}

// LanguageTier returns the support tier for a language name.
func LanguageTier(lang string) string {
	if t, ok := languageTiers[lang]; ok {
		return string(t)
	}
	return string(TierBasic)
}

// Confidence constants — the policy numbers the pitch specifies for
// edge confidence. Centralising them here keeps the emit-side
// extractors and the resolve-side clamp aligned, and makes the pitch
// numbers discoverable by Go-docs lookup rather than grepped out of
// scattered literals.
//
// The four levels describe four distinct resolution paths:
//   - Static: the extractor saw a syntactic fact (a call_expression,
//     a superclass clause). No inference.
//   - Convention: match derived from a naming or framework
//     convention — reserved for Cycle 5 framework inference and
//     currently unused by any emit site.
//   - Ambiguous: the resolver picked among multiple candidates at
//     the same qualified-name key, or succeeded only via the
//     calls-edge unqualified-name fallback.
//   - Tests: a test-file-to-implementation match derived from
//     filename convention (checkout_service_test.rb ⇒
//     checkout_service.rb). Same numeric value as Ambiguous today
//     but semantically distinct — a change to the tests confidence
//     should not require changing the ambiguous clamp.
//   - Dynamic: the extractor resolved a literal argument passed to a
//     dynamic-dispatch callsite (Ruby send, Python getattr). Non-
//     literal dynamic dispatch is skipped entirely, not flagged
//     low-confidence.
const (
	ConfidenceStatic     = 1.0
	ConfidenceConvention = 0.9
	ConfidenceAmbiguous  = 0.8
	ConfidenceTests      = 0.8
	ConfidenceDynamic    = 0.7
)

// EmittedSymbol is the pre-insert form produced by an extractor. Index-
// assigned fields (ID, FileID, numeric ParentID) are resolved by the
// scan harness when rows are written; the extractor only sees its own
// source file.
//
// The `Emitted` prefix disambiguates this from model.Symbol, the
// stored form. They have overlapping fields but different identities:
// an EmittedSymbol knows its qualified-name parent, a model.Symbol
// knows its parent's numeric ID after resolution.
//
// ParentQualified is the qualified name of the enclosing symbol within
// the same file, or "" for a top-level symbol. The harness resolves it
// to a ParentID during write. Cross-file parents don't exist in
// Tier-Basic — a method's class always lives in the same file as the
// method.
type EmittedSymbol struct {
	Name            string
	Qualified       string
	Kind            model.SymbolKind
	Visibility      string
	ParentQualified string
	LineStart       int
	LineEnd         int
	Snippet         string
}

// EmittedEdge is the pre-insert form of an intra-file edge. Both
// endpoints are by qualified name; the scan harness maps them to
// symbol IDs within the same file. Edges that reference a name not
// defined in this file are dropped in Tier-Basic — 01-03 backfills
// cross-file resolution.
type EmittedEdge struct {
	SourceQualified string
	TargetQualified string
	Kind            model.EdgeKind
	Line            *int
	Confidence      float64
}

// Emitter receives streamed extraction output. Returning an error
// aborts the extraction; the harness bubbles it up and skips the file.
type Emitter interface {
	Symbol(EmittedSymbol) error
	Edge(EmittedEdge) error
}

// Extractor walks a parsed tree and emits symbols + edges for one language.
// Implementations are stateless — the same instance handles every file in
// that language. Any per-file state lives on the call stack.
//
// Parsing is deliberately *not* owned by the Extractor: the scan harness
// can reuse one sitter.Parser across files in the same language without
// each extractor needing parser-lifecycle code. Grammar() supplies the
// language binding the harness feeds into its parser.
type Extractor interface {
	Extract(tree *sitter.Tree, source []byte, filePath string, emit Emitter) error
	Grammar() *sitter.Language
	Language() string
	Extensions() []string // leading dot, lower-case: ".rb", ".ts"
	Tier() Tier
}

// ---------- registry ----------

var (
	registryMu sync.RWMutex
	byLang     = map[string]Extractor{}
	byExt      = map[string]Extractor{}
)

// Register adds an extractor to the process-wide registry. Each language's
// package calls this from an init() function, so the registry is fully
// populated before any scan runs.
//
// Duplicate registration is a misconfiguration — two packages both claiming
// "ruby" or both claiming ".ts" is never intended. Panic at init is the
// right loud failure; silently overwriting would mean "last import wins",
// which is a silent Heisenbug waiting to happen.
func Register(e Extractor) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if existing, ok := byLang[e.Language()]; ok {
		panic(fmt.Sprintf("extract: duplicate registration for language %q (existing: %T, new: %T)",
			e.Language(), existing, e))
	}
	byLang[e.Language()] = e
	for _, ext := range e.Extensions() {
		key := strings.ToLower(ext)
		if existing, ok := byExt[key]; ok {
			panic(fmt.Sprintf("extract: extension %q already claimed by %q, cannot register for %q",
				key, existing.Language(), e.Language()))
		}
		byExt[key] = e
	}
}

// ForExtension returns the extractor that handles the given file extension
// (leading dot, e.g. ".rb"). Nil means no extractor is registered — the
// scan harness treats the file as opaque and skips parsing.
func ForExtension(ext string) Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return byExt[strings.ToLower(ext)]
}

// ByLanguage returns the extractor registered under a language name, or
// nil if none. Primarily used by the fixture-test harness to look up an
// extractor by the subdirectory it lives in.
func ByLanguage(name string) Extractor {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return byLang[name]
}

// Languages returns every registered language name in sorted order. The
// deterministic order keeps fixture tests and status output stable across
// runs regardless of init() ordering.
func Languages() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(byLang))
	for n := range byLang {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ---- per-language helpers ----
// Kept here rather than in each language package: these are 2-line
// stateless helpers every extractor needs identically. Putting them in
// the interface package avoids cross-language copies drifting in
// small ways (off-by-one line numbering, nil-node panics, etc.).

// Text returns a node's source text, safe on nil nodes (returns ""),
// so extractors can write `Text(n.ChildByFieldName("name"), src)`
// without nil-guarding every field lookup.
func Text(n *sitter.Node, source []byte) string {
	if n == nil {
		return ""
	}
	return n.Utf8Text(source)
}

// Line converts a tree-sitter 0-indexed Point row into a 1-indexed
// line number, matching the sense_symbols.line_start / line_end
// convention.
func Line(p sitter.Point) int { return int(p.Row) + 1 }

// WalkNamedDescendants visits every named descendant of n whose Kind()
// equals kind and invokes visit on it. Traversal is depth-first; a
// matched node is both visited AND recursed through, so nested
// occurrences (`f(g())`, chained calls, matched nodes inside matched
// nodes) each produce a visit. A nil node is a no-op — callers don't
// need to guard.
//
// This is the plumbing behind every extractor's body-walking helper:
// after three language extractors (Go, Ruby, Python) landed the same
// recursion, it was extracted here so per-language code keeps only its
// own logic (what to emit when a call is found) and the shared
// traversal has one authoritative implementation.
//
// visit returning an error aborts the walk — the error is surfaced
// unchanged so extractors can bubble emitter failures out to the
// scan harness.
func WalkNamedDescendants(n *sitter.Node, kind string, visit func(*sitter.Node) error) error {
	if n == nil {
		return nil
	}
	count := n.NamedChildCount()
	for i := uint(0); i < count; i++ {
		child := n.NamedChild(i)
		if child == nil {
			continue
		}
		if child.Kind() == kind {
			if err := visit(child); err != nil {
				return err
			}
		}
		if err := WalkNamedDescendants(child, kind, visit); err != nil {
			return err
		}
	}
	return nil
}
