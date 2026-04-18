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
// Tier-Basic in this pitch means: symbols + intra-file edges only. Standard
// and Full tier values will be added when 05-0x promotes extractors — we
// don't define them ahead of any code that uses them.
type Tier string

const TierBasic Tier = "basic"

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
