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
// sense.graph response
// ---------------------------------------------------------------

// GraphResponse is the shape of the sense.graph tool's reply and the
// `sense graph --json` CLI output. Freshness is a pointer so emitters
// that do not compute it (the CLI in 01-04) omit the block entirely;
// the MCP server in 01-05 always populates it.
type GraphResponse struct {
	Symbol       GraphSymbol  `json:"symbol"`
	Edges        GraphEdges   `json:"edges"`
	SenseMetrics GraphMetrics `json:"sense_metrics"`
	Freshness    *Freshness   `json:"freshness,omitempty"`
	NextSteps    []NextStep   `json:"next_steps"`
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
}

// GraphEdges groups the subject's relationships by kind. Every kind
// the documented schema recognises is represented here; emitters may
// leave unrequested kinds as empty slices and callers must treat
// `[]` as "none found" rather than "not provided."
type GraphEdges struct {
	Calls    []CallEdgeRef    `json:"calls"`
	CalledBy []CallEdgeRef    `json:"called_by"`
	Inherits []InheritEdgeRef `json:"inherits"`
	Composes []ComposeEdgeRef `json:"composes"`
	Includes []IncludeEdgeRef `json:"includes"`
	Imports  []ImportEdgeRef  `json:"imports"`
	Tests    []TestEdgeRef    `json:"tests"`
}

// CallEdgeRef is the shape of a calls / called_by edge entry. File
// is a pointer because an external call target (stdlib, third-party)
// may have no indexed file — the documented example includes
// `"file": null` for exactly that case.
type CallEdgeRef struct {
	Symbol     string     `json:"symbol"`
	File       *string    `json:"file"`
	Confidence Confidence `json:"confidence"`
}

// InheritEdgeRef is the shape of an inherits edge entry. The
// documented schema omits confidence for inheritance (always
// syntactically explicit, so there is no probability to report);
// leaving the field off keeps the on-wire shape honest.
type InheritEdgeRef struct {
	Symbol string  `json:"symbol"`
	File   *string `json:"file"`
}

// ComposeEdgeRef is the shape of a composes edge entry (has_many,
// belongs_to, has_one in Ruby; field composition in other languages).
// Like InheritEdgeRef, confidence is omitted — associations are
// syntactically explicit.
type ComposeEdgeRef struct {
	Symbol string  `json:"symbol"`
	File   *string `json:"file"`
}

// IncludeEdgeRef is the shape of an includes edge entry (Ruby
// `include SoftDeletable`, mixin inclusion). Like InheritEdgeRef,
// confidence is omitted — includes are syntactically explicit.
type IncludeEdgeRef struct {
	Symbol string  `json:"symbol"`
	File   *string `json:"file"`
}

// ImportEdgeRef is the shape of an imports edge entry (JS/TS
// `import { foo } from './bar'`, Python `from x import y`).
// Confidence is omitted — imports are syntactically explicit.
type ImportEdgeRef struct {
	Symbol string  `json:"symbol"`
	File   *string `json:"file"`
}

// TestEdgeRef points at a test file rather than a symbol: the
// granularity the documented schema settled on is file-level (tests
// target the symbol, the symbol points at the file that tests it).
type TestEdgeRef struct {
	File       string     `json:"file"`
	Confidence Confidence `json:"confidence"`
}

// GraphMetrics is the observability footer on a graph response.
type GraphMetrics struct {
	SymbolsReturned           int `json:"symbols_returned"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}

// ---------------------------------------------------------------
// sense.blast response
// ---------------------------------------------------------------

// BlastResponse is the shape of the sense.blast tool's reply and the
// `sense blast --json` CLI output. Symbol is the qualified-name
// string (not a struct like GraphSymbol), mirroring the documented
// example — blast callers index into affected symbols by name, not
// by line range. Freshness follows the same CLI-omits / MCP-populates
// convention as GraphResponse.
type BlastResponse struct {
	Symbol          string          `json:"symbol"`
	Risk            string          `json:"risk"`
	RiskFactors     []string        `json:"risk_factors"`
	DirectCallers   []BlastCaller   `json:"direct_callers"`
	IndirectCallers []BlastIndirect `json:"indirect_callers"`
	AffectedTests   []string        `json:"affected_tests"`
	TotalAffected   int             `json:"total_affected"`
	SenseMetrics    BlastMetrics    `json:"sense_metrics"`
	Freshness       *Freshness      `json:"freshness,omitempty"`
	NextSteps       []NextStep      `json:"next_steps"`
}

// BlastCaller is the shape of a direct_callers entry.
type BlastCaller struct {
	Symbol string `json:"symbol"`
	File   string `json:"file"`
}

// BlastIndirect is the shape of an indirect_callers entry. Via names
// the predecessor on the BFS shortest path — the symbol "one hop
// closer" to the subject — so a consumer can render
// "X (via Y, hops=N)".
type BlastIndirect struct {
	Symbol string `json:"symbol"`
	Via    string `json:"via"`
	Hops   int    `json:"hops"`
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
// Freshness (shared) + sense.status response
// ---------------------------------------------------------------

// Freshness tells an agent whether the index it is querying still
// matches the working tree. All three fields are pointers so
// emitters can omit cells they did not compute — sense.graph and
// sense.blast populate only IndexAgeSeconds + StaleFilesSeen;
// sense.status populates all three plus `last_scan`. The pitch
// (01-05 rabbit holes) calls out that IndexAgeSeconds alone is
// misleading: "10 seconds since scan" looks fresh until a single
// edit bumps StaleFilesSeen to 1. Both fields together tell the
// whole story.
type Freshness struct {
	LastScan              *string `json:"last_scan,omitempty"`
	IndexAgeSeconds       *int64  `json:"index_age_seconds,omitempty"`
	StaleFilesSeen        *int    `json:"stale_files_seen,omitempty"`
	MaxFileMtimeSinceScan *string `json:"max_file_mtime_since_scan,omitempty"`
	Watching              *bool   `json:"watching,omitempty"`
	WatchSince            *string `json:"watch_since,omitempty"`
}

// StatusResponse is the shape of the sense.status tool's reply (and
// the future `sense status --json` output). Unlike graph/blast the
// sense.status schema has no `sense_metrics` footer — status is
// metadata about the index itself, not the result of a query against
type StatusResponse struct {
	Index             StatusIndex               `json:"index"`
	Languages         map[string]StatusLanguage `json:"languages"`
	Freshness         Freshness                 `json:"freshness"`
	EmbeddingProgress *EmbeddingProgress        `json:"embedding_progress,omitempty"`
	Session           *StatusSession            `json:"session,omitempty"`
	Lifetime          *StatusLifetime           `json:"lifetime,omitempty"`
	Version           *StatusVersion            `json:"version,omitempty"`
	NextSteps         []NextStep                `json:"next_steps"`
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
}

// StatusVersion reports schema and embedding-model version state.
// Created and managed by pitch 04-04; this pitch reads whatever is
// available and displays current/mismatch status.
type StatusVersion struct {
	Binary               string `json:"binary"`
	Schema               int    `json:"schema"`
	SchemaCurrent        bool   `json:"schema_current"`
	EmbeddingModel       string `json:"embedding_model"`
	EmbeddingModelCurrent bool  `json:"embedding_model_current"`
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
// sense.search response
// ---------------------------------------------------------------

// SearchResponse is the shape of the sense.search tool's reply and the
// `sense search --json` CLI output. Matches the documented example in
// .doc/definition/06-mcp-and-cli.md exactly.
type SearchResponse struct {
	Results      []SearchResultEntry `json:"results"`
	SearchMode   string              `json:"search_mode"`
	SenseMetrics SearchMetrics       `json:"sense_metrics"`
	NextSteps    []NextStep          `json:"next_steps"`
}

// SearchResultEntry is a single search hit in the wire response.
type SearchResultEntry struct {
	Symbol  string      `json:"symbol"`
	File    string      `json:"file"`
	Line    int         `json:"line"`
	Kind    string      `json:"kind"`
	Score   SearchScore `json:"score"`
	Snippet string      `json:"snippet"`
}

// SearchScore is a fused relevance score. It renders with two decimal
// places on the wire so JSON consumers see `0.03` instead of
// `0.032786885245901636`. The documented examples show two-decimal
// scores (`0.92`, `0.87`).
type SearchScore float64

func (s SearchScore) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatFloat(float64(s), 'f', 2, 64)), nil
}

// SearchMetrics is the observability footer on a search response.
type SearchMetrics struct {
	SymbolsSearched           int `json:"symbols_searched"`
	EstimatedFileReadsAvoided int `json:"estimated_file_reads_avoided"`
	EstimatedTokensSaved      int `json:"estimated_tokens_saved"`
}
