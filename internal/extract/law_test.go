package extract

import "testing"

// TestReceiverConfidenceLaw pins decision 0003, the cross-language
// receiver/confidence law. Clause 1: an unresolved-receiver bare-name
// fallback edge carries at most ConfidenceNameCollision, strictly below
// blast's traversal floor (defaultMinConfidence = 0.5 in internal/blast),
// so a guess can never pollute impact analysis at default settings.
// Clause 2: a bare-name edge to a common name is not emitted at all
// without a type witness.
func TestReceiverConfidenceLaw(t *testing.T) {
	if ConfidenceNameCollision > 0.3 {
		t.Fatalf("clause 1: bare-name fallback confidence %v exceeds 0.3", ConfidenceNameCollision)
	}
	if ConfidenceNameCollision >= 0.5 {
		t.Fatalf("clause 1: bare-name fallback confidence %v is not below blast's 0.5 traversal floor", ConfidenceNameCollision)
	}

	common := map[string]bool{"get": true, "set": true, "filter": true}

	if conf, ok := BareNameEdge("get", common); ok {
		t.Fatalf("clause 2: common name %q emitted a bare-name edge (conf %v)", "get", conf)
	}

	conf, ok := BareNameEdge("checkoutTotal", common)
	if !ok {
		t.Fatalf("clause 2: non-common name %q was refused a bare-name edge", "checkoutTotal")
	}
	if conf != ConfidenceNameCollision {
		t.Fatalf("clause 2: bare-name edge confidence = %v, want ConfidenceNameCollision (%v)", conf, ConfidenceNameCollision)
	}

	if conf, ok := BareNameEdge("get", nil); !ok || conf != ConfidenceNameCollision {
		t.Fatalf("nil common set must be permissive: got conf=%v ok=%v", conf, ok)
	}
}
