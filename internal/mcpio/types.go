// Package mcpio is the shared marshalling layer between the CLI
// (pitch 01-04) and the MCP server (pitch 01-05). The JSON shapes
// here are the contract documented in
// .doc/definition/06-mcp-and-cli.md.
//
// Three invariants make the contract stable:
//   - Field declaration order matches the documented examples —
//     encoding/json emits in struct-declaration order, so re-ordering
//     is a schema break.
//   - Slice fields never carry `omitempty`. The Marshal* functions
//     normalize nil slices to `[]` so the wire always distinguishes
//     "Sense looked, found nothing" from "this emitter forgot a
//     field."
//   - Confidence fields always render with at least one decimal place
//     (`1.0`, not `1`) so consumers in languages that distinguish int
//     from float at the type level (Python, Ruby) always see a float.
//     The Confidence type owns that guarantee via its MarshalJSON.
package mcpio

import (
	"math"
	"strconv"
)

// AvgTokensPerFile is the conservative estimate for tokens in a typical
// source file. Used across all estimation formulas.
const AvgTokensPerFile = 800

// MaxNextSteps caps follow-up hints per response — transcript evidence shows agents follow at most 1-2.
const MaxNextSteps = 2

// ServerInstructions is the canonical MCP server instruction text.
// Used by both the MCP server (serverInstructions) and setup (.mcp.json).
const ServerInstructions = "When Sense is available and indexed, prefer Sense tools over grep, glob, " +
	"and file-walking agents for structural and semantic code questions. " +
	"Sense provides pre-indexed results that are faster and more complete.\n\n" +
	"WHEN TO USE SENSE TOOLS:\n" +
	"- Symbol relationships, callers, dependencies → sense_graph\n" +
	"- \"What would break if I changed X?\", impact analysis → sense_blast\n" +
	"- Conceptual/semantic code search (not exact string match) → sense_search\n" +
	"- Project patterns and conventions → sense_conventions\n" +
	"- Index health, what's indexed → sense_status\n\n" +
	"WORKFLOWS:\n" +
	"- Orientation (new to the codebase?) → sense_search with broad concepts + sense_conventions\n" +
	"- Impact analysis (changing something?) → sense_blast\n" +
	"- Dependency tracing (who calls what?) → sense_graph\n" +
	"- Debugging (where does X happen?) → sense_search\n" +
	"- Refactoring (what patterns exist?) → sense_conventions + sense_graph\n\n" +
	"WHEN NOT TO USE SENSE TOOLS:\n" +
	"- Exact text/string search → use grep\n" +
	"- Reading file contents → use your file reading tool\n" +
	"- Editing code → Sense is read-only"

// Confidence is a 0.0-1.0 edge-probability value. It exists as a
// named type, not a bare float64, solely to pin the wire form: a
// whole-number value (1.0, 0.0) must render with one decimal place,
// not as an integer literal. Untyped float literals in callers
// (`Confidence: 1.0`) auto-convert, so the named type is a zero-cost
// migration from plain float64.
type Confidence float64

// MarshalJSON emits `f.N` form — whole numbers render as `1.0` /
// `0.0`, fractional values keep their minimum-length form (`0.9`
// stays `0.9`, `0.92` stays `0.92`). The precision=-1 branch defers
// to strconv's "shortest round-trippable" representation; the
// precision=1 branch is only hit for integer-valued floats where
// that default would produce `1`.
func (c Confidence) MarshalJSON() ([]byte, error) {
	f := float64(c)
	if math.Trunc(f) == f {
		return []byte(strconv.FormatFloat(f, 'f', 1, 64)), nil
	}
	return []byte(strconv.FormatFloat(f, 'f', -1, 64)), nil
}

// ---------------------------------------------------------------
// Next-step hints (pitch 11-06)
// ---------------------------------------------------------------

// NextStep is a follow-up action hint appended to every MCP
// response. Tool is the MCP tool name, Args are pre-filled
// arguments the agent can pass directly, and Reason is a
// one-sentence explanation of why this is the logical next call.
type NextStep struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Reason string         `json:"reason"`
}

// ---------------------------------------------------------------
// sense_graph response
// ---------------------------------------------------------------

// GraphResponse is the shape of the sense_graph tool's reply and the
// `sense graph --json` CLI output. Freshness is a pointer so emitters
// that do not compute it (the CLI in 01-04) omit the block entirely;
// the MCP server in 01-05 always populates it.
type GraphResponse struct {
	Symbol            GraphSymbol           `json:"symbol"`
	Edges             GraphEdges            `json:"edges"`
	DispatchInferred  []DispatchInferredRef `json:"dispatch_inferred,omitempty"`
	Layers            []GraphLayer          `json:"layers,omitempty"`
	Truncated         bool                  `json:"truncated,omitempty"`
	SnippetsTruncated bool                  `json:"snippets_truncated,omitempty"`
	TestCallerSummary *TestCallerSummary    `json:"test_caller_summary,omitempty"`
	// LowConfidenceHidden counts usage edges dropped below graphConfidenceFloor
	// for the root symbol only (not deeper layers), so a consumer knows the
	// edge list was filtered rather than silently truncated.
	LowConfidenceHidden int `json:"low_confidence_hidden,omitempty"`
	// OmittedEdges counts edges dropped to keep the response within its
	// token budget (distinct from low_confidence_hidden, which is a
	// confidence filter). Non-zero means the edge lists are a partial,
	// highest-signal view — narrow with a direction or a specific symbol.
	OmittedEdges int    `json:"omitted_edges,omitempty"`
	CoverageNote string `json:"coverage_note,omitempty"`
	VerifyHint   string `json:"verify_hint,omitempty"`
	IndexCaveat  string `json:"index_caveat,omitempty"`
	// Completeness is the consolidated stop/verify verdict.
	Completeness *Completeness `json:"completeness,omitempty"`
	// ViewEdges is the per-subject view-reachability signal: "present" when a
	// view template reaches this symbol, "none" when view-dispatch is a live
	// question for it but no view edge exists, "" (omitted) otherwise. See
	// viewedges.go for the full contract.
	ViewEdges    string       `json:"view_edges,omitempty"`
	SenseMetrics GraphMetrics `json:"-"`
	Freshness    *Freshness   `json:"freshness,omitempty"`
	NextSteps    []NextStep   `json:"next_steps"`
}

// DispatchInferredRef is a caller discovered through interface dispatch —
// the caller invokes a method on a type connected via inherits edges,
// not the queried symbol directly.
type DispatchInferredRef struct {
	Symbol     string     `json:"symbol"`
	File       *string    `json:"file"`
	LineStart  int        `json:"line_start,omitempty"`
	LineEnd    int        `json:"line_end,omitempty"`
	Ref        string     `json:"ref,omitempty"`
	Via        string     `json:"via"`
	Confidence Confidence `json:"confidence"`
}

// GraphLayer holds edges discovered at one BFS hop beyond the root.
// Depth is the hop number (2, 3, …). Edges use the same shape as the
// root's edges so the LLM can process them uniformly.
type GraphLayer struct {
	Depth int        `json:"depth"`
	Edges GraphEdges `json:"edges"`
}

// TestCallerSummary segments test callers out of the main called_by
// list so the LLM focuses on production callers. When test callers
// exceed 20, Examples holds 3 representative file paths instead of
// the full list. Present only when there are test callers; nil
// otherwise.
type TestCallerSummary struct {
	Count    int      `json:"count"`
	Examples []string `json:"examples"`
}

// GraphSymbol is the focal symbol's identity block. File is always
// set (an indexed symbol always has a file) so it is a plain string,
// not a pointer — unlike edge endpoints, where external targets like
// `Beacon.track` may have no known file.
type GraphSymbol struct {
	Name      string `json:"name"`
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	Kind      string `json:"kind"`
	Ref       string `json:"ref"`
}

// GraphEdges groups the subject's relationships by kind. Every kind
// the documented schema recognises is represented here; emitters may
// leave unrequested kinds as empty slices and callers must treat
// `[]` as "none found" rather than "not provided."
type GraphEdges struct {
	Calls    []CallEdgeRef    `json:"calls"`
	CalledBy []CallEdgeRef    `json:"called_by"`
	Inherits []InheritEdgeRef `json:"inherits"`
	// InheritedBy is the reverse-inheritance direction: the symbols that extend
	// THIS one (subclasses, trait/interface implementors). Kept distinct from
	// Inherits for the same reason Composes/ComposedBy are split — "what I
	// extend" (the contract I must satisfy) vs "what extends me" (my change-impact
	// radius) are different questions, and merging them printed a supertype and a
	// subtype in one undirected list where "AnthropicConfig inherits DatabricksConfig"
	// read backwards (Databricks is the child). inherits/inherited_by are a
	// directional pair (like calls/called_by), so a callers-direction query
	// returns inbound inheritors here, NOT in Inherits.
	InheritedBy []InheritEdgeRef `json:"inherited_by"`
	Composes    []ComposeEdgeRef `json:"composes"`
	// ComposedBy is the reverse-composition direction: the symbols that hold a
	// has-a relationship TO this one (a Django model's ForeignKey / OneToOne /
	// ManyToMany dependents). Kept distinct from Composes — "what I own" vs "what
	// owns me" are different questions, and merging them hid the reverse fan-out
	// a change-impact audit needs. Note: composes/composed_by are a directional
	// pair (like calls/called_by), so a callers-direction query now returns
	// inbound composers here, NOT in Composes; includes/imports stay
	// merged into one bucket per kind.
	ComposedBy []ComposeEdgeRef  `json:"composed_by"`
	Includes   []IncludeEdgeRef  `json:"includes"`
	Imports    []ImportEdgeRef   `json:"imports"`
	Tests      []TestEdgeRef     `json:"tests"`
	Temporal   []TemporalEdgeRef `json:"temporal"`
}

// CallEdgeRef is the shape of a calls / called_by edge entry. File
// is a pointer because an external call target (stdlib, third-party)
// may have no indexed file — the documented example includes
// `"file": null` for exactly that case.
type CallEdgeRef struct {
	// ID is the target symbol's id, kept off the wire (json:"-"). It rides
	// through segmentation and the budget trim so the handler can mark the
	// FINAL rendered called_by set seen — a later sense_blast then collapses
	// only callers the model actually received, never ones the budget dropped.
	ID           int64      `json:"-"`
	Symbol       string     `json:"symbol"`
	File         *string    `json:"file"`
	LineStart    int        `json:"line_start,omitempty"`
	LineEnd      int        `json:"line_end,omitempty"`
	Ref          string     `json:"ref,omitempty"`
	Confidence   Confidence `json:"confidence"`
	CallSite     *CallSite  `json:"call_site,omitempty"`
	AlsoCalledBy []string   `json:"also_called_by,omitempty"`
}

// InheritEdgeRef is the shape of an inherits edge entry. The
// documented schema omits confidence for inheritance (always
// syntactically explicit, so there is no probability to report);
// leaving the field off keeps the on-wire shape honest.
type InheritEdgeRef struct {
	Symbol    string  `json:"symbol"`
	File      *string `json:"file"`
	LineStart int     `json:"line_start,omitempty"`
	LineEnd   int     `json:"line_end,omitempty"`
	Ref       string  `json:"ref,omitempty"`
}

// ComposeEdgeRef is the shape of a composes edge entry (has_many,
// belongs_to, has_one in Ruby; field composition in other languages).
// Like InheritEdgeRef, confidence is omitted — associations are
// syntactically explicit.
type ComposeEdgeRef struct {
	Symbol    string  `json:"symbol"`
	File      *string `json:"file"`
	LineStart int     `json:"line_start,omitempty"`
	LineEnd   int     `json:"line_end,omitempty"`
	Ref       string  `json:"ref,omitempty"`
}

// IncludeEdgeRef is the shape of an includes edge entry (Ruby
// `include SoftDeletable`, mixin inclusion). Like InheritEdgeRef,
// confidence is omitted — includes are syntactically explicit.
type IncludeEdgeRef struct {
	Symbol    string  `json:"symbol"`
	File      *string `json:"file"`
	LineStart int     `json:"line_start,omitempty"`
	LineEnd   int     `json:"line_end,omitempty"`
	Ref       string  `json:"ref,omitempty"`
}

// ImportEdgeRef is the shape of an imports edge entry (JS/TS
// `import { foo } from './bar'`, Python `from x import y`).
// Confidence is omitted — imports are syntactically explicit.
type ImportEdgeRef struct {
	Symbol    string  `json:"symbol"`
	File      *string `json:"file"`
	LineStart int     `json:"line_start,omitempty"`
	LineEnd   int     `json:"line_end,omitempty"`
	Ref       string  `json:"ref,omitempty"`
}

// TestEdgeRef points at a test file rather than a symbol: the
// granularity the documented schema settled on is file-level (tests
// target the symbol, the symbol points at the file that tests it).
type TestEdgeRef struct {
	File       string     `json:"file"`
	Confidence Confidence `json:"confidence"`
}

// TemporalEdgeRef is the shape of a temporal coupling edge entry.
// Co-change data is derived from git history; strength is the
// normalized co-change frequency (co_changes / max(changes_A, changes_B)).
type TemporalEdgeRef struct {
	Symbol    string     `json:"symbol"`
	File      *string    `json:"file"`
	LineStart int        `json:"line_start,omitempty"`
	LineEnd   int        `json:"line_end,omitempty"`
	Ref       string     `json:"ref,omitempty"`
	CoChanges int        `json:"co_changes"`
	Strength  Confidence `json:"strength"`
}

// GraphMetrics is the observability footer on a graph response.
type GraphMetrics struct {
	SymbolsReturned           int `json:"symbols_returned"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// ---------------------------------------------------------------
// sense_blast response
// ---------------------------------------------------------------

// BlastResponse is the shape of the sense_blast tool's reply and the
// `sense blast --json` CLI output. Symbol is the qualified-name
// string (not a struct like GraphSymbol), mirroring the documented
// example — blast callers index into affected symbols by name, not
// by line range. Freshness follows the same CLI-omits / MCP-populates
// convention as GraphResponse.
type BlastResponse struct {
	Symbol              string          `json:"symbol"`
	Risk                string          `json:"risk"`
	RiskFactors         []string        `json:"risk_factors"`
	DirectCallers       []BlastCaller   `json:"direct_callers"`
	IndirectCallers     []BlastIndirect `json:"indirect_callers"`
	AffectedTests       []string        `json:"affected_tests"`
	AffectedSymbols     int             `json:"affected_symbols"`
	AffectedFiles       int             `json:"affected_files"`
	GraphEdgesTraversed int             `json:"graph_edges_traversed"`
	TotalAffected       int             `json:"total_affected"`

	// DirectCallersByArea groups EVERY direct caller by its file's
	// directory, e.g. {"app/models": 40, "app/jobs": 18}. It is computed
	// from the full caller set before direct_callers is capped, so it
	// reports the true magnitude and structural shape (which subsystems
	// depend on the subject) even when direct_callers enumerates only the
	// top slice. The values sum to the true direct-caller count; pair it
	// with total_affected for the full radius.
	DirectCallersByArea map[string]int `json:"direct_callers_by_area,omitempty"`

	AffectedSubclasses     []BlastCaller `json:"affected_subclasses"`
	AffectedViaComposition []BlastCaller `json:"affected_via_composition"`
	AffectedViaIncludes    []BlastCaller `json:"affected_via_includes"`

	// Tier 2 — references (composes/inherits/includes). Count + top examples.
	References BlastTierSummary `json:"references"`
	// Tier 3 — affected test count (detail omitted to keep response focused).
	TestsAffectedCount int `json:"tests_affected_count"`

	Note               string `json:"note,omitempty"`
	ProductionAffected int    `json:"production_affected"`
	TestAffected       int    `json:"test_affected"`
	Truncated          bool   `json:"truncated,omitempty"`
	SnippetsTruncated  bool   `json:"snippets_truncated,omitempty"`
	CoverageNote       string `json:"coverage_note,omitempty"`

	VerifyHint  string `json:"verify_hint,omitempty"`
	IndexCaveat string `json:"index_caveat,omitempty"`
	// Completeness is the consolidated stop/verify verdict.
	Completeness *Completeness `json:"completeness,omitempty"`
	// ViewEdges is the per-subject view-reachability signal: "present" when a
	// view template reaches this symbol, "none" when view-dispatch is a live
	// question for it but no view edge exists, "" (omitted) otherwise. See
	// viewedges.go for the full contract.
	ViewEdges string `json:"view_edges,omitempty"`
	// SeenVia summarises the direct callers collapsed out of the enumerated
	// list because an earlier sense_graph/sense_blast call already returned
	// them to this session. It is a token-saving deduplication, not a
	// truncation: the data is already in the agent's context, so the magnitude
	// fields (total_affected, direct_callers_by_area) and the completeness
	// verdict are unaffected. Present only when at least one caller was
	// collapsed.
	SeenVia      *BlastSeenSummary `json:"seen_elsewhere,omitempty"`
	SenseMetrics BlastMetrics      `json:"-"`
	Freshness    *Freshness        `json:"freshness,omitempty"`
	NextSteps    []NextStep        `json:"next_steps"`
}

// BlastSeenSummary collapses direct callers already returned earlier this
// session into a single count + note, instead of re-enumerating them. Count
// is how many enumerated direct callers were omitted; Note explains why.
type BlastSeenSummary struct {
	Count int    `json:"count"`
	Note  string `json:"note"`
}

// BlastTierSummary holds a count plus a capped set of examples for
// lower-relevance tiers (Tier 2 references).
type BlastTierSummary struct {
	Count    int           `json:"count"`
	Examples []BlastCaller `json:"examples"`
}

// BlastCaller is the shape of a direct_callers entry.
type BlastCaller struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
	// Relation is a one-line "how this reaches the subject" phrase
	// (calls / inherits / includes / composes <subject>), so the agent
	// knows the edge kind per entry without re-opening the file. The kind
	// is already known from which bucket the entry came from; surfacing it
	// inline is what lets the agent act without a follow-up read.
	Relation    string    `json:"relation,omitempty"`
	LineStart   int       `json:"line_start,omitempty"`
	LineEnd     int       `json:"line_end,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	ViaTemporal bool      `json:"via_temporal,omitempty"`
	CallSite    *CallSite `json:"call_site,omitempty"`
}

// Completeness is a single, machine-branchable verdict on whether the
// returned set is the full *statically resolvable* dependent set, so the
// agent can decide to STOP searching vs verify specific names. "complete"
// deliberately covers ONLY resolvable static edges — dynamic-dispatch
// residual is reported separately in index_caveat and is NEVER folded into
// "complete", so the verdict can't over-claim into a duck-typed gap.
//
// It is the DERIVED top-level branch point, computed from the granular
// signals that still ship alongside it (low_confidence_hidden, omitted_edges,
// truncated for graph; total_affected and the tier cap for blast). Those
// remain because they carry detail the verdict collapses (which floor, how
// many, why); Completeness is the one field a consumer should branch on.
// Resolved is "items enumerated in this response": direct+indirect callers
// for blast, edges returned for graph.
type Completeness struct {
	Verdict  string `json:"verdict"`          // "complete" | "partial"
	Resolved int    `json:"resolved"`         // edges enumerated in this response
	Hidden   int    `json:"hidden,omitempty"` // affected symbols not enumerated (budget/cap)
	Advice   string `json:"advice"`
}

// BlastIndirect is the shape of an indirect_callers entry. Via names
// the predecessor on the BFS shortest path — the symbol "one hop
// closer" to the subject — so a consumer can render
// "X (via Y, hops=N)".
type BlastIndirect struct {
	Symbol      string `json:"symbol"`
	Via         string `json:"via"`
	Hops        int    `json:"hops"`
	LineStart   int    `json:"line_start,omitempty"`
	LineEnd     int    `json:"line_end,omitempty"`
	Ref         string `json:"ref,omitempty"`
	ViaTemporal bool   `json:"via_temporal,omitempty"`
}

// BlastMetrics mirrors GraphMetrics' footer shape but with the
// blast-specific counter name: symbols_traversed counts the BFS
// frontier expansions, not just the returned set.
type BlastMetrics struct {
	SymbolsTraversed          int `json:"symbols_traversed"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// ---------------------------------------------------------------
// Freshness (shared) + sense_status response
// ---------------------------------------------------------------

// Freshness tells an agent whether the index it is querying still
// matches the working tree. All three fields are pointers so
// emitters can omit cells they did not compute — sense_graph and
// sense_blast populate only IndexAgeSeconds + StaleFilesSeen;
// sense_status populates all three plus `last_scan`. The pitch
// (01-05 rabbit holes) calls out that IndexAgeSeconds alone is
// misleading: "10 seconds since scan" looks fresh until a single
// edit bumps StaleFilesSeen to 1. Both fields together tell the
// whole story.
type Freshness struct {
	LastScan              *string `json:"last_scan,omitempty"`
	IndexAgeSeconds       *int64  `json:"index_age_seconds,omitempty"`
	LastUpdate            *string `json:"last_update,omitempty"`
	IndexUpdateAgeSeconds *int64  `json:"index_update_age_seconds,omitempty"`
	StaleFilesSeen        *int    `json:"stale_files_seen,omitempty"`
	MaxFileMtimeSinceScan *string `json:"max_file_mtime_since_scan,omitempty"`
	Watching              *bool   `json:"watching,omitempty"`
	WatchSince            *string `json:"watch_since,omitempty"`
	// Pending is the number of symbols seen by the watcher but not yet
	// embedded (embedding debt). Non-nil only while a watcher is active.
	Pending *int `json:"pending,omitempty"`
}

// StatusResponse is the shape of the sense_status tool's reply (and
// the future `sense status --json` output). Unlike graph/blast the
// sense_status schema has no `sense_metrics` footer — status is
// metadata about the index itself, not the result of a query against
type StatusResponse struct {
	Index             StatusIndex               `json:"index"`
	Languages         map[string]StatusLanguage `json:"languages"`
	Structure         *StatusStructure          `json:"structure,omitempty"`
	Profile           *StatusProfile            `json:"profile,omitempty"`
	Freshness         Freshness                 `json:"freshness"`
	EmbeddingProgress *EmbeddingProgress        `json:"embedding_progress,omitempty"`
	Session           *StatusSession            `json:"session,omitempty"`
	Lifetime          *StatusLifetime           `json:"lifetime,omitempty"`
	Version           *StatusVersion            `json:"version,omitempty"`
	NextSteps         []NextStep                `json:"next_steps"`
}

type StatusProfile struct {
	Tier            string `json:"tier"`
	Symbols         int    `json:"symbols"`
	PrimaryLanguage string `json:"primary_language"`
	DynamicLanguage bool   `json:"dynamic_language"`
	Description     string `json:"description,omitempty"`
}

// StatusStructure provides a project-level structural summary for
// orientation. Computed fresh from the index on each status call.
type StatusStructure struct {
	TopNamespaces []StatusNamespace  `json:"top_namespaces"`
	HubSymbols    []StatusHub        `json:"hub_symbols"`
	KeySymbols    []KeySymbolEntry   `json:"key_symbols,omitempty"`
	EntryPoints   []StatusEntryPoint `json:"entry_points"`
	Frameworks    []string           `json:"frameworks,omitempty"`
	Fingerprint   string             `json:"fingerprint"`
}

type StatusNamespace struct {
	Name    string `json:"name"`
	Symbols int    `json:"symbols"`
	Kind    string `json:"kind"`
}

type StatusHub struct {
	Name    string `json:"name"`
	Callers int    `json:"callers"`
	Kind    string `json:"kind"`
	Role    string `json:"role,omitempty"`
}

type StatusEntryPoint struct {
	Name string `json:"name"`
	File string `json:"file"`
	Kind string `json:"kind"`
}

// EmbeddingProgress reports background embedding state. Present only
// when there is embedding debt (symbols without embeddings). Omitted
// when fully indexed.
type EmbeddingProgress struct {
	Total    int `json:"total"`
	Embedded int `json:"embedded"`
	Percent  int `json:"percent"`
}

// StatusSession holds in-memory session-scoped savings counters.
type StatusSession struct {
	Queries                   int             `json:"queries"`
	EstimatedFileReadsAvoided int             `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int             `json:"estimated_tokens_saved"`
	TextFallbackFired         int             `json:"text_fallback_fired"`
	TopQuery                  *StatusTopQuery `json:"top_query,omitempty"`
}

// StatusTopQuery is the single highest-saving query this session.
type StatusTopQuery struct {
	Tool                 string `json:"tool"`
	Args                 string `json:"args"`
	EstimatedTokensSaved int    `json:"estimated_tokens_saved"`
}

// StatusLifetime holds all-time savings counters (persisted in SQLite).
type StatusLifetime struct {
	Queries                   int `json:"queries"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
	TextFallbackFired         int `json:"text_fallback_fired"`
}

// StatusVersion reports schema and embedding-model version state.
// Created and managed by pitch 04-04; this pitch reads whatever is
// available and displays current/mismatch status.
type StatusVersion struct {
	Binary                string `json:"binary"`
	Schema                int    `json:"schema"`
	SchemaCurrent         bool   `json:"schema_current"`
	EmbeddingModel        string `json:"embedding_model"`
	EmbeddingModelCurrent bool   `json:"embedding_model_current"`
}

// StatusIndex reports index-level counts and size. Path is the
// relative path to the index file; SizeBytes is its on-disk size.
// Coverage is embeddings/symbols as a fraction (0.0–1.0).
type StatusIndex struct {
	Path       string  `json:"path"`
	SizeBytes  int64   `json:"size_bytes"`
	Files      int     `json:"files"`
	Symbols    int     `json:"symbols"`
	Edges      int     `json:"edges"`
	Embeddings int     `json:"embeddings"`
	Coverage   float64 `json:"coverage"`
}

// StatusLanguage is the per-language breakdown. Tier mirrors the
// three-tier vocabulary in 05-languages.md ("full", "standard",
// "basic"); unrecognised languages report "basic" so the field is
// always present.
type StatusLanguage struct {
	Files   int    `json:"files"`
	Symbols int    `json:"symbols"`
	Tier    string `json:"tier"`
}

// ---------------------------------------------------------------
// sense_search response
// ---------------------------------------------------------------

// SearchResponse is the shape of the sense_search tool's reply and the
// `sense search --json` CLI output. Matches the documented example in
// .doc/definition/06-mcp-and-cli.md exactly.
type SearchResponse struct {
	Results       []SearchResultEntry `json:"results"`
	SearchMode    string              `json:"search_mode"`
	FusionWeights FusionWeights       `json:"fusion_weights"`
	SenseMetrics  SearchMetrics       `json:"-"`
	NextSteps     []NextStep          `json:"next_steps"`
}

// FusionWeights reports the keyword/vector weight pair used for
// reciprocal rank fusion. Present in every search response for
// diagnostics — helps debug why results changed across queries.
type FusionWeights struct {
	Keyword float64 `json:"keyword"`
	Vector  float64 `json:"vector"`
}

// SearchResultEntry is a single search hit in the wire response.
//
// Source reports which retrieval path surfaced the hit, so a consumer
// can distinguish a keyword match from a semantic one. It is one of:
// "keyword" (FTS5 leg only), "vector" (semantic leg only),
// "hybrid" (both legs), "graph" (injected by graph enrichment — search
// did not match it directly), or "text" (substring text-fallback path).
type SearchResultEntry struct {
	Symbol     string      `json:"symbol"`
	File       string      `json:"file"`
	Line       int         `json:"line"`
	Kind       string      `json:"kind"`
	Score      SearchScore `json:"score"`
	Snippet    string      `json:"snippet"`
	References int         `json:"references,omitempty"`
	Source     string      `json:"source"`
	Seen       bool        `json:"seen,omitempty"`
}

// SearchScore is a relative relevance score. Scores are normalized
// then boosted by graph centrality, so values may exceed 1.0 for
// hub symbols with many callers. Renders with two decimal places
// on the wire (`0.92`, `1.17`).
type SearchScore float64

func (s SearchScore) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(s), 'f', 2, 64)), nil
}

// SearchMetrics is the observability footer on a search response.
type SearchMetrics struct {
	SymbolsSearched           int  `json:"symbols_searched"`
	EstimatedFileReadsAvoided int  `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int  `json:"estimated_tokens_saved"`
	TextFallbackFired         bool `json:"text_fallback_fired"`
}

// ---------------------------------------------------------------
// Dead code response (sense_graph dead_code mode)
// ---------------------------------------------------------------

// DeadCodeMetrics is the observability footer for dead code analysis.
type DeadCodeMetrics struct {
	SymbolsAnalyzed           int `json:"symbols_analyzed"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// ---------------------------------------------------------------
// Unreferenced-symbols response (honest-verdict contract)
// ---------------------------------------------------------------
//
// This is the shape the dead-code honest-verdicts rebuild emits (pitch
// 25-13). It replaces the flat dead_symbols list with the *fact* — symbols
// with zero indexed references — split into the rare, earned `dead` (safe to
// remove now) and the default-majority `possibly_dead` (a hidden caller could
// exist), grouped by reason. The internal open/closed-world vocabulary never
// appears: the agent gets a verdict, a reason, and a check, not a taxonomy.

// UnreferencedResponse is the top-level shape returned by sense_graph
// dead_code mode and `sense dead`.
type UnreferencedResponse struct {
	Unreferenced      UnreferencedSymbols `json:"unreferenced_symbols"`
	TotalSymbols      int                 `json:"total_symbols"`
	DeadCount         int                 `json:"dead_count"`
	PossiblyDeadCount int                 `json:"possibly_dead_count"`
	CoverageNote      string              `json:"coverage_note,omitempty"`
	SenseMetrics      DeadCodeMetrics     `json:"-"`
	NextSteps         []NextStep          `json:"next_steps"`
}

// UnreferencedSymbols is the two-tier body: the earned `dead` list first
// (never truncated), then `possibly_dead` grouped by reason and ranked by
// removability.
type UnreferencedSymbols struct {
	Dead         []DeadEntry         `json:"dead"`
	PossiblyDead []PossiblyDeadGroup `json:"possibly_dead"`
}

// DeadEntry is one symbol the engine proved safe to remove. It is
// self-contained: it carries its own per-symbol verify grep (call-scoped,
// definition line excluded) so a surviving hit is a genuinely missed call.
type DeadEntry struct {
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
	Verify    string `json:"verify"`
}

// PossiblyDeadGroup is a set of symbols sharing one open-world reason. The
// group carries the reason (stable code + imperative hint) and a single
// verify recipe for the whole group; Dropped reports how many symbols of
// this group were cut by --limit, so truncation is never silent.
type PossiblyDeadGroup struct {
	Reason  ReasonInfo           `json:"reason"`
	Verify  string               `json:"verify"`
	Dropped int                  `json:"dropped,omitempty"`
	Symbols []PossiblyDeadSymbol `json:"symbols"`
}

// ReasonInfo is the wire view of an open-world reason: a stable enum code the
// agent can switch on, plus an imperative one-line hint.
type ReasonInfo struct {
	Code string `json:"code"`
	Hint string `json:"hint"`
}

// PossiblyDeadSymbol is one unreferenced symbol within a reason group.
type PossiblyDeadSymbol struct {
	Qualified string `json:"qualified"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Kind      string `json:"kind"`
}
