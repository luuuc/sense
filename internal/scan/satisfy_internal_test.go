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

// TestLoadEmbeddingsEdgeQueryError covers the shared embeddings load failure:
// querying the includes edges fails on a closed index and the wrapped error
// returns before any expansion or promotion consumes the map.
func TestLoadEmbeddingsEdgeQueryError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), out: io.Discard, warn: io.Discard}
	if _, err := h.loadEmbeddings(); err == nil {
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

	if !methodSetSatisfies(methods, map[string]bool{"Read": true, "Write": true}) {
		t.Error("should satisfy subset")
	}
	if !methodSetSatisfies(methods, map[string]bool{"Read": true, "Write": true, "Close": true}) {
		t.Error("should satisfy exact set")
	}
	if methodSetSatisfies(methods, map[string]bool{"Read": true, "Flush": true}) {
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

	promoteEmbeddedMethods(outer, 1, embeddings, structs, nil, 3)

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

	promoteEmbeddedMethods(a, 1, embeddings, structs, nil, 1)

	// Depth=1 means we only go one hop. b's methods get promoted,
	// but c's methods should not (they need depth=2).
	if a.methods["Deep"] {
		t.Error("expected depth limit to prevent promoting from 2 hops away")
	}
}

// TestExpandInterfaceMethodSets proves the interface-side closure: a chain of
// embedded interfaces unions transitively (no depth cap — a truncated
// expansion would shrink required sets and re-create false satisfaction),
// diamonds dedupe, and unknown targets contribute nothing.
func TestExpandInterfaceMethodSets(t *testing.T) {
	interfaces := map[int64]*ifaceInfo{
		1: {methods: map[string]bool{"A": true}},
		2: {methods: map[string]bool{"B": true}},
		3: {methods: map[string]bool{}},
		4: {methods: map[string]bool{"D": true}},
	}
	// 3 embeds 2 embeds 1 (chain, deeper than one hop); 4 embeds 1 and 2
	// (diamond: A arrives via both paths); 2 also embeds an unknown target.
	embeddings := map[int64][]int64{
		2: {1, 99},
		3: {2},
		4: {1, 2},
	}
	expandInterfaceMethodSets(interfaces, embeddings)

	for _, m := range []string{"A", "B"} {
		if !interfaces[3].methods[m] {
			t.Errorf("chain: interface 3 missing %s after expansion", m)
		}
	}
	if len(interfaces[4].methods) != 3 { // A, B, D — diamond dedupes
		t.Errorf("diamond: expected 3 methods on interface 4, got %v", interfaces[4].methods)
	}
	if !interfaces[2].methods["A"] || len(interfaces[2].methods) != 2 {
		t.Errorf("unknown target must contribute nothing: %v", interfaces[2].methods)
	}
}

// TestExpandInterfaceMethodSetsCycle proves termination on an embedding cycle
// (illegal Go, but the index can contain mid-edit or misresolved code).
func TestExpandInterfaceMethodSetsCycle(t *testing.T) {
	interfaces := map[int64]*ifaceInfo{
		1: {methods: map[string]bool{"A": true}},
		2: {methods: map[string]bool{"B": true}},
	}
	embeddings := map[int64][]int64{1: {2}, 2: {1}}
	expandInterfaceMethodSets(interfaces, embeddings) // must terminate
	if !interfaces[1].methods["B"] {
		t.Error("cycle: interface 1 should still union interface 2's methods")
	}
}

// TestPromoteThroughEmbeddedInterface proves the struct-side path for an
// embedded interface VALUE (the pageState shape): the interface's expanded
// method set delegates wholesale onto the struct.
func TestPromoteThroughEmbeddedInterface(t *testing.T) {
	iface := &ifaceInfo{methods: map[string]bool{"A": true, "B": true}}
	st := &structInfo{methods: map[string]bool{"Own": true}}
	structs := map[int64]*structInfo{1: st}
	interfaces := map[int64]*ifaceInfo{7: iface}
	embeddings := map[int64][]int64{1: {7}}

	promoteEmbeddedMethodSets(structs, interfaces, embeddings)

	for _, m := range []string{"A", "B", "Own"} {
		if !st.methods[m] {
			t.Errorf("struct missing %s after embedded-interface promotion", m)
		}
	}
}

// satisfyFakeStore drives satisfyInterfaces past classification with an
// in-memory symbol set, then lets each test override one downstream call.
type satisfyFakeStore struct {
	*sqlite.Adapter
	syms      []model.Symbol
	edges     []model.Edge
	edgesErr  error
	writeErr  error
	structCnt int64
}

func (f *satisfyFakeStore) FileIDsByLanguage(context.Context, string) (map[int64]bool, error) {
	return map[int64]bool{1: true}, nil
}

func (f *satisfyFakeStore) Query(context.Context, index.Filter) ([]model.Symbol, error) {
	if f.syms != nil {
		return f.syms, nil
	}
	iface := int64(1)
	syms := []model.Symbol{
		{ID: 1, Name: "I", Kind: model.KindInterface, FileID: 1},
		{ID: 2, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: &iface},
		{ID: 3, Name: "S", Kind: model.KindClass, FileID: 1},
	}
	for i := int64(0); i < f.structCnt; i++ {
		syms = append(syms, model.Symbol{ID: 100 + i, Kind: model.KindClass, FileID: 1})
	}
	return syms, nil
}

func (f *satisfyFakeStore) EdgesOfKind(context.Context, model.EdgeKind) ([]model.Edge, error) {
	return f.edges, f.edgesErr
}

func (f *satisfyFakeStore) InTx(_ context.Context, fn func() error) error { return fn() }

func (f *satisfyFakeStore) WriteEdge(context.Context, *model.Edge) (int64, error) {
	return 0, f.writeErr
}

// TestSatisfyInterfacesBudgetSkip covers the budget short-circuit through the
// full pass: enough structs to blow 500K means no edges are attempted. The
// trip arithmetic leans on the dormant stdlibInterfaces table: (1 declared +
// len(stdlibInterfaces)=12) × 50,001 structs ≈ 650K > 500K. If that table
// ever shrinks below 9 entries this fails on the warn assertion — adjust
// structCnt, not the gate.
func TestSatisfyInterfacesBudgetSkip(t *testing.T) {
	var warn bytes.Buffer
	h := &harness{ctx: context.Background(),
		idx: &satisfyFakeStore{Adapter: newOpenAdapter(t), structCnt: 50_000, writeErr: errors.New("must not write")},
		out: io.Discard, warn: &warn}
	if err := h.satisfyInterfaces(); err != nil {
		t.Fatalf("budget skip must not error: %v", err)
	}
	if warn.Len() == 0 {
		t.Error("expected the budget skip warning")
	}
}

// TestSatisfyInterfacesEmbeddingsLoadError covers the shared embeddings load
// failing inside the full pass.
func TestSatisfyInterfacesEmbeddingsLoadError(t *testing.T) {
	h := &harness{ctx: context.Background(),
		idx: &satisfyFakeStore{Adapter: newOpenAdapter(t), edgesErr: errors.New("injected edges failure")},
		out: io.Discard, warn: io.Discard}
	if err := h.satisfyInterfaces(); err == nil {
		t.Fatal("expected the embeddings load error to surface")
	}
}

// TestSatisfyInterfacesWriteError covers a satisfaction-edge write failing
// inside the transaction: S has method M so it satisfies I, and the injected
// WriteEdge failure must surface wrapped.
func TestSatisfyInterfacesWriteError(t *testing.T) {
	structID := int64(3)
	f := &satisfyFakeStore{Adapter: newOpenAdapter(t), writeErr: errors.New("injected write failure")}
	f.syms = []model.Symbol{
		{ID: 1, Name: "I", Kind: model.KindInterface, FileID: 1},
		{ID: 2, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: func() *int64 { i := int64(1); return &i }()},
		{ID: 3, Name: "S", Kind: model.KindClass, FileID: 1},
		{ID: 4, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: &structID},
	}
	h := &harness{ctx: context.Background(), idx: f, out: io.Discard, warn: io.Discard}
	if err := h.satisfyInterfaces(); err == nil {
		t.Fatal("expected the write failure to surface")
	}
}

// TestLoadEmbeddingsSkipsNilSource pins the adjacency build: an includes edge
// without a resolved source contributes nothing.
func TestLoadEmbeddingsSkipsNilSource(t *testing.T) {
	src := int64(1)
	f := &satisfyFakeStore{Adapter: newOpenAdapter(t), edges: []model.Edge{
		{SourceID: nil, TargetID: 9},
		{SourceID: &src, TargetID: 2},
	}}
	h := &harness{ctx: context.Background(), idx: f, out: io.Discard, warn: io.Discard}
	embeddings, err := h.loadEmbeddings()
	if err != nil {
		t.Fatalf("loadEmbeddings: %v", err)
	}
	if len(embeddings) != 1 || len(embeddings[1]) != 1 || embeddings[1][0] != 2 {
		t.Errorf("expected only the resolved edge in the adjacency, got %v", embeddings)
	}
}

// TestPromoteEmbeddedMethodsUnknownTarget pins the skip for a target that is
// neither a known interface nor a known struct (stdlib, unresolved).
func TestPromoteEmbeddedMethodsUnknownTarget(t *testing.T) {
	st := &structInfo{methods: map[string]bool{"Own": true}}
	structs := map[int64]*structInfo{1: st}
	promoteEmbeddedMethods(st, 1, map[int64][]int64{1: {99}}, structs, nil, 3)
	if len(st.methods) != 1 {
		t.Errorf("unknown embed target must contribute nothing, got %v", st.methods)
	}
}
