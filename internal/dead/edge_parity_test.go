package dead

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
	for _, k := range reachabilityKinds {
		covered[k] = true
	}
	exempt := map[model.EdgeKind]string{
		model.EdgeTests:    "a symbol referenced only by its own test is dead-but-tested, not reachable",
		model.EdgeTemporal: "co-change is correlation, not a reference",
		model.EdgeImports:  "importing a symbol is not using it; counting it would mark unused imports alive",
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
