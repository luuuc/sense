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
		},
		SenseMetrics: GraphMetrics{SymbolsReturned: 3},
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
		AffectedTests: []string{"test/models/user_test.rb"},
		TotalAffected: 2,
		SenseMetrics:  BlastMetrics{SymbolsTraversed: 5},
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
// The savings fields on SenseMetrics render as `null` by policy
// (pitch 01-05: honest stub until 04-03 lands real estimation),
// so the test explicitly allows null there and elsewhere — it only
// enforces that the named slice fields carry `[]`.
func TestMarshalZeroValueEmptySlices(t *testing.T) {
	graphBytes, err := MarshalGraph(GraphResponse{})
	if err != nil {
		t.Fatalf("MarshalGraph: %v", err)
	}
	for _, field := range []string{`"calls": []`, `"called_by": []`, `"inherits": []`, `"composes": []`, `"includes": []`, `"imports": []`, `"tests": []`} {
		if !strings.Contains(string(graphBytes), field) {
			t.Errorf("GraphResponse zero-value missing %s\ngot:\n%s", field, graphBytes)
		}
	}
	for _, nullField := range []string{`"calls": null`, `"called_by": null`, `"inherits": null`, `"composes": null`, `"includes": null`, `"imports": null`, `"tests": null`} {
		if strings.Contains(string(graphBytes), nullField) {
			t.Errorf("GraphResponse zero-value slice field should be []: %s\ngot:\n%s", nullField, graphBytes)
		}
	}

	blastBytes, err := MarshalBlast(BlastResponse{})
	if err != nil {
		t.Fatalf("MarshalBlast: %v", err)
	}
	for _, field := range []string{`"risk_factors": []`, `"direct_callers": []`, `"indirect_callers": []`, `"affected_tests": []`} {
		if !strings.Contains(string(blastBytes), field) {
			t.Errorf("BlastResponse zero-value missing %s\ngot:\n%s", field, blastBytes)
		}
	}
	for _, nullField := range []string{`"risk_factors": null`, `"direct_callers": null`, `"indirect_callers": null`, `"affected_tests": null`} {
		if strings.Contains(string(blastBytes), nullField) {
			t.Errorf("BlastResponse zero-value slice field should be []: %s\ngot:\n%s", nullField, blastBytes)
		}
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
