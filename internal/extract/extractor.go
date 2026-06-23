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

// ERB is Full, not Basic: it has a dedicated extractor doing cross-language
// Stimulus/Turbo resolution AND parses the embedded Ruby of every <% %> tag
// with the Ruby grammar (calls, chains, blocks, render-collection, form model,
// and the view → route-helper → controller chain) — strictly more than the
// Standard langspec languages.
var languageTiers = map[string]Tier{
	"ruby":       TierFull,
	"go":         TierFull,
	"typescript": TierFull,
	"javascript": TierFull,
	"erb":        TierFull,
	"python":     TierStandard,
	"java":       TierStandard,
	"rust":       TierStandard,
	"c":          TierStandard,
	"cpp":        TierStandard,
	"csharp":     TierStandard,
	"kotlin":     TierStandard,
	"php":        TierStandard,
	"scala":      TierStandard,
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
	// ConfidenceUnresolved is for edges where the extractor could not
	// determine a receiver type and fell back to the bare method name.
	// The resolver may still match it via unqualified-name fallback, but
	// the edge is intentionally low-confidence to avoid surfacing noisy
	// cross-class guesses in blast-radius and caller queries.
	ConfidenceUnresolved = 0.5

	// ConfidenceNameCollision marks an edge whose target resolved only by
	// bare-name fallback among multiple same-named symbols (no receiver type
	// to disambiguate). Deliberately below blast's traversal floor (0.5) so
	// impact analysis ignores the guess, while it still counts as a weak
	// incoming edge for dead-code liveness.
	ConfidenceNameCollision = 0.3
)

// Receiver kinds for EmittedSymbol.Receiver / Symbol.Receiver. They record
// a method's dispatch kind for languages that distinguish them (Ruby
// instance `Class#m` vs singleton `Class.m`). Empty means "no distinction"
// — non-methods and languages that don't carry it. The resolver uses these
// to keep an instance call from binding to a same-named singleton method.
const (
	ReceiverInstance  = "instance"
	ReceiverSingleton = "singleton"
)

// Synthetic qualified-name prefixes for cross-language resolution.
// Edges targeting these names connect symbols across language boundaries
// (e.g., ERB template → JS controller via Turbo channel name matching).
const (
	PrefixTurboChannel = "turbo-channel:"
	PrefixTurboFrame   = "turbo-frame:"
	PrefixImportmap    = "importmap:"
	// PrefixPartial qualifies a Rails view partial by its render path
	// (e.g. "partial:users/profile"). The partial file emits a symbol with
	// this prefix; `render "users/profile"` emits a matching calls edge.
	PrefixPartial = "partial:"
	// PrefixI18n qualifies a translation key referenced from a view
	// (e.g. "i18n:users.show.title"). Emitted as a symbol so semantic search
	// can surface the view that renders a given piece of copy.
	PrefixI18n = "i18n:"
	// PrefixRoute qualifies a synthetic Rails route-helper symbol (e.g.
	// "route:orders_path", "route:edit_order_url"). The route DSL emits one per
	// generated path/url helper, with an edge to the controller action it
	// routes to; a `*_path`/`*_url` reference in a view emits an edge to the
	// prefixed name, so View → route:helper → Controller#action is a connected
	// chain. The reserved prefix guarantees the view edge can never collide
	// with a same-named application method (e.g. a model's own `foo_path`).
	// These symbols are plumbing — filtered out of dead-code and search output.
	PrefixRoute = "route:"
	// PrefixRubyCore qualifies a synthetic stand-in for a Ruby core class
	// that is never defined in any indexed source file (Struct, Data). A
	// `CONST = Struct.new(...)` value object emits an `inherits` edge to the
	// matching synthetic symbol so dead-code analysis can recognise the
	// value object structurally rather than by a fragile name suffix. The
	// synthetic stand-in exists only because sense_edges.target_id is NOT
	// NULL: an `inherits → Struct` edge whose target resolves to nothing is
	// dropped at write time, so the target must be a real emitted row. These
	// symbols are plumbing — filtered out of dead-code and search output.
	PrefixRubyCore = "ruby-core:"
)

// syntheticPrefixes is the canonical set of reserved qualified-name prefixes for
// the synthetic symbols the extractors emit for cross-language and framework
// resolution (turbo channels/frames, importmap entries, view partials, i18n
// keys, route helpers, ruby-core shims). They are plumbing, not project
// declarations, so query-side consumers (search, dead-code, conventions, the
// resolver's cross-language gate) exclude or special-case them. It is the single
// source of truth, kept unexported and reached only through IsSyntheticQualified
// so no caller can mutate the set.
var syntheticPrefixes = []string{
	PrefixTurboChannel,
	PrefixTurboFrame,
	PrefixImportmap,
	PrefixPartial,
	PrefixI18n,
	PrefixRoute,
	PrefixRubyCore,
}

// IsSyntheticQualified reports whether a fully-qualified name belongs to a
// synthetic symbol (one carrying a reserved synthetic prefix).
func IsSyntheticQualified(qualified string) bool {
	for _, p := range syntheticPrefixes {
		if strings.HasPrefix(qualified, p) {
			return true
		}
	}
	return false
}

// RubyCoreStruct and RubyCoreData are the qualified names of the synthetic
// base symbols a Ruby value object inherits from. They live here (not in the
// ruby package) so the dead package can key its value-object query on the
// exact same strings the extractor emits, without importing the extractor.
const (
	RubyCoreStruct = PrefixRubyCore + "Struct"
	RubyCoreData   = PrefixRubyCore + "Data"
)

// StimulusControllerQualified converts a kebab-case Stimulus controller name
// to its qualified form for cross-language resolution. Both the ERB extractor
// (emitting edge targets) and the TS/JS extractor (emitting symbols) must
// produce the same qualified name for resolution to succeed.
//
// Namespace separators (--) become :: separators:
//
//	"checkout"       → "CheckoutController"
//	"user-profile"   → "UserProfileController"
//	"admin--users"   → "Admin::UsersController"
func StimulusControllerQualified(name string) string {
	parts := strings.Split(name, "--")
	for i, part := range parts {
		parts[i] = kebabToPascal(part)
	}
	last := len(parts) - 1
	parts[last] += "Controller"
	return strings.Join(parts, "::")
}

func kebabToPascal(s string) string {
	words := strings.Split(s, "-")
	var b strings.Builder
	for _, w := range words {
		if w == "" {
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]))
		b.WriteString(w[1:])
	}
	return b.String()
}

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
	Name       string
	Qualified  string
	Kind       model.SymbolKind
	Visibility string
	// Receiver is a method's dispatch kind ("instance" / "singleton") for
	// languages that distinguish them; empty otherwise. Persisted to
	// sense_symbols.receiver and used by the resolver to keep instance and
	// singleton methods of the same name from cross-binding.
	Receiver        string
	ParentQualified string
	LineStart       int
	LineEnd         int
	Snippet         string
	// Docstring is the doc comment attached to this symbol per language
	// convention (godoc, RDoc, JSDoc, PEP 257, /// & /** */). Empty when
	// no comment is attached or when the candidate is filtered (license
	// header, magic comment, blank-line gap).
	Docstring string
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

// DispatchEmitter is an optional Emitter extension for streaming the literal
// names a file reflectively dispatches on (the symbol/string arguments to
// send/public_send/__send__/define_method/respond_to?/method/const_get and
// the receiver of constantize). An extractor that detects such names probes
// for this interface with a type assertion; an Emitter that does not
// implement it simply receives no dispatch names. The names feed a
// project-global set in sense_meta so the dead-code arbiter can keep a
// reflectively-reachable symbol open-world instead of falsely calling it
// dead. Returning an error aborts extraction like the core Emitter methods.
type DispatchEmitter interface {
	DispatchName(name string) error
}

// CgoExportEmitter is an optional Emitter extension for streaming the names a
// Go file marks with a cgo `//export <name>` directive. Such a function is
// called from C and has no Go caller edge, so it would otherwise look dead. The
// names feed a project-wide set in sense_meta so the dead-code Go voice keeps a
// cgo-exported symbol open-world (reason go_cgo) instead of falsely calling it
// dead. Distinct from DispatchEmitter because its verify recipe differs — the
// caller lives in C, not in a reflective string literal. An extractor probes for
// this interface with a type assertion; an Emitter that does not implement it
// simply receives no cgo names. Returning an error aborts extraction.
type CgoExportEmitter interface {
	CgoExportName(name string) error
}

// RustHarvestEmitter is an optional Emitter extension for streaming the Rust
// attribute facts the dead-code Rust voice reads — names whose reachability the
// edge graph cannot see because the caller lives outside indexed Rust:
//
//   - RustExportName: a function marked `#[no_mangle]` / `#[export_name = …]`
//     (called across the FFI boundary from C) or a `#[no_mangle]` / `#[used]`
//     static (kept alive by the linker). No Rust caller edge exists, so the voice
//     keeps it open-world (rust_ffi for a function, rust_used for a static).
//   - RustTestSymbol: an item marked `#[test]` / `#[bench]` (including scoped
//     `#[tokio::test]`) or nested under a `#[cfg(test)]` module. The test harness
//     invokes it, never an indexed caller, and `cargo build` does not even compile
//     it, so the voice keeps it open-world (rust_test).
//   - RustTraitImplMethod: a method defined in an `impl Trait for Type` block. It
//     satisfies a trait and is reached through a trait object or generic bound,
//     so the voice keeps it open-world (rust_trait_impl). This is the sound,
//     name-independent signal that covers external traits (serde, std::io) the
//     magic table cannot enumerate.
//   - RustAllowDeadName: an item annotated `#[allow(dead_code)]` / `#[allow(unused)]`.
//     The author deliberately suppressed the lint, so rustc never warns it and it
//     is absent from the cargo oracle; the voice keeps it open-world
//     (rust_allow_dead).
//
// The name sets feed flat (not per-language) sense_meta keys — these concepts are
// Rust-only, like cgo is Go-only — that the Rust voice reads. An extractor probes
// for this interface with a type assertion; an Emitter that does not implement it
// simply receives no names. Returning an error aborts extraction.
type RustHarvestEmitter interface {
	RustExportName(name string) error
	RustTestSymbol(name string) error
	RustTraitImplMethod(name string) error
	RustAllowDeadName(name string) error
}

// MentionEmitter is an optional Emitter extension for streaming every bare
// name a file *mentions* — every identifier or symbol-literal token that
// appears in any position other than a definition's own name. Unlike
// DispatchEmitter (a small, targeted reflection set), this is the broad set:
// the project-global union feeds the dead-code arbiter's soundness gate, which
// earns `dead` for a symbol only when its name is mentioned nowhere it could be
// an unresolved caller. This makes `dead` robust to resolver incompleteness —
// a live-but-unbindable call (an inherited bare call, a `**splat`, a chain
// receiver, a `validate :sym` symbol arg) still leaves a textual mention, so
// the symbol stays open-world instead of being falsely called dead. An
// extractor probes for this interface with a type assertion; an Emitter that
// does not implement it simply receives no mention names.
type MentionEmitter interface {
	MentionName(name string) error
}

// TSHarvestEmitter is an optional Emitter extension for streaming the TS/JS
// dead-code facts the TypeScript voice reads — names whose reachability the edge
// graph cannot see because a framework or the module system reaches them:
//
//   - TSDecoratedName: a class or method carrying a decorator (`@Component`,
//     `@Injectable`, `@Controller`, a route-method decorator). Angular/Nest's
//     DI/router instantiates or routes to it with no source caller, so the voice
//     keeps it open-world (ts_decorator) even when module-private.
//   - TSDefaultExportName: the name bound by an `export default` form. A default
//     export is imported by path rather than by name, so the voice raises the more
//     specific ts_default_export instead of the generic ts_exported.
//
// The name sets feed flat (not per-language) sense_meta keys — these concepts span
// the .ts/.tsx/.js family, which shares one extractor. An extractor probes for this
// interface with a type assertion; an Emitter that does not implement it simply
// receives no names. Returning an error aborts extraction.
type TSHarvestEmitter interface {
	TSDecoratedName(name string) error
	TSDefaultExportName(name string) error
}

// PythonHarvestEmitter is an optional Emitter extension for streaming the Python
// dead-code facts the Python voice reads — names whose reachability the edge
// graph cannot see because a framework, a decorator, or a declared public API
// reaches them:
//
//   - PythonDecoratedName: a function/method/class carrying any decorator
//     (`@property`, `@staticmethod`, `@pytest.fixture`, `@click.command`, …).
//     The decorator changes the call story (an attribute access, an injected
//     fixture, a CLI entry), so the voice keeps it open-world (py_decorator).
//   - PythonRouteName: a handler carrying a route decorator (Flask `@app.route`,
//     FastAPI `@app.get`/`@router.post`/`@app.websocket`). The framework's router
//     dispatches it with no source caller (py_route) — the more specific reason.
//   - PythonDjangoName: a symbol carrying a Django-dispatch decorator (a
//     `@receiver` signal handler, an `@admin.register`). Django's signal/admin
//     machinery invokes it invisibly (py_django).
//   - PythonAllExportName: a name listed in a module's `__all__`. It is declared
//     public API — re-exported by `from mod import *` — so the voice keeps it
//     open-world (py_all_export) even when it is underscore-private (the one case
//     where the underscore convention is overridden, which the broad mention set
//     does NOT catch because `__all__` lists names as string literals).
//
// The name sets feed flat (not per-language) sense_meta keys — these concepts are
// Python-only. An extractor probes for this interface with a type assertion; an
// Emitter that does not implement it simply receives no names. Returning an error
// aborts extraction.
type PythonHarvestEmitter interface {
	PythonDecoratedName(name string) error
	PythonRouteName(name string) error
	PythonDjangoName(name string) error
	PythonAllExportName(name string) error
}

// LangspecHarvestEmitter is an optional Emitter extension for streaming the
// dead-code fact the table-driven langspec extractor produces for the
// Standard-tier languages (Java, Kotlin, C#, Scala, C++, PHP, C):
//
//   - LangspecAnnotatedName: the name of a class/method/function carrying any
//     annotation or attribute (Java `@Service`/`@Test`, C# `[Fact]`/`[HttpGet]`,
//     Kotlin/Scala annotations, PHP `#[Route]`). These languages have no
//     per-framework voice, so any annotated symbol may be dispatched by a DI
//     container, a test runner, or a router with no source caller; the langspec
//     voice keeps such a name open-world (ls_annotated).
//
// The name set feeds a flat (not per-language) sense_meta key — annotations span
// the shared langspec extractor, like decorators span the .ts/.tsx/.js family.
// Cross-language name overlap is the safe direction (it only ever raises a hand).
// An extractor probes for this interface with a type assertion; an Emitter that
// does not implement it simply receives no names. Returning an error aborts
// extraction.
type LangspecHarvestEmitter interface {
	LangspecAnnotatedName(name string) error
}

// MentionHarvester marks an Extractor whose Extract streams the broad mention
// set (via MentionEmitter) for every file it processes. The scan records such a
// language as harvested even on a scan that yields zero mentions for it, so the
// dead-code soundness gate can tell "harvested, nothing mentioned" (which may
// still earn `dead`) apart from "this language never harvested" (which must NOT
// — it would be `dead` off another language's mentions). An extractor opts in by
// implementing HarvestsMentions; returning false is the same as not
// implementing it. This is a STATIC capability, independent of how many names a
// given scan happens to produce, which is exactly why the two facts can differ.
type MentionHarvester interface {
	HarvestsMentions() bool
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

// RawExtractor is an optional interface for extractors that operate on source
// bytes directly without tree-sitter parsing (e.g., regex-based extraction
// for template files like ERB). When an Extractor also implements RawExtractor,
// the scan harness calls ExtractRaw and skips tree-sitter parsing entirely.
// Grammar() should return nil for raw extractors.
//
// Callers that invoke extractors directly (outside the scan harness) should
// type-assert for RawExtractor before calling Extract, since Grammar() returns
// nil and tree-sitter parsing will fail.
type RawExtractor interface {
	ExtractRaw(source []byte, filePath string, emit Emitter) error
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
