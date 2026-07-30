package sqlite

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
	for _, k := range contextKinds {
		covered[k] = true
	}
	exempt := map[model.EdgeKind]string{
		model.EdgeTests:      "the context view is a structural neighbourhood, not test wiring",
		model.EdgeTemporal:   "co-change does not show how a symbol is wired",
		model.EdgeReferences: "too weak to describe the neighbourhood",
		model.EdgeImports:    "an import does not show how the symbol is used",
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
