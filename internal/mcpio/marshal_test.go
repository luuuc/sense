package mcpio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// TestMarshalGraphRoundTrip checks that MarshalGraph produces bytes
// that decode back to the same GraphResponse. The byte-level contract
// against the documented examples is card 3's job; this card only
// owns the types + encoder settings, so we verify the shapes survive
// a marshal/unmarshal cycle with the "tricky" fields exercised: a
// nullable File on an edge ref and an inherits edge (which lacks
// confidence).
func TestMarshalGraphRoundTrip(t *testing.T) {
	filePath := "app/services/payment_gateway.rb"
	in := GraphResponse{
		Symbol: GraphSymbol{
			Name:      "CheckoutService",
			Qualified: "App::Services::CheckoutService",
			File:      "app/services/checkout_service.rb",
			LineStart: 12,
			LineEnd:   85,
			Kind:      "class",
		},
		Edges: GraphEdges{
			Calls: []CallEdgeRef{
				{Symbol: "PaymentGateway#charge", File: &filePath, Confidence: 1.0},
				{Symbol: "Beacon.track", File: nil, Confidence: 0.9},
			},
			CalledBy:    []CallEdgeRef{},
			Inherits:    []InheritEdgeRef{{Symbol: "ApplicationService", File: nil}},
			InheritedBy: []InheritEdgeRef{},
			Composes:    []ComposeEdgeRef{},
			ComposedBy:  []ComposeEdgeRef{},
			Includes:    []IncludeEdgeRef{},
			Imports:     []ImportEdgeRef{},
			Tests:       []TestEdgeRef{{File: "test/services/checkout_service_test.rb", Confidence: 0.8}},
			Temporal:    []TemporalEdgeRef{},
		},
		NextSteps: []NextStep{},
	}

	raw, err := MarshalGraph(in)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}

	var out GraphResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v\nbytes:\n%s", err, raw)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch\nin:  %+v\nout: %+v", in, out)
	}
}

// TestMarshalBlastRoundTrip mirrors the graph test for BlastResponse.
func TestMarshalBlastRoundTrip(t *testing.T) {
	in := BlastResponse{
		Symbol:      "User#email_verified?",
		Risk:        "medium",
		RiskFactors: []string{"4 direct callers"},
		DirectCallers: []BlastCaller{
			{Symbol: "SessionsController#create", File: "app/controllers/sessions_controller.rb"},
		},
		IndirectCallers: []BlastIndirect{
			{Symbol: "OrdersController#new", Via: "SessionsController#create", Hops: 2},
		},
		AffectedTests:          []string{"test/models/user_test.rb"},
		TotalAffected:          2,
		AffectedSubclasses:     []BlastCaller{},
		AffectedViaComposition: []BlastCaller{},
		AffectedViaIncludes:    []BlastCaller{},
		References:             BlastTierSummary{Count: 0, Examples: []BlastCaller{}},
		NextSteps:              []NextStep{},
	}

	raw, err := MarshalBlast(in)
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	var out BlastResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v\nbytes:\n%s", err, raw)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch\nin:  %+v\nout: %+v", in, out)
	}
}

// TestMarshalZeroValueEmptySlices pins that a zero-value response
// marshals with every slice as `[]`, not `null`. This is the
// in-code version of the slice-nil policy the package comment
// declares — without this test, a future edit to remove a
// normalizer would break the contract silently.
//
// This test enforces that all named slice fields carry `[]`, not `null`.
func TestMarshalZeroValueEmptySlices(t *testing.T) {
	graphBytes, err := MarshalGraph(GraphResponse{})
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	for _, field := range []string{`"calls": []`, `"called_by": []`, `"inherits": []`, `"inherited_by": []`, `"composes": []`, `"composed_by": []`, `"includes": []`, `"imports": []`, `"tests": []`, `"temporal": []`, `"next_steps": []`} {
		if !strings.Contains(string(graphBytes), field) {
			t.Errorf("GraphResponse zero-value missing %s\ngot:\n%s", field, graphBytes)
		}
	}
	for _, nullField := range []string{`"calls": null`, `"called_by": null`, `"inherits": null`, `"inherited_by": null`, `"composes": null`, `"composed_by": null`, `"includes": null`, `"imports": null`, `"tests": null`, `"temporal": null`, `"next_steps": null`} {
		if strings.Contains(string(graphBytes), nullField) {
			t.Errorf("GraphResponse zero-value slice field should be []: %s\ngot:\n%s", nullField, graphBytes)
		}
	}

	blastBytes, err := MarshalBlast(BlastResponse{})
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	for _, field := range []string{`"risk_factors": []`, `"direct_callers": []`, `"indirect_callers": []`, `"affected_tests": []`, `"affected_subclasses": []`, `"affected_via_composition": []`, `"affected_via_includes": []`, `"next_steps": []`} {
		if !strings.Contains(string(blastBytes), field) {
			t.Errorf("BlastResponse zero-value missing %s\ngot:\n%s", field, blastBytes)
		}
	}
	for _, nullField := range []string{`"risk_factors": null`, `"direct_callers": null`, `"indirect_callers": null`, `"affected_tests": null`, `"affected_subclasses": null`, `"affected_via_composition": null`, `"affected_via_includes": null`, `"next_steps": null`} {
		if strings.Contains(string(blastBytes), nullField) {
			t.Errorf("BlastResponse zero-value slice field should be []: %s\ngot:\n%s", nullField, blastBytes)
		}
	}
}

func TestSearchResponseIncludesReferences(t *testing.T) {
	resp := SearchResponse{
		Results: []SearchResultEntry{
			{Symbol: "pkg.HandleRequest", File: "handler.go", Kind: "function", Score: 0.95, References: 50},
			{Symbol: "pkg.Helper", File: "helper.go", Kind: "function", Score: 0.8, References: 0},
		},
		SearchMode: "hybrid",
	}
	raw, err := MarshalSearch(resp)
	if err != nil {
		t.Fatalf("MarshalSearch: %v", err)
	}
	s := string(raw)

	if !strings.Contains(s, `"references": 50`) {
		t.Errorf("expected references: 50 in output:\n%s", s)
	}
	// omitempty: references should not appear for 0-value entries
	var parsed struct {
		Results []json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed.Results) < 2 {
		t.Fatalf("expected 2 results, got %d", len(parsed.Results))
	}
	if strings.Contains(string(parsed.Results[1]), `"references"`) {
		t.Errorf("references with value 0 should be omitted (omitempty):\n%s", parsed.Results[1])
	}
}

func TestResponseOmitsMetrics(t *testing.T) {
	graph := GraphResponse{
		Symbol:       GraphSymbol{Name: "X", Qualified: "X", File: "x.go", Kind: "function"},
		SenseMetrics: GraphMetrics{SymbolsReturned: 99, EstimatedFileReadsAvoided: 10, EstimatedTokensSaved: 8000},
	}
	graphBytes, err := MarshalGraph(graph)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	if strings.Contains(string(graphBytes), "sense_metrics") {
		t.Errorf("graph response should not contain sense_metrics:\n%s", graphBytes)
	}

	bl := BlastResponse{
		Symbol:       "Y",
		Risk:         "low",
		RiskFactors:  []string{"1 direct caller"},
		SenseMetrics: BlastMetrics{SymbolsTraversed: 50, EstimatedFileReadsAvoided: 5, EstimatedTokensSaved: 4000},
	}
	blastBytes, err := MarshalBlast(bl)
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	if strings.Contains(string(blastBytes), "sense_metrics") {
		t.Errorf("blast response should not contain sense_metrics:\n%s", blastBytes)
	}

	sr := SearchResponse{
		Results:      []SearchResultEntry{{Symbol: "Z", File: "z.go", Kind: "function", Score: 0.9}},
		SearchMode:   "hybrid",
		SenseMetrics: SearchMetrics{SymbolsSearched: 100, EstimatedFileReadsAvoided: 3, EstimatedTokensSaved: 2400},
	}
	searchBytes, err := MarshalSearch(sr)
	if err != nil {
		t.Fatalf("MarshalSearch: %v", err)
	}
	if strings.Contains(string(searchBytes), "sense_metrics") {
		t.Errorf("search response should not contain sense_metrics:\n%s", searchBytes)
	}

	conv := ConventionsResponse{
		Conventions:  []ConventionEntry{{Category: "naming", Description: "test", Strength: 0.8, Instances: []string{"a"}, TotalInstances: 1}},
		SenseMetrics: ConventionsMetrics{SymbolsAnalyzed: 200, EstimatedFileReadsAvoided: 10, EstimatedTokensSaved: 8000},
	}
	convBytes, err := MarshalConventions(conv)
	if err != nil {
		t.Fatalf("MarshalConventions: %v", err)
	}
	if strings.Contains(string(convBytes), "sense_metrics") {
		t.Errorf("conventions response should not contain sense_metrics:\n%s", convBytes)
	}

	dc := UnreferencedResponse{
		Unreferenced: UnreferencedSymbols{
			Dead: []DeadEntry{{Qualified: "W", File: "w.go", Kind: "function", Verify: "grep W"}},
		},
		TotalSymbols: 100,
		DeadCount:    1,
		SenseMetrics: DeadCodeMetrics{SymbolsAnalyzed: 100, EstimatedFileReadsAvoided: 1, EstimatedTokensSaved: 800},
	}
	dcBytes, err := MarshalUnreferenced(dc)
	if err != nil {
		t.Fatalf("MarshalUnreferenced: %v", err)
	}
	if strings.Contains(string(dcBytes), "sense_metrics") {
		t.Errorf("unreferenced response should not contain sense_metrics:\n%s", dcBytes)
	}
}

func TestResponseRetainsFreshness(t *testing.T) {
	age := int64(42)
	stale := 2
	graph := GraphResponse{
		Symbol:    GraphSymbol{Name: "X", Qualified: "X", File: "x.go", Kind: "function"},
		Freshness: &Freshness{IndexAgeSeconds: &age, StaleFilesSeen: &stale},
	}
	graphBytes, err := MarshalGraph(graph)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	if !strings.Contains(string(graphBytes), "freshness") {
		t.Errorf("graph response should contain freshness when set:\n%s", graphBytes)
	}
	if !strings.Contains(string(graphBytes), "index_age_seconds") {
		t.Errorf("freshness should contain index_age_seconds:\n%s", graphBytes)
	}
}

// TestMarshalNoHTMLEscape pins that identifiers carrying < > & stay
// literal — the documented examples print `{}` and `#` unescaped, and
// a future editor re-enabling SetEscapeHTML by mistake would break
// the byte-for-byte contract without failing a Go-level test
// otherwise.
// TestMarshalLineNumbers verifies that line_start and line_end appear
// in serialized JSON for all edge ref types that carry them, and that
// omitempty correctly suppresses zero values.
func TestMarshalLineNumbers(t *testing.T) {
	filePath := "app/models/user.rb"

	t.Run("graph response includes line numbers", func(t *testing.T) {
		in := GraphResponse{
			Symbol: GraphSymbol{
				Name: "User", Qualified: "User", File: filePath,
				LineStart: 10, LineEnd: 85, Kind: "class",
			},
			Edges: GraphEdges{
				Calls: []CallEdgeRef{
					{Symbol: "Post", File: &filePath, LineStart: 20, LineEnd: 30, Confidence: 1.0},
				},
				CalledBy: []CallEdgeRef{
					{Symbol: "SessionsController", File: &filePath, LineStart: 5, LineEnd: 15, Confidence: 0.9},
				},
				Temporal: []TemporalEdgeRef{
					{Symbol: "Order", File: &filePath, LineStart: 40, LineEnd: 50, CoChanges: 8, Strength: 0.7},
				},
			},
			NextSteps: []NextStep{},
		}
		raw, err := MarshalGraph(in)
		if err != nil {
			t.Fatalf("MarshalGraph: %v", err)
		}
		s := string(raw)
		if !strings.Contains(s, `"line_start": 10`) {
			t.Errorf("expected line_start 10 in graph response:\n%s", s)
		}
		if !strings.Contains(s, `"line_end": 85`) {
			t.Errorf("expected line_end 85 in graph response:\n%s", s)
		}
	})

	t.Run("blast response includes line numbers", func(t *testing.T) {
		in := BlastResponse{
			Symbol:      "User#email_verified?",
			Risk:        "medium",
			RiskFactors: []string{"4 direct callers"},
			DirectCallers: []BlastCaller{
				{Symbol: "SessionsController#create", File: filePath, LineStart: 12, LineEnd: 24},
			},
			IndirectCallers: []BlastIndirect{
				{Symbol: "OrdersController#new", Via: "SessionsController#create", Hops: 2, LineStart: 44, LineEnd: 56},
			},
			AffectedTests:          []string{"test/models/user_test.rb"},
			TotalAffected:          2,
			AffectedSubclasses:     []BlastCaller{},
			AffectedViaComposition: []BlastCaller{},
			AffectedViaIncludes:    []BlastCaller{},
			References:             BlastTierSummary{Count: 0, Examples: []BlastCaller{}},
			NextSteps:              []NextStep{},
		}
		raw, err := MarshalBlast(in)
		if err != nil {
			t.Fatalf("MarshalBlast: %v", err)
		}
		s := string(raw)
		if !strings.Contains(s, `"line_start": 12`) {
			t.Errorf("expected line_start 12 in blast response:\n%s", s)
		}
		if !strings.Contains(s, `"line_end": 24`) {
			t.Errorf("expected line_end 24 in blast response:\n%s", s)
		}
	})

	t.Run("zero line numbers are omitted", func(t *testing.T) {
		in := BlastResponse{
			Symbol:        "X",
			Risk:          "low",
			RiskFactors:   []string{"1 direct caller"},
			DirectCallers: []BlastCaller{{Symbol: "Y", File: "y.rb"}},
			NextSteps:     []NextStep{},
		}
		raw, err := MarshalBlast(in)
		if err != nil {
			t.Fatalf("MarshalBlast: %v", err)
		}
		s := string(raw)
		if strings.Contains(s, "line_start") {
			t.Errorf("zero line_start should be omitted (omitempty):\n%s", s)
		}
		if strings.Contains(s, "line_end") {
			t.Errorf("zero line_end should be omitted (omitempty):\n%s", s)
		}
	})
}

func TestMarshalNoHTMLEscape(t *testing.T) {
	in := GraphResponse{
		Symbol: GraphSymbol{
			Name:      "Option<T>",
			Qualified: "core::option::Option<T>",
			File:      "src/option.rs",
			LineStart: 1,
			LineEnd:   2,
			Kind:      "type",
		},
		Edges: GraphEdges{
			Calls:    []CallEdgeRef{},
			CalledBy: []CallEdgeRef{},
			Inherits: []InheritEdgeRef{},
			Composes: []ComposeEdgeRef{},
			Includes: []IncludeEdgeRef{},
			Imports:  []ImportEdgeRef{},
			Tests:    []TestEdgeRef{},
			Temporal: []TemporalEdgeRef{},
		},
	}
	raw, err := MarshalGraph(in)
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	if strings.Contains(string(raw), `\u003c`) || strings.Contains(string(raw), `\u003e`) {
		t.Fatalf("expected literal < and >, got HTML-escaped output:\n%s", raw)
	}
	if !strings.Contains(string(raw), "Option<T>") {
		t.Fatalf("expected 'Option<T>' literally, got:\n%s", raw)
	}
}

func TestSearchScoreMarshalJSON(t *testing.T) {
	tests := []struct {
		score  SearchScore
		expect string
	}{
		{0.95, "0.95"},
		{1.0, "1.00"},
		{0.0, "0.00"},
		{0.92, "0.92"},
		{1.17, "1.17"},
	}
	for _, tt := range tests {
		got, err := tt.score.MarshalJSON()
		if err != nil {
			t.Errorf("SearchScore(%v).MarshalJSON: %v", tt.score, err)
			continue
		}
		if string(got) != tt.expect {
			t.Errorf("SearchScore(%v).MarshalJSON = %q, want %q", tt.score, got, tt.expect)
		}
	}
}

func TestMarshalStatusNilSlices(t *testing.T) {
	resp := StatusResponse{}
	raw, err := MarshalStatus(resp)
	if err != nil {
		t.Fatalf("MarshalStatus: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"languages": {}`) {
		t.Errorf("expected languages: {} for nil map:\n%s", s)
	}
}

func TestMarshalSearchEmpty(t *testing.T) {
	resp := SearchResponse{}
	raw, err := MarshalSearch(resp)
	if err != nil {
		t.Fatalf("MarshalSearch: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"results": []`) {
		t.Errorf("expected results: [] in output:\n%s", s)
	}
}

func TestMarshalGraphWithLayers(t *testing.T) {
	p := "a.go"
	resp := GraphResponse{
		Symbol: GraphSymbol{Name: "X", Qualified: "X", File: "x.go", Kind: "function"},
		Edges: GraphEdges{
			Calls: []CallEdgeRef{{Symbol: "Y", File: &p}},
		},
		Layers: []GraphLayer{
			{Depth: 2, Edges: GraphEdges{Calls: []CallEdgeRef{{Symbol: "Z"}}}},
			{Depth: 3, Edges: GraphEdges{CalledBy: []CallEdgeRef{{Symbol: "W"}}}},
		},
		Truncated: true,
	}
	raw, err := MarshalGraph(resp)
	if err != nil {
		t.Fatalf("MarshalGraph with layers: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, `"layers"`) {
		t.Errorf("expected layers in output:\n%s", s)
	}
	if !strings.Contains(s, `"truncated": true`) {
		t.Errorf("expected truncated in output:\n%s", s)
	}
}

func TestMarshalGraphWithDispatchInferred(t *testing.T) {
	p := "a.go"
	resp := GraphResponse{
		Symbol: GraphSymbol{Name: "M", Qualified: "I.M", Kind: "method"},
		Edges:  GraphEdges{},
		DispatchInferred: []DispatchInferredRef{
			{Symbol: "F", File: &p, Via: "S.M", Confidence: 0.8},
		},
	}
	raw, err := MarshalGraph(resp)
	if err != nil {
		t.Fatalf("MarshalGraph with dispatch_inferred: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "dispatch_inferred") {
		t.Errorf("expected dispatch_inferred in output:\n%s", s)
	}
}

func TestMarshalGraphWithTestCallerSummary(t *testing.T) {
	resp := GraphResponse{
		Symbol:            GraphSymbol{Name: "X", Qualified: "X", File: "x.go", Kind: "class"},
		TestCallerSummary: &TestCallerSummary{Count: 5, Examples: []string{"spec/a_spec.rb", "test/b_test.rb"}},
	}
	raw, err := MarshalGraph(resp)
	if err != nil {
		t.Fatalf("MarshalGraph with test_caller_summary: %v", err)
	}
	s := string(raw)
	if !strings.Contains(s, "test_caller_summary") {
		t.Errorf("expected test_caller_summary in output:\n%s", s)
	}
}

// TestMarshalCompactEquivalence covers pitch 25-05 pattern 1:
// for every Marshal*Compact variant, the compact bytes must
//  1. decode back to a Go value equal to the pretty bytes' decode,
//  2. shrink relative to the pretty bytes on a non-trivial fixture,
//  3. contain no JSON indentation whitespace ("\n  ").
//
// The decoded-equality check is the load-bearing one: it proves the
// MCP wire change is semantically invisible to consumers.
func TestMarshalCompactEquivalence(t *testing.T) {
	filePath := "app/services/payment_gateway.rb"
	graph := GraphResponse{
		Symbol: GraphSymbol{
			Name: "CheckoutService", Qualified: "App::Services::CheckoutService",
			File: "app/services/checkout_service.rb", LineStart: 12, LineEnd: 85, Kind: "class",
		},
		Edges: GraphEdges{
			Calls: []CallEdgeRef{
				{Symbol: "PaymentGateway#charge", File: &filePath, Confidence: 1.0},
				{Symbol: "Beacon.track", File: nil, Confidence: 0.9},
			},
			Inherits: []InheritEdgeRef{{Symbol: "ApplicationService", File: nil}},
			Tests:    []TestEdgeRef{{File: "test/services/checkout_service_test.rb", Confidence: 0.8}},
		},
		NextSteps: []NextStep{{Tool: "sense_blast", Args: map[string]any{"symbol": "CheckoutService"}, Reason: "high-fanout symbol — check blast radius"}},
	}

	tests := []struct {
		name    string
		pretty  func() ([]byte, error)
		compact func() ([]byte, error)
		into    func() any
	}{
		{
			name:    "graph",
			pretty:  func() ([]byte, error) { return MarshalGraph(graph) },
			compact: func() ([]byte, error) { return MarshalGraphCompact(graph) },
			into:    func() any { return &GraphResponse{} },
		},
		{
			name:    "search",
			pretty:  func() ([]byte, error) { return MarshalSearch(searchFixture()) },
			compact: func() ([]byte, error) { return MarshalSearchCompact(searchFixture()) },
			into:    func() any { return &SearchResponse{} },
		},
		{
			name:    "blast",
			pretty:  func() ([]byte, error) { return MarshalBlast(blastFixture()) },
			compact: func() ([]byte, error) { return MarshalBlastCompact(blastFixture()) },
			into:    func() any { return &BlastResponse{} },
		},
		{
			name:    "status",
			pretty:  func() ([]byte, error) { return MarshalStatus(statusFixture()) },
			compact: func() ([]byte, error) { return MarshalStatusCompact(statusFixture()) },
			into:    func() any { return &StatusResponse{} },
		},
		{
			name:    "unreferenced",
			pretty:  func() ([]byte, error) { return MarshalUnreferenced(unreferencedFixture()) },
			compact: func() ([]byte, error) { return MarshalUnreferencedCompact(unreferencedFixture()) },
			into:    func() any { return &UnreferencedResponse{} },
		},
		{
			name:    "conventions",
			pretty:  func() ([]byte, error) { return MarshalConventions(conventionsFixture()) },
			compact: func() ([]byte, error) { return MarshalConventionsCompact(conventionsFixture()) },
			into:    func() any { return &ConventionsResponse{} },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pretty, err := tc.pretty()
			if err != nil {
				t.Fatalf("pretty: %v", err)
			}
			compact, err := tc.compact()
			if err != nil {
				t.Fatalf("compact: %v", err)
			}

			if len(compact) >= len(pretty) {
				t.Errorf("compact (%d) not smaller than pretty (%d)", len(compact), len(pretty))
			}
			if strings.Contains(string(compact), "\n  ") {
				t.Errorf("compact bytes contain pretty-print indentation:\n%s", compact)
			}

			prettyDecoded, compactDecoded := tc.into(), tc.into()
			if err := json.Unmarshal(pretty, prettyDecoded); err != nil {
				t.Fatalf("decode pretty: %v", err)
			}
			if err := json.Unmarshal(compact, compactDecoded); err != nil {
				t.Fatalf("decode compact: %v", err)
			}
			if !reflect.DeepEqual(prettyDecoded, compactDecoded) {
				t.Errorf("decoded values differ\npretty:  %+v\ncompact: %+v", prettyDecoded, compactDecoded)
			}
		})
	}
}

// TestMarshalGraphCompactDirectional covers pitch 25-05 pattern 2:
// when sense_graph is called with direction=callers or callees, the
// edge buckets the request excluded are absent from the wire (no
// `[]` and no `null`), while in-scope empty buckets still emit `[]`.
func TestMarshalGraphCompactDirectional(t *testing.T) {
	base := func() GraphResponse {
		return GraphResponse{
			Symbol: GraphSymbol{Name: "X", Qualified: "pkg.X", File: "pkg/x.go", Kind: "function"},
		}
	}

	t.Run("callers omits calls and inherits but keeps inherited_by", func(t *testing.T) {
		raw, err := MarshalGraphCompactDirectional(base(), model.DirectionCallers)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s := string(raw)
		// `inherits` (supertypes) is outbound-only, so it is pruned from a
		// callers query alongside `calls`.
		for _, key := range []string{`"calls"`, `"inherits":`} {
			if strings.Contains(s, key) {
				t.Errorf("expected %s to be absent for direction=callers:\n%s", key, s)
			}
		}
		// `inherited_by` carries inheritors (inbound EdgeInherits) under
		// the callers direction — the natural fit for "who implements
		// this trait." Empty bucket renders as `[]`.
		for _, key := range []string{`"called_by":[]`, `"tests":[]`, `"inherited_by":[]`} {
			if !strings.Contains(s, key) {
				t.Errorf("expected %s to be present for direction=callers:\n%s", key, s)
			}
		}
	})

	t.Run("callees omits called_by, tests and inherited_by", func(t *testing.T) {
		raw, err := MarshalGraphCompactDirectional(base(), model.DirectionCallees)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s := string(raw)
		// `inherited_by` (subtypes) is inbound-only, pruned from a callees query.
		for _, key := range []string{`"called_by"`, `"tests"`, `"inherited_by"`} {
			if strings.Contains(s, key) {
				t.Errorf("expected %s to be absent for direction=callees:\n%s", key, s)
			}
		}
		for _, key := range []string{`"calls":[]`, `"inherits":[]`} {
			if !strings.Contains(s, key) {
				t.Errorf("expected %s to be present for direction=callees:\n%s", key, s)
			}
		}
	})

	t.Run("both renders all edge buckets", func(t *testing.T) {
		raw, err := MarshalGraphCompactDirectional(base(), model.DirectionBoth)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		s := string(raw)
		for _, key := range []string{"calls", "called_by", "inherits", "inherited_by", "composes", "composed_by", "includes", "imports", "tests", "temporal"} {
			if !strings.Contains(s, `"`+key+`":[]`) {
				t.Errorf("expected %q:[] to be present for direction=both:\n%s", key, s)
			}
		}
	})

	t.Run("decoded equality with symmetric variant on in-scope buckets", func(t *testing.T) {
		filePath := "pkg/caller.go"
		r := base()
		r.Edges.CalledBy = []CallEdgeRef{{Symbol: "Caller", File: &filePath, Confidence: 1.0}}
		r.Edges.Tests = []TestEdgeRef{{File: "pkg/x_test.go", Confidence: 0.8}}

		raw, err := MarshalGraphCompactDirectional(r, model.DirectionCallers)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded GraphResponse
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode: %v\nbytes:\n%s", err, raw)
		}
		if len(decoded.Edges.CalledBy) != 1 || decoded.Edges.CalledBy[0].Symbol != "Caller" {
			t.Errorf("CalledBy lost in round-trip: %+v", decoded.Edges.CalledBy)
		}
		if len(decoded.Edges.Tests) != 1 {
			t.Errorf("Tests lost in round-trip: %+v", decoded.Edges.Tests)
		}
		if decoded.Edges.Calls != nil {
			t.Errorf("Calls should round-trip as nil for direction=callers, got %+v", decoded.Edges.Calls)
		}
	})

	t.Run("byte savings vs symmetric for empty isolated symbol", func(t *testing.T) {
		symmetric, _ := MarshalGraphCompact(base())
		directional, _ := MarshalGraphCompactDirectional(base(), model.DirectionCallers)
		if len(directional) >= len(symmetric) {
			t.Errorf("directional (%d bytes) not smaller than symmetric (%d) for empty graph", len(directional), len(symmetric))
		}
	})
}

func searchFixture() SearchResponse {
	return SearchResponse{
		Results: []SearchResultEntry{
			{Symbol: "User", File: "app/models/user.rb", Line: 1, Kind: "class", Score: 0.92, Snippet: "class User < ApplicationRecord", References: 12, Source: "hybrid"},
		},
		SearchMode:    "hybrid",
		FusionWeights: FusionWeights{Keyword: 0.6, Vector: 0.4},
	}
}

func blastFixture() BlastResponse {
	return BlastResponse{
		Symbol:        "User#verified?",
		Risk:          "high",
		RiskFactors:   []string{"public API", "many callers"},
		DirectCallers: []BlastCaller{{Symbol: "SessionsController#create", File: "app/controllers/sessions_controller.rb", LineStart: 8, LineEnd: 8}},
		AffectedTests: []string{"spec/models/user_spec.rb"},
		TotalAffected: 17,
	}
}

func statusFixture() StatusResponse {
	return StatusResponse{
		Index:     StatusIndex{Path: ".sense.db", SizeBytes: 1024, Files: 10, Symbols: 50, Edges: 80, Embeddings: 50, Coverage: 1.0},
		Languages: map[string]StatusLanguage{"go": {Files: 10, Symbols: 50, Tier: "full"}},
	}
}

// TestNormalizeUnreferencedFillsNilSlices pins the wire-shape contract: nil
// slices normalize to empty arrays so the JSON is stable (`[]`, never `null`)
// for the consuming agent. The per-group loop is exercised by a PossiblyDead
// group whose Symbols slice is nil.
func TestNormalizeUnreferencedFillsNilSlices(t *testing.T) {
	r := UnreferencedResponse{
		Unreferenced: UnreferencedSymbols{
			Dead: nil,
			PossiblyDead: []PossiblyDeadGroup{{
				Reason:  ReasonInfo{Code: "ruby_public_method"},
				Symbols: nil,
			}},
		},
		NextSteps: nil,
	}
	normalizeUnreferencedResponse(&r)

	if r.Unreferenced.Dead == nil {
		t.Error("nil Dead should normalize to a non-nil empty slice")
	}
	if r.Unreferenced.PossiblyDead[0].Symbols == nil {
		t.Error("nil group Symbols should normalize to a non-nil empty slice")
	}
	if r.NextSteps == nil {
		t.Error("nil NextSteps should normalize to a non-nil empty slice")
	}
}

func unreferencedFixture() UnreferencedResponse {
	return UnreferencedResponse{
		Unreferenced: UnreferencedSymbols{
			Dead: []DeadEntry{{Qualified: "pkg.Old", File: "pkg/old.go", Line: 1, Kind: "function", Verify: "grep Old"}},
			PossiblyDead: []PossiblyDeadGroup{{
				Reason:  ReasonInfo{Code: "ruby_public_method", Hint: "public method; grep call sites"},
				Verify:  "grep each name",
				Symbols: []PossiblyDeadSymbol{{Qualified: "pkg.Maybe", File: "pkg/maybe.rb", Line: 3, Kind: "method"}},
			}},
		},
		TotalSymbols:      100,
		DeadCount:         1,
		PossiblyDeadCount: 1,
	}
}

func conventionsFixture() ConventionsResponse {
	return ConventionsResponse{
		KeySymbols: []KeySymbolEntry{{Name: "ApplicationRecord", Kind: "class", References: 2, Callers: []string{"User", "Order"}}},
	}
}

func TestEstimateJSONTokens(t *testing.T) {
	v := struct {
		Name string `json:"name"`
	}{Name: "test"}
	n := estimateJSONTokens(v)
	if n <= 0 {
		t.Errorf("estimateJSONTokens returned %d, want > 0", n)
	}
}

func TestMarshalStatusNilStructure(t *testing.T) {
	resp := StatusResponse{
		Languages: nil,
		NextSteps: nil,
	}

	raw, err := MarshalStatus(resp)
	if err != nil {
		t.Fatalf("MarshalStatus: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	langs, ok := out["languages"]
	if !ok {
		t.Fatal("missing languages key")
	}
	if langs == nil {
		t.Error("languages should be {} not null")
	}
}

func TestMarshalStatusWithStructure(t *testing.T) {
	resp := StatusResponse{
		Languages: map[string]StatusLanguage{"go": {Files: 10, Symbols: 100}},
		Structure: &StatusStructure{
			TopNamespaces: nil,
			HubSymbols:    nil,
			EntryPoints:   nil,
		},
		NextSteps: nil,
	}

	raw, err := MarshalStatus(resp)
	if err != nil {
		t.Fatalf("MarshalStatus: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	structure, ok := out["structure"].(map[string]any)
	if !ok {
		t.Fatal("missing structure")
	}
	if structure["top_namespaces"] == nil {
		t.Error("top_namespaces should be [] not null")
	}
}

func TestEstimateJSONTokensEmpty(t *testing.T) {
	if got := estimateJSONTokens([]byte("{}")); got != 1 {
		t.Errorf("estimateJSONTokens({}) = %d, want 1", got)
	}
}
