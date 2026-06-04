package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestSatisfyExceedsBudget proves the performance gate: when the
// interface×struct product exceeds 500K the pass warns and reports the budget
// as exceeded, so satisfyInterfaces skips the O(n²) satisfaction scan rather
// than stalling a large repo.
func TestSatisfyExceedsBudget(t *testing.T) {
	var warn bytes.Buffer
	h := &harness{warn: &warn}

	// One interface, but enough structs that ifaceCount×structs blows the gate
	// (the stdlib interfaces are added to ifaceCount, so even one declared
	// interface needs a large struct set; 600K structs clears 500K outright).
	interfaces := map[int64]*ifaceInfo{1: {}}
	structs := make(map[int64]*structInfo, 1)
	structs[1] = &structInfo{}

	// Cheaper than allocating 600K maps: the product is len×len, so make the
	// struct count large by faking the length via many keys is expensive; use
	// a count that, multiplied by (1 declared + len(stdlibInterfaces)), exceeds
	// the gate. len(stdlibInterfaces) is ~12, so 50_000 structs → >600K.
	for i := int64(2); i <= 50_000; i++ {
		structs[i] = &structInfo{}
	}

	if !h.satisfyExceedsBudget(interfaces, structs) {
		t.Fatal("expected the budget gate to trip for a large struct set")
	}
	if warn.Len() == 0 {
		t.Error("expected a skip warning when the budget is exceeded")
	}
}

// TestSatisfyExceedsBudgetWithinLimit confirms a small product does not trip
// the gate, so the satisfaction scan runs.
func TestSatisfyExceedsBudgetWithinLimit(t *testing.T) {
	var warn bytes.Buffer
	h := &harness{warn: &warn}
	interfaces := map[int64]*ifaceInfo{1: {}}
	structs := map[int64]*structInfo{1: {}, 2: {}}
	if h.satisfyExceedsBudget(interfaces, structs) {
		t.Fatal("small interface×struct product should be within budget")
	}
	if warn.Len() != 0 {
		t.Errorf("no warning expected within budget, got %q", warn.String())
	}
}

// TestSatisfyInterfacesQueryError covers satisfyInterfaces' first failure
// guard: querying the Go files fails on a closed index and the wrapped error
// surfaces instead of an empty-but-successful pass.
func TestSatisfyInterfacesQueryError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), out: io.Discard, warn: io.Discard}
	if err := h.satisfyInterfaces(); err == nil {
		t.Fatal("expected error querying go files on a closed index")
	}
}

// TestPromoteEmbeddedMethodSetsEdgeQueryError covers promoteEmbeddedMethodSets'
// edge-load failure (satisfy.go:152-154): querying the includes edges fails on a
// closed index and the wrapped error returns before any promotion.
func TestPromoteEmbeddedMethodSetsEdgeQueryError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), out: io.Discard, warn: io.Discard}
	structs := map[int64]*structInfo{1: {methods: map[string]bool{}}}
	if err := h.promoteEmbeddedMethodSets(structs); err == nil {
		t.Fatal("expected error loading includes edges on a closed index")
	}
}

// faultSatisfyQueryStore returns one Go file so satisfyInterfaces proceeds past
// the file check, then fails the symbol Query. Two named methods, both
// deterministic — the file set is real-shaped, the symbol load is the injected
// failure.
type faultSatisfyQueryStore struct {
	*sqlite.Adapter
}

func (f *faultSatisfyQueryStore) FileIDsByLanguage(context.Context, string) (map[int64]bool, error) {
	return map[int64]bool{1: true}, nil
}

func (f *faultSatisfyQueryStore) Query(context.Context, index.Filter) ([]model.Symbol, error) {
	return nil, errors.New("injected Query failure")
}

// TestSatisfyInterfacesSymbolQueryError covers satisfyInterfaces' symbol-query
// failure (satisfy.go:65-67): the Go-file set loads, but loading the symbols to
// classify fails, so the wrapped error surfaces instead of an empty pass.
func TestSatisfyInterfacesSymbolQueryError(t *testing.T) {
	h := &harness{
		ctx:  context.Background(),
		idx:  &faultSatisfyQueryStore{Adapter: newOpenAdapter(t)},
		out:  io.Discard,
		warn: io.Discard,
	}
	if err := h.satisfyInterfaces(); err == nil {
		t.Fatal("expected error querying symbols when the load fails")
	}
}

func TestMethodSetSatisfies(t *testing.T) {
	methods := map[string]bool{"Read": true, "Write": true, "Close": true}

	if !methodSetSatisfies(methods, []string{"Read", "Write"}) {
		t.Error("should satisfy subset")
	}
	if !methodSetSatisfies(methods, []string{"Read", "Write", "Close"}) {
		t.Error("should satisfy exact set")
	}
	if methodSetSatisfies(methods, []string{"Read", "Flush"}) {
		t.Error("should not satisfy with missing method")
	}
	if !methodSetSatisfies(methods, nil) {
		t.Error("nil required should satisfy")
	}
}

func TestPromoteEmbeddedMethods(t *testing.T) {
	outer := &structInfo{methods: map[string]bool{"Own": true}}
	inner := &structInfo{methods: map[string]bool{"Read": true, "Write": true}}

	structs := map[int64]*structInfo{
		1: outer,
		2: inner,
	}
	embeddings := map[int64][]int64{
		1: {2},
	}

	promoteEmbeddedMethods(outer, 1, embeddings, structs, 3)

	if !outer.methods["Read"] {
		t.Error("expected Read promoted from embedded struct")
	}
	if !outer.methods["Write"] {
		t.Error("expected Write promoted from embedded struct")
	}
	if !outer.methods["Own"] {
		t.Error("expected Own to remain")
	}
}

func TestPromoteEmbeddedMethodsDepthLimit(t *testing.T) {
	a := &structInfo{methods: map[string]bool{}}
	b := &structInfo{methods: map[string]bool{}}
	c := &structInfo{methods: map[string]bool{"Deep": true}}

	structs := map[int64]*structInfo{1: a, 2: b, 3: c}
	embeddings := map[int64][]int64{1: {2}, 2: {3}}

	promoteEmbeddedMethods(a, 1, embeddings, structs, 1)

	// Depth=1 means we only go one hop. b's methods get promoted,
	// but c's methods should not (they need depth=2).
	if a.methods["Deep"] {
		t.Error("expected depth limit to prevent promoting from 2 hops away")
	}
}
