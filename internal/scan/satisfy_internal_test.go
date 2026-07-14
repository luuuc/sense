package scan

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

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
	methods := map[string]arity{"Read": {}, "Write": {}, "Close": {}}

	if !methodSetSatisfies(methods, map[string]arity{"Read": {}, "Write": {}}) {
		t.Error("should satisfy subset")
	}
	if !methodSetSatisfies(methods, map[string]arity{"Read": {}, "Write": {}, "Close": {}}) {
		t.Error("should satisfy exact set")
	}
	if methodSetSatisfies(methods, map[string]arity{"Read": {}, "Flush": {}}) {
		t.Error("should not satisfy with missing method")
	}
	if !methodSetSatisfies(methods, nil) {
		t.Error("nil required should satisfy")
	}
}

func TestPromoteEmbeddedMethods(t *testing.T) {
	outer := &structInfo{methods: map[string]arity{"Own": {}}}
	inner := &structInfo{methods: map[string]arity{"Read": {}, "Write": {}}}

	structs := map[int64]*structInfo{
		1: outer,
		2: inner,
	}
	embeddings := map[int64][]int64{
		1: {2},
	}

	promoteEmbeddedMethods(outer, 1, map[int64]map[string]bool{}, embeddings, structs, nil, 3)

	if !hasMethod(outer.methods, "Read") {
		t.Error("expected Read promoted from embedded struct")
	}
	if !hasMethod(outer.methods, "Write") {
		t.Error("expected Write promoted from embedded struct")
	}
	if !hasMethod(outer.methods, "Own") {
		t.Error("expected Own to remain")
	}
}

func TestPromoteEmbeddedMethodsDepthLimit(t *testing.T) {
	a := &structInfo{methods: map[string]arity{}}
	b := &structInfo{methods: map[string]arity{}}
	c := &structInfo{methods: map[string]arity{"Deep": {}}}

	structs := map[int64]*structInfo{1: a, 2: b, 3: c}
	embeddings := map[int64][]int64{1: {2}, 2: {3}}

	promoteEmbeddedMethods(a, 1, map[int64]map[string]bool{}, embeddings, structs, nil, 1)

	// Depth=1 means we only go one hop. b's methods get promoted,
	// but c's methods should not (they need depth=2).
	if hasMethod(a.methods, "Deep") {
		t.Error("expected depth limit to prevent promoting from 2 hops away")
	}
}

// TestExpandInterfaceMethodSets proves the interface-side closure: a chain of
// embedded interfaces unions transitively (no depth cap — a truncated
// expansion would shrink required sets and re-create false satisfaction),
// diamonds dedupe, and unknown targets contribute nothing.
func TestExpandInterfaceMethodSets(t *testing.T) {
	interfaces := map[int64]*ifaceInfo{
		1: {methods: map[string]arity{"A": {}}},
		2: {methods: map[string]arity{"B": {}}},
		3: {methods: map[string]arity{}},
		4: {methods: map[string]arity{"D": {}}},
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
		if !hasMethod(interfaces[3].methods, m) {
			t.Errorf("chain: interface 3 missing %s after expansion", m)
		}
	}
	if len(interfaces[4].methods) != 3 { // A, B, D — diamond dedupes
		t.Errorf("diamond: expected 3 methods on interface 4, got %v", interfaces[4].methods)
	}
	if !hasMethod(interfaces[2].methods, "A") || len(interfaces[2].methods) != 2 {
		t.Errorf("unknown target must contribute nothing: %v", interfaces[2].methods)
	}
}

// TestExpandInterfaceMethodSetsArityConflict pins the diamond-disagreement
// rule: the same member name arriving from two embedded interfaces with
// DIFFERENT arities collapses to UNKNOWN — map order never decides, and the
// conservative direction (keep the edge) holds.
func TestExpandInterfaceMethodSetsArityConflict(t *testing.T) {
	k := func(p, r int) arity { return arity{params: p, results: r, known: true} }
	interfaces := map[int64]*ifaceInfo{
		1: {methods: map[string]arity{"Close": k(0, 1)}},
		2: {methods: map[string]arity{"Close": k(1, 1)}},
		3: {methods: map[string]arity{}},
	}
	embeddings := map[int64][]int64{3: {1, 2}}
	expandInterfaceMethodSets(interfaces, embeddings)
	got, ok := interfaces[3].methods["Close"]
	if !ok {
		t.Fatal("Close must be present after expansion")
	}
	if got.known {
		t.Errorf("conflicting arities must collapse to UNKNOWN, got %+v", got)
	}
}

// TestExpandInterfaceMethodSetsCycle proves termination on an embedding cycle
// (illegal Go, but the index can contain mid-edit or misresolved code).
func TestExpandInterfaceMethodSetsCycle(t *testing.T) {
	interfaces := map[int64]*ifaceInfo{
		1: {methods: map[string]arity{"A": {}}},
		2: {methods: map[string]arity{"B": {}}},
	}
	embeddings := map[int64][]int64{1: {2}, 2: {1}}
	expandInterfaceMethodSets(interfaces, embeddings) // must terminate
	if !hasMethod(interfaces[1].methods, "B") {
		t.Error("cycle: interface 1 should still union interface 2's methods")
	}
}

// TestPromoteThroughEmbeddedInterface proves the struct-side path for an
// embedded interface VALUE (the pageState shape): the interface's expanded
// method set delegates wholesale onto the struct.
func TestPromoteThroughEmbeddedInterface(t *testing.T) {
	iface := &ifaceInfo{methods: map[string]arity{"A": {}, "B": {}}}
	st := &structInfo{methods: map[string]arity{"Own": {}}}
	structs := map[int64]*structInfo{1: st}
	interfaces := map[int64]*ifaceInfo{7: iface}
	embeddings := map[int64][]int64{1: {7}}

	promoteEmbeddedMethodSets(structs, interfaces, embeddings)

	for _, m := range []string{"A", "B", "Own"} {
		if !hasMethod(st.methods, m) {
			t.Errorf("struct missing %s after embedded-interface promotion", m)
		}
	}
}

// satisfyFakeStore drives satisfyInterfaces past classification with an
// in-memory symbol set, then lets each test override one downstream call.
type satisfyFakeStore struct {
	*sqlite.Adapter
	syms     []model.Symbol
	edges    []model.Edge
	edgesErr error
	writeErr error
}

func (f *satisfyFakeStore) FileIDsByLanguage(context.Context, string) (map[int64]bool, error) {
	return map[int64]bool{1: true}, nil
}

func (f *satisfyFakeStore) Query(context.Context, index.Filter) ([]model.Symbol, error) {
	if f.syms != nil {
		return f.syms, nil
	}
	iface := int64(1)
	return []model.Symbol{
		{ID: 1, Name: "I", Kind: model.KindInterface, FileID: 1},
		{ID: 2, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: &iface},
		{ID: 3, Name: "S", Kind: model.KindClass, FileID: 1},
	}, nil
}

func (f *satisfyFakeStore) EdgesOfKind(context.Context, model.EdgeKind) ([]model.Edge, error) {
	return f.edges, f.edgesErr
}

func (f *satisfyFakeStore) InTx(_ context.Context, fn func() error) error { return fn() }

func (f *satisfyFakeStore) WriteEdge(context.Context, *model.Edge) (int64, error) {
	return 0, f.writeErr
}

// TestSatisfyInterfacesOverBudgetWrites is the G-2 behavior gate: a repo whose
// interface×struct product would have blown the old 500K budget still gets its
// satisfaction edges, silently (no skip warning). S has method M so it
// satisfies I; the 600K method-less filler structs satisfy nothing.
func TestSatisfyInterfacesOverBudgetWrites(t *testing.T) {
	var warn bytes.Buffer
	structID := int64(3)
	f := &recordingSatisfyStore{satisfyFakeStore: satisfyFakeStore{Adapter: newOpenAdapter(t)}}
	f.syms = []model.Symbol{
		{ID: 1, Name: "I", Kind: model.KindInterface, FileID: 1},
		{ID: 2, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: func() *int64 { i := int64(1); return &i }()},
		{ID: 3, Name: "S", Kind: model.KindClass, FileID: 1},
		{ID: 4, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: &structID},
	}
	for i := int64(0); i < 600_000; i++ {
		f.syms = append(f.syms, model.Symbol{ID: 100 + i, Kind: model.KindClass, FileID: 1})
	}
	h := &harness{ctx: context.Background(), idx: f, out: io.Discard, warn: &warn}
	if err := h.satisfyInterfaces(); err != nil {
		t.Fatalf("over-budget pass must not error: %v", err)
	}
	if len(f.written) != 1 {
		t.Fatalf("want exactly 1 satisfaction edge over budget, got %d", len(f.written))
	}
	if warn.Len() != 0 {
		t.Errorf("no skip warning may remain, got %q", warn.String())
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
	st := &structInfo{methods: map[string]arity{"Own": {}}}
	structs := map[int64]*structInfo{1: st}
	promoteEmbeddedMethods(st, 1, map[int64]map[string]bool{}, map[int64][]int64{1: {99}}, structs, nil, 3)
	if len(st.methods) != 1 {
		t.Errorf("unknown embed target must contribute nothing, got %v", st.methods)
	}
}

// mkStruct builds a structInfo with the given symbol id and method names.
func mkStruct(id int64, methods ...string) *structInfo {
	st := &structInfo{sym: model.Symbol{ID: id, FileID: 1}, methods: map[string]arity{}}
	for _, m := range methods {
		st.methods[m] = arity{}
	}
	return st
}

// hasMethod reports name membership in an arity method set (test helper for
// the pre-arity boolean assertions).
func hasMethod(methods map[string]arity, name string) bool {
	_, ok := methods[name]
	return ok
}

// TestIndexStructMethods pins the bucket build: every struct appears in the
// bucket of each of its methods, exactly once, and buckets are sorted by
// symbol ID so downstream edge writes are deterministic.
func TestIndexStructMethods(t *testing.T) {
	structs := map[int64]*structInfo{
		2: mkStruct(2, "Read", "Close"),
		1: mkStruct(1, "Read"),
		3: mkStruct(3, "Write"),
	}
	buckets := indexStructMethods(structs)
	if got := len(buckets["Read"]); got != 2 {
		t.Fatalf("Read bucket: want 2 structs, got %d", got)
	}
	if buckets["Read"][0].sym.ID != 1 || buckets["Read"][1].sym.ID != 2 {
		t.Errorf("Read bucket must be sorted by symbol ID, got %d,%d",
			buckets["Read"][0].sym.ID, buckets["Read"][1].sym.ID)
	}
	if len(buckets["Close"]) != 1 || len(buckets["Write"]) != 1 {
		t.Errorf("Close/Write buckets wrong: %d/%d", len(buckets["Close"]), len(buckets["Write"]))
	}
	if _, ok := buckets["Flush"]; ok {
		t.Error("no struct has Flush; bucket must be absent")
	}
}

// TestCandidateStructs pins the pruning rule: candidates are EXACTLY the
// smallest bucket among the required methods (kills the largest-bucket
// mutant), and a required method with no bucket short-circuits to zero
// candidates — falling through to any other bucket would emit false 0.9
// satisfaction edges for interfaces nothing implements.
func TestCandidateStructs(t *testing.T) {
	structs := map[int64]*structInfo{
		1: mkStruct(1, "Read", "Close"),
		2: mkStruct(2, "Read"),
		3: mkStruct(3, "Read", "Close", "Rare"),
	}
	buckets := indexStructMethods(structs)

	// Rare's bucket (1 struct) is strictly smaller than Read's (3) and Close's (2).
	got := candidateStructs(map[string]arity{"Read": {}, "Close": {}, "Rare": {}}, buckets)
	if len(got) != 1 || got[0].sym.ID != 3 {
		t.Fatalf("candidates must be exactly the smallest (Rare) bucket, got %d structs", len(got))
	}

	// A required method with NO bucket anywhere → zero candidates, immediately.
	if got := candidateStructs(map[string]arity{"Read": {}, "Missing": {}}, buckets); got != nil {
		t.Fatalf("missing bucket must short-circuit to zero candidates, got %d", len(got))
	}
}

// recordingSatisfyStore extends satisfyFakeStore to capture written edges so
// full-pass tests can compare the emitted set against an oracle.
type recordingSatisfyStore struct {
	satisfyFakeStore
	written []model.Edge
}

func (r *recordingSatisfyStore) WriteEdge(_ context.Context, e *model.Edge) (int64, error) {
	r.written = append(r.written, *e)
	return int64(len(r.written)), nil
}

// satisfyPairs runs the full satisfyInterfaces pass over the given symbols and
// includes edges, returning the emitted (struct,interface) pairs.
func satisfyPairs(t *testing.T, syms []model.Symbol, includes []model.Edge) map[[2]int64]bool {
	t.Helper()
	f := &recordingSatisfyStore{satisfyFakeStore: satisfyFakeStore{Adapter: newOpenAdapter(t), syms: syms, edges: includes}}
	h := &harness{ctx: context.Background(), idx: f, out: io.Discard, warn: io.Discard}
	if err := h.satisfyInterfaces(); err != nil {
		t.Fatalf("satisfyInterfaces: %v", err)
	}
	pairs := map[[2]int64]bool{}
	for _, e := range f.written {
		pairs[[2]int64{*e.SourceID, e.TargetID}] = true
	}
	return pairs
}

// TestSatisfyDifferentialOracle is the correctness anchor for the candidate
// pruning: the emitted edge set must equal a naive all-pairs oracle computed
// with the untouched methodSetSatisfies predicate. The fixture family is
// chosen to kill the mutants the pruning could hide:
//   - iface 10 (Read+Close): its smallest bucket is Close {1,4,5,6} (Read has
//     five members thanks to struct 7), and struct 6 sits IN that bucket while
//     lacking Read — a candidate that must FAIL the re-check (kills
//     skip-re-check; verified by running the mutant, per council pass 2);
//   - iface 11 (Rare): served from a 2-struct bucket; struct 3 satisfies it
//     while never being a candidate for iface 10 (not in the Close bucket);
//   - iface 12 (Ghost): required method exists on NO struct — zero edges;
//   - struct 4 satisfies iface 10 ONLY through methods promoted from its
//     embedded struct 5 (kills index-built-before-promotion).
func TestSatisfyDifferentialOracle(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	syms := []model.Symbol{
		{ID: 10, Name: "IReadCloser", Kind: model.KindInterface, FileID: 1},
		{ID: 20, Name: "Read", Kind: model.KindMethod, FileID: 1, ParentID: pid(10)},
		{ID: 21, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(10)},
		{ID: 11, Name: "IRare", Kind: model.KindInterface, FileID: 1},
		{ID: 22, Name: "Rare", Kind: model.KindMethod, FileID: 1, ParentID: pid(11)},
		{ID: 12, Name: "IGhost", Kind: model.KindInterface, FileID: 1},
		{ID: 23, Name: "Ghost", Kind: model.KindMethod, FileID: 1, ParentID: pid(12)},

		{ID: 1, Name: "Full", Kind: model.KindClass, FileID: 1},
		{ID: 30, Name: "Read", Kind: model.KindMethod, FileID: 1, ParentID: pid(1)},
		{ID: 31, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(1)},
		{ID: 32, Name: "Rare", Kind: model.KindMethod, FileID: 1, ParentID: pid(1)},
		{ID: 3, Name: "HasRareLacksClose", Kind: model.KindClass, FileID: 1},
		{ID: 33, Name: "Read", Kind: model.KindMethod, FileID: 1, ParentID: pid(3)},
		{ID: 34, Name: "Rare", Kind: model.KindMethod, FileID: 1, ParentID: pid(3)},
		{ID: 4, Name: "Embedder", Kind: model.KindClass, FileID: 1},
		{ID: 5, Name: "Embedded", Kind: model.KindClass, FileID: 1},
		{ID: 35, Name: "Read", Kind: model.KindMethod, FileID: 1, ParentID: pid(5)},
		{ID: 36, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(5)},
		{ID: 6, Name: "CloseOnly", Kind: model.KindClass, FileID: 1},
		{ID: 37, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(6)},
		{ID: 7, Name: "ReadOnly", Kind: model.KindClass, FileID: 1},
		{ID: 38, Name: "Read", Kind: model.KindMethod, FileID: 1, ParentID: pid(7)},
	}
	// Struct 4 embeds struct 5 (its only route to Read+Close).
	includes := []model.Edge{{SourceID: pid(4), TargetID: 5, Kind: model.EdgeIncludes}}

	got := satisfyPairs(t, syms, includes)

	// Naive oracle over the same classified sets, untouched predicate.
	want := map[[2]int64]bool{
		{1, 10}: true, {1, 11}: true, // Full satisfies IReadCloser and IRare
		{3, 11}: true, // HasRareLacksClose satisfies IRare; never a candidate for 10
		{4, 10}: true, // Embedder: only via promotion from Embedded
		{5, 10}: true, // Embedded satisfies IReadCloser directly
		// CloseOnly (6) and ReadOnly (7) satisfy NOTHING: 6 is the in-bucket
		// candidate the re-check must reject, 7 only pads the Read bucket so
		// Close stays strictly smallest.
	}
	if len(got) != len(want) {
		t.Fatalf("edge set mismatch: got %v want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing edge %v", p)
		}
	}
}

// TestSatisfyUbiquitousMethodInterfaces pins the degenerate CPU shape: many
// one-method interfaces sharing a ubiquitous method. Every candidate is a true
// satisfier, so the "cost" is exactly the correct output, not wasted checks.
func TestSatisfyUbiquitousMethodInterfaces(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	var syms []model.Symbol
	const nIfaces, nStructs = 5, 4
	for i := int64(0); i < nIfaces; i++ {
		id := 10 + i
		syms = append(syms,
			model.Symbol{ID: id, Name: "I", Kind: model.KindInterface, FileID: 1},
			model.Symbol{ID: 100 + i, Name: "Reset", Kind: model.KindMethod, FileID: 1, ParentID: pid(id)})
	}
	for s := int64(0); s < nStructs; s++ {
		id := 50 + s
		syms = append(syms,
			model.Symbol{ID: id, Name: "S", Kind: model.KindClass, FileID: 1},
			model.Symbol{ID: 200 + s, Name: "Reset", Kind: model.KindMethod, FileID: 1, ParentID: pid(id)})
	}
	got := satisfyPairs(t, syms, nil)
	if len(got) != nIfaces*nStructs {
		t.Fatalf("ubiquitous shape: want %d edges (all true), got %d", nIfaces*nStructs, len(got))
	}
}

// TestParseMethodArity is the parser's input contract (council pass 1, Beck's
// matrix): every construct the walker models parses to exact slot counts;
// every construct it does not model returns UNKNOWN — never a miscount. The
// two-shape equivalence rows are load-bearing: interface members and
// receiver-form declarations of the same signature MUST parse equal.
func TestParseMethodArity(t *testing.T) {
	known := func(p, r int) arity { return arity{params: p, results: r, known: true} }
	unknown := arity{}
	cases := []struct {
		name, snippet string
		want          arity
	}{
		{"iface empty both", "Close()", known(0, 0)},
		{"recv empty both", "func (r *T) Close() {", known(0, 0)},
		{"iface bare return", "Close() error", known(0, 1)},
		{"recv bare return", "func (r *T) Close() error {", known(0, 1)},
		{"iface tuple return", "Next() (*CommitMeta, error)", known(0, 2)},
		{"parenthesized single return", "Sum() (int)", known(0, 1)},
		{"grouped params", "Set(key, value []byte, o *WriteOptions)", known(3, 0)},
		{"func-typed param", "Iterate(ctx context.Context, cb func(k, v []byte) error)", known(2, 0)},
		{"func-typed return", "Handler() func(a, b int) bool", known(0, 1)},
		{"variadic", "Printf(format string, args ...interface{})", known(2, 0)},
		{"generic tuple return", "Next(ctx context.Context) (K, V, error)", known(1, 3)},
		{"generic receiver", "func (m *Map[K, V]) Get(k K) (V, bool) {", known(1, 2)},
		{"bracket-comma param", "Do(p Pair[K, V])", known(1, 0)},
		{"brace-comma param", "Apply(s struct{ a, b int })", known(1, 0)},
		{"map return", "Snapshot() map[string]int", known(0, 1)},
		{"one-line body literal commas", "func (t T) Pair() point { return point{1, 2} }", known(0, 1)},
		{"the flagship junk pair A", "Close(ctx context.Context) error", known(1, 1)},
		{"truncated multi-line", "NewIterator(ctx context.Context,", unknown},
		{"empty snippet", "", unknown},
		{"backquote struct tag", "Parse(v struct{ A int `json:\"a,b\"` })", unknown},
		{"no param group", "type Closer interface {", unknown},
		{"leading paren no name", "(x int) error", unknown},
		{"receiver group unbalanced", "func (r *T[K,", unknown},
		{"returns group truncated", "Foo() (int,", unknown},
		{"empty parens returns", "Foo() ()", known(0, 0)},
		{"func keyword bare (no receiver)", "func Standalone(a int) error {", known(1, 1)},
		{"receiver without space", "func(r *T) Close() error {", known(0, 1)},
		{"receiver after tab", "func\t(r *T) Close() error {", known(0, 1)},
	}
	for _, c := range cases {
		if got := parseMethodArity(c.snippet); got != c.want {
			t.Errorf("%s: parseMethodArity(%q) = %+v, want %+v", c.name, c.snippet, got, c.want)
		}
	}

	// The 200-byte cap: a single-line declaration at or over snippetMaxBytes
	// may be truncated into a BALANCED wrong parse — cap-length input is
	// always UNKNOWN.
	// BALANCED at the cap: the cut lands exactly after the params' closing
	// paren, so without the length guard this parses cleanly as (1,0) while
	// the real declaration carried results — the false-clean-parse hole.
	balanced := "F(" + strings.Repeat("a", snippetMaxBytes-3) + ")"
	if len(balanced) != snippetMaxBytes {
		t.Fatalf("fixture must sit exactly at the cap, got %d", len(balanced))
	}
	if got := parseMethodArity(balanced); got != unknown {
		t.Errorf("cap-length snippet must be UNKNOWN even when balanced, got %+v", got)
	}
	// And an unbalanced truncation for the ordinary multi-line path.
	long := "LongOne(" + strings.Repeat("a int, ", 40) + "b int)"
	if got := parseMethodArity(long[:snippetMaxBytes]); got != unknown {
		t.Errorf("cap-length truncation must be UNKNOWN, got %+v", got)
	}

	// Two-shape equivalence (the load-bearing rows, asserted pairwise).
	pairs := [][2]string{
		{"Close() error", "func (r *reader) Close() error {"},
		{"Get(k K) (V, bool)", "func (m *Map[K, V]) Get(k K) (V, bool) {"},
		{"Set(key, value []byte, o *WriteOptions) error", "func (b *Batch) Set(key, value []byte, o *WriteOptions) error {"},
	}
	for _, p := range pairs {
		a, b := parseMethodArity(p[0]), parseMethodArity(p[1])
		if !a.known || !b.known || a != b {
			t.Errorf("shape equivalence: %q=%+v vs %q=%+v", p[0], a, p[1], b)
		}
	}
}

// TestArityCompatible pins the asymmetry: UNKNOWN on either side keeps the
// name-only fallback; both known require equality on BOTH params and results.
func TestArityCompatible(t *testing.T) {
	k := func(p, r int) arity { return arity{params: p, results: r, known: true} }
	u := arity{}
	if !u.compatible(k(1, 1)) || !k(1, 1).compatible(u) || !u.compatible(u) {
		t.Error("UNKNOWN must always be compatible (keep the edge)")
	}
	if !k(2, 1).compatible(k(2, 1)) {
		t.Error("equal arities must be compatible")
	}
	if k(2, 1).compatible(k(1, 1)) {
		t.Error("params-differ must be incompatible")
	}
	if k(2, 1).compatible(k(2, 2)) {
		t.Error("results-differ must be incompatible (returns are compared, not just params)")
	}
}

// TestSatisfyArityThroughFullPass extends the differential oracle with real
// snippets: arity now decides where names collide. Iface 40 requires
// Write(p []byte) (int, error); struct 8 declares it exactly (edge), struct 9
// declares Write() (a name-only match that MUST drop — the F-31-09a junk
// class), struct 10's declaration is a truncated multi-line snippet (UNKNOWN
// → name-only keep, the conservative fallback). The not-UNKNOWN pin: struct
// 8's parse must be a concrete arity, so this test FAILS the day snippet
// shape drifts and the junk quietly returns.
func TestSatisfyArityThroughFullPass(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	syms := []model.Symbol{
		{ID: 40, Name: "IWriter", Kind: model.KindInterface, FileID: 1},
		{ID: 41, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(40),
			Snippet: "Write(p []byte) (int, error)"},
		{ID: 8, Name: "GoodWriter", Kind: model.KindClass, FileID: 1},
		{ID: 42, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(8),
			Snippet: "func (w *GoodWriter) Write(p []byte) (int, error) {"},
		{ID: 9, Name: "JunkWriter", Kind: model.KindClass, FileID: 1},
		{ID: 43, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(9),
			Snippet: "func (w *JunkWriter) Write() {"},
		{ID: 10, Name: "TruncWriter", Kind: model.KindClass, FileID: 1},
		{ID: 44, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(10),
			Snippet: "func (w *TruncWriter) Write(p []byte,"},
	}
	got := satisfyPairs(t, syms, nil)
	want := map[[2]int64]bool{
		{8, 40}:  true, // exact arity match
		{10, 40}: true, // UNKNOWN keeps (truncated declaration)
	}
	if len(got) != len(want) {
		t.Fatalf("edge set mismatch: got %v want %v", got, want)
	}
	for p := range want {
		if !got[p] {
			t.Errorf("missing edge %v", p)
		}
	}
	if got[[2]int64{9, 40}] {
		t.Error("name-only junk (Write() vs Write(p []byte) (int, error)) must drop")
	}

	// the not-UNKNOWN contract pin: the interface member's snippet must parse
	// to a concrete arity — if snippet shape ever drifts, this reds.
	if a := parseMethodArity("Write(p []byte) (int, error)"); !a.known || a.params != 1 || a.results != 2 {
		t.Errorf("snippet contract drifted: %+v", a)
	}
}

// TestSatisfyArityPromotion pins the insert-if-absent threading: a struct
// satisfying only via a promoted embedded method drops on promoted-arity
// MISMATCH and survives on match; an embedder's own declaration shadows an
// embedded one with different arity (Go semantics), order-proof.
func TestSatisfyArityPromotion(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	syms := []model.Symbol{
		{ID: 50, Name: "ICloser", Kind: model.KindInterface, FileID: 1},
		{ID: 51, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(50),
			Snippet: "Close() error"},
		// Embedder satisfies only via embedded struct whose Close matches.
		{ID: 11, Name: "GoodEmbedder", Kind: model.KindClass, FileID: 1},
		{ID: 12, Name: "GoodInner", Kind: model.KindClass, FileID: 1},
		{ID: 52, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(12),
			Snippet: "func (i *GoodInner) Close() error {"},
		// Embedder whose embedded Close has WRONG arity — must not satisfy.
		{ID: 13, Name: "JunkEmbedder", Kind: model.KindClass, FileID: 1},
		{ID: 14, Name: "JunkInner", Kind: model.KindClass, FileID: 1},
		{ID: 53, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(14),
			Snippet: "func (i *JunkInner) Close(ctx context.Context) error {"},
		// Shadowing: own correct Close + embedded wrong-arity Close — own wins.
		{ID: 15, Name: "Shadower", Kind: model.KindClass, FileID: 1},
		{ID: 54, Name: "Close", Kind: model.KindMethod, FileID: 1, ParentID: pid(15),
			Snippet: "func (s *Shadower) Close() error {"},
	}
	includes := []model.Edge{
		{SourceID: pid(11), TargetID: 12, Kind: model.EdgeIncludes},
		{SourceID: pid(13), TargetID: 14, Kind: model.EdgeIncludes},
		{SourceID: pid(15), TargetID: 14, Kind: model.EdgeIncludes}, // wrong-arity embed shadowed by own
	}
	got := satisfyPairs(t, syms, includes)
	for _, expect := range []struct {
		src  int64
		want bool
		why  string
	}{
		{11, true, "promoted matching arity must satisfy"},
		{12, true, "inner itself satisfies"},
		{13, false, "promoted MISMATCHED arity must not satisfy"},
		{14, false, "junk inner must not satisfy"},
		{15, true, "own declaration shadows the wrong-arity embed"},
	} {
		if got[[2]int64{expect.src, 50}] != expect.want {
			t.Errorf("struct %d → ICloser = %v, want %v (%s)", expect.src, !expect.want, expect.want, expect.why)
		}
	}
}

// TestPromoteSiblingEmbedConflict is Rams's witness (council pass 2): struct S
// embeds A (which embeds B carrying m()) and C (carrying m(int)). Go's depth
// rule resolves S.m to C's m(int) — S genuinely satisfies I{ m(int) }. Sibling
// disagreement must collapse to UNKNOWN (keep the edge), never let embed order
// pick a winner and drop a compile-true satisfier.
func TestPromoteSiblingEmbedConflict(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	syms := []model.Symbol{
		{ID: 60, Name: "IM", Kind: model.KindInterface, FileID: 1},
		{ID: 61, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: pid(60),
			Snippet: "M(x int)"},
		{ID: 20, Name: "S", Kind: model.KindClass, FileID: 1},
		{ID: 21, Name: "A", Kind: model.KindClass, FileID: 1},
		{ID: 22, Name: "B", Kind: model.KindClass, FileID: 1},
		{ID: 62, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: pid(22),
			Snippet: "func (b *B) M() {"},
		{ID: 23, Name: "C", Kind: model.KindClass, FileID: 1},
		{ID: 63, Name: "M", Kind: model.KindMethod, FileID: 1, ParentID: pid(23),
			Snippet: "func (c *C) M(x int) {"},
	}
	includes := []model.Edge{
		{SourceID: pid(20), TargetID: 21, Kind: model.EdgeIncludes}, // S embeds A (first)
		{SourceID: pid(21), TargetID: 22, Kind: model.EdgeIncludes}, // A embeds B
		{SourceID: pid(20), TargetID: 23, Kind: model.EdgeIncludes}, // S embeds C (second)
	}
	got := satisfyPairs(t, syms, includes)
	if !got[[2]int64{20, 60}] {
		t.Error("sibling-embed arity conflict must collapse to UNKNOWN and keep S -> IM")
	}
}

// TestSatisfyComposedInterfaceArity requires arity to survive interface
// EXPANSION: IRW acquires Write(p []byte) (int, error) only through an
// embedded interface. The junk struct's Write() must drop and the true
// implementor must survive — if expansion loses arity, the junk class
// silently returns on every composed interface (the common Go shape).
func TestSatisfyComposedInterfaceArity(t *testing.T) {
	pid := func(i int64) *int64 { return &i }
	syms := []model.Symbol{
		{ID: 70, Name: "IWrite", Kind: model.KindInterface, FileID: 1},
		{ID: 71, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(70),
			Snippet: "Write(p []byte) (int, error)"},
		{ID: 72, Name: "IRW", Kind: model.KindInterface, FileID: 1}, // embeds IWrite only
		{ID: 24, Name: "TrueWriter", Kind: model.KindClass, FileID: 1},
		{ID: 73, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(24),
			Snippet: "func (w *TrueWriter) Write(p []byte) (int, error) {"},
		{ID: 25, Name: "JunkW", Kind: model.KindClass, FileID: 1},
		{ID: 74, Name: "Write", Kind: model.KindMethod, FileID: 1, ParentID: pid(25),
			Snippet: "func (w *JunkW) Write() {"},
	}
	includes := []model.Edge{{SourceID: pid(72), TargetID: 70, Kind: model.EdgeIncludes}}
	got := satisfyPairs(t, syms, includes)
	if !got[[2]int64{24, 72}] {
		t.Error("true implementor must satisfy the composed interface (arity survives expansion)")
	}
	if got[[2]int64{25, 72}] {
		t.Error("junk Write() must not satisfy the composed interface — expansion lost arity")
	}
}

// TestClearSatisfactionEdgesError surfaces the delete failing (closed index):
// the transaction must abort rather than write alongside a failed clear.
func TestClearSatisfactionEdgesError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), out: io.Discard, warn: io.Discard}
	if err := h.clearSatisfactionEdges(); err == nil {
		t.Fatal("expected the clear to surface a closed-index error")
	}
}

// BenchmarkParseMethodArity pins the parser's cost claim: a short forward
// byte-walk, no allocation on the hot path.
func BenchmarkParseMethodArity(b *testing.B) {
	snippets := []string{
		"Close(ctx context.Context) error",
		"func (b *Batch) Set(key, value []byte, o *WriteOptions) error {",
		"func (m *Map[K, V]) Get(k K) (V, bool) {",
		"Iterate(ctx context.Context, cb func(k, v []byte) error)",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		parseMethodArity(snippets[i%len(snippets)])
	}
}
