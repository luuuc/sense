package blast

// Chain assembly policy: a chain renders whole or not at all. These are
// internal tests because the churn window they guard (a symbol row vanishing
// between the edge walk and hydration) cannot be constructed through the
// public Compute path: the closure would simply never admit the carrier.

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func TestAssembleChainDropsWholeOnMissingHop(t *testing.T) {
	syms := map[int64]model.Symbol{
		1: {ID: 1, Name: "A"},
		3: {ID: 3, Name: "C"},
	}
	if got := assembleChain(syms, []int64{1, 2, 3}); got != nil {
		t.Fatalf("missing hop 2 must drop the whole chain, got %v (a spliced A > C fabricates an edge)", got)
	}
}

func TestAssembleChainWholeWhenAllHydrate(t *testing.T) {
	syms := map[int64]model.Symbol{
		1: {ID: 1, Name: "A"},
		2: {ID: 2, Name: "B"},
	}
	got := assembleChain(syms, []int64{1, 2})
	if len(got) != 2 || got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("full chain must hydrate in order, got %v", got)
	}
}
