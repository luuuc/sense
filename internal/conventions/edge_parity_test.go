package conventions

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// TestEdgeKindConsumerParity is the anti-drift gate. This package consumes a SUBSET of
// the edge vocabulary, and the subset plus its documented exemptions must account for
// every kind in model.AllEdgeKinds. A new kind therefore cannot be silently ignored: it
// either joins the list or is exempted here with a reason.
//
// Born from four instances of one failure in a single day, all the same shape: a producer
// gained a value and a consumer's hardcoded list never learned about it. blast did not
// traverse `imports` for two months after the PHP extractor began emitting it.
func TestEdgeKindConsumerParity(t *testing.T) {
	covered := map[model.EdgeKind]bool{}
	for _, k := range couplingKinds {
		covered[k] = true
	}
	exempt := map[model.EdgeKind]string{
		model.EdgeTests:      "a test reaching across areas is not an architecture violation",
		model.EdgeTemporal:   "co-change is not a structural dependency",
		model.EdgeReferences: "too weak to imply coupling",
		model.EdgeImports:    "an import without a call is not coupling in this detector's sense",
	}

	for _, k := range model.AllEdgeKinds {
		_, isExempt := exempt[k]
		if covered[k] && isExempt {
			t.Errorf("kind %q is both consumed and exempted: pick one", k)
		}
		if !covered[k] && !isExempt {
			t.Errorf("kind %q is neither consumed nor exempted here. A new edge kind must be "+
				"handled deliberately: add it to the list, or exempt it with the reason.", k)
		}
	}
	for k := range exempt {
		if covered[k] {
			continue // already reported above
		}
		found := false
		for _, known := range model.AllEdgeKinds {
			if known == k {
				found = true
			}
		}
		if !found {
			t.Errorf("exemption names %q, which is not a known edge kind: remove the stale entry", k)
		}
	}
}
