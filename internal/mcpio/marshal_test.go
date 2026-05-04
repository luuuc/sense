package mcpio

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
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
			CalledBy: []CallEdgeRef{},
			Inherits: []InheritEdgeRef{{Symbol: "ApplicationService", File: nil}},
			Composes: []ComposeEdgeRef{},
			Includes: []IncludeEdgeRef{},
			Imports:  []ImportEdgeRef{},
			Tests:    []TestEdgeRef{{File: "test/services/checkout_service_test.rb", Confidence: 0.8}},
			Temporal: []TemporalEdgeRef{},
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
		NextSteps: []NextStep{},
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
	for _, field := range []string{`"calls": []`, `"called_by": []`, `"inherits": []`, `"composes": []`, `"includes": []`, `"imports": []`, `"tests": []`, `"temporal": []`, `"next_steps": []`} {
		if !strings.Contains(string(graphBytes), field) {
			t.Errorf("GraphResponse zero-value missing %s\ngot:\n%s", field, graphBytes)
		}
	}
	for _, nullField := range []string{`"calls": null`, `"called_by": null`, `"inherits": null`, `"composes": null`, `"includes": null`, `"imports": null`, `"tests": null`, `"temporal": null`, `"next_steps": null`} {
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

	dc := DeadCodeResponse{
		DeadSymbols:  []DeadSymbolEntry{{Symbol: "W", Qualified: "W", File: "w.go", Kind: "function"}},
		TotalSymbols: 100,
		DeadCount:    1,
		SenseMetrics: DeadCodeMetrics{SymbolsAnalyzed: 100, EstimatedFileReadsAvoided: 1, EstimatedTokensSaved: 800},
	}
	dcBytes, err := MarshalDeadCode(dc)
	if err != nil {
		t.Fatalf("MarshalDeadCode: %v", err)
	}
	if strings.Contains(string(dcBytes), "sense_metrics") {
		t.Errorf("dead code response should not contain sense_metrics:\n%s", dcBytes)
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
