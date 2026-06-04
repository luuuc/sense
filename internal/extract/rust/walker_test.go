package rust

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/model"
)

// parseRust parses src and returns the tree root, source bytes, and a
// cleanup func. Branch tests that drive type helpers directly use it to
// reach the node-kind cases the goldens don't exercise.
func parseRust(t *testing.T, src string) (*sitter.Node, []byte, func()) {
	t.Helper()
	p := sitter.NewParser()
	if err := p.SetLanguage(Extractor{}.Grammar()); err != nil {
		p.Close()
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		p.Close()
		t.Fatal("Parse returned nil tree")
	}
	return tree.RootNode(), source, func() { tree.Close(); p.Close() }
}

// firstNamed returns the first node of the given kind in a pre-order walk
// of n (n itself included), or nil if none.
func firstNamed(n *sitter.Node, kind string) *sitter.Node {
	if n == nil {
		return nil
	}
	if n.Kind() == kind {
		return n
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if found := firstNamed(n.NamedChild(i), kind); found != nil {
			return found
		}
	}
	return nil
}

// rustErrorSweepSource exercises every emitting handler: struct + derive +
// composing fields, enum + variant compositions, trait + methods, const,
// static, free function + call, impl Trait for Type (inherits + method +
// self-call), an inherent impl, and a nested module.
const rustErrorSweepSource = `
use std::collections::HashMap;

#[derive(Debug, Clone)]
pub struct Account {
    owner: Customer,
    tags: Vec<Label>,
    parent: Box<Account>,
    meta: (Key, Value),
}

pub enum Status {
    Active(Session),
    Closed { reason: Reason },
}

pub trait Processor {
    fn process(&self);
    fn reset(&self) {}
}

pub const MAX: u32 = 100;
pub static GLOBAL: u32 = 1;

pub fn run() {
    helper();
}

impl Processor for Account {
    fn process(&self) {
        self.validate();
    }
}

impl Account {
    fn validate(&self) {
        check();
    }
}

pub mod inner {
    pub fn nested() {}
    pub struct Widget;
}
`

// TestRustPropagatesEmitterErrors drives the extractor through a failing
// emitter at every budget below the total emission count. Every emission
// point must surface the first emitter error (faithful propagation), which
// is what reaches each handler's `if err := emit(...); err != nil` branch.
func TestRustPropagatesEmitterErrors(t *testing.T) {
	r := parse(t, rustErrorSweepSource)
	nSym, nEdg := len(r.symbols), len(r.edges)
	if nSym == 0 || nEdg == 0 {
		t.Fatalf("sweep source emitted %d symbols / %d edges; both must be non-zero", nSym, nEdg)
	}
	for budget := 0; budget < nSym; budget++ {
		if err := parseWithEmitter(t, rustErrorSweepSource, &failAfterN{symbolsLeft: budget, edgesLeft: 1 << 20}); err == nil {
			t.Errorf("symbol budget %d: expected error, got nil", budget)
		}
	}
	for budget := 0; budget < nEdg; budget++ {
		if err := parseWithEmitter(t, rustErrorSweepSource, &failAfterN{symbolsLeft: 1 << 20, edgesLeft: budget}); err == nil {
			t.Errorf("edge budget %d: expected error, got nil", budget)
		}
	}
	if err := parseWithEmitter(t, rustErrorSweepSource, &failAfterN{symbolsLeft: 1 << 20, edgesLeft: 1 << 20}); err != nil {
		t.Errorf("generous budget: unexpected error %v", err)
	}
}

// TestUnwrapTypeName covers every node-kind case of the type-name
// unwrapper, including the wrappers it must descend through and the shapes
// that resolve to no name.
func TestUnwrapTypeName(t *testing.T) {
	cases := []struct {
		name string
		src  string
		kind string
		want string
	}{
		{"plain", `type A = Foo;`, "type_identifier", "Foo"},
		{"generic", `type A = Vec<Foo>;`, "generic_type", "Vec"},
		{"reference", `type A = &Foo;`, "reference_type", "Foo"},
		{"scoped", `type A = std::Foo;`, "scoped_type_identifier", "Foo"},
		{"primitive", `type A = u32;`, "primitive_type", ""},
		{"tuple", `type A = (u8, u8);`, "tuple_type", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, source, done := parseRust(t, tc.src)
			defer done()
			// Locate the type node inside the type_item's value field.
			ti := firstNamed(root, "type_item")
			if ti == nil {
				t.Fatal("no type_item")
			}
			node := ti.ChildByFieldName("type")
			if node == nil || node.Kind() != tc.kind {
				t.Fatalf("value kind = %v, want %s", node, tc.kind)
			}
			if got := unwrapTypeName(node, source); got != tc.want {
				t.Errorf("unwrapTypeName = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestUnwrapTypeNameNilInner covers the guard branches where a wrapper
// type has no inner type node (a malformed generic/reference): the
// unwrapper bottoms out at the empty name rather than dereferencing nil.
func TestUnwrapTypeNameNilInner(t *testing.T) {
	// `type A = Vec;` parses the bare `Vec` as a type_identifier (not a
	// generic_type), so to reach the nil-inner guard we drive unwrap on a
	// node kind that lacks the field. A scoped_type_identifier without a
	// name field is unreachable via real source; the generic/reference
	// nil-inner guards are defensive. Drive the reachable empty cases.
	root, source, done := parseRust(t, `type A = ();`)
	defer done()
	ti := firstNamed(root, "type_item")
	node := ti.ChildByFieldName("type")
	if got := unwrapTypeName(node, source); got != "" {
		t.Errorf("unwrapTypeName(unit) = %q, want \"\"", got)
	}
}

// TestResolveComposeTargetsShapes drives the compose-target resolver
// across the type shapes a field can carry: a user type, a primitive
// (skipped), a wrapper unwrapped to its argument, a reference, a tuple,
// and a non-composing default.
func TestResolveComposeTargetsShapes(t *testing.T) {
	cases := []struct {
		src  string
		kind string
		want []string
	}{
		{`type A = Foo;`, "type_identifier", []string{"Foo"}},
		{`type A = u32;`, "primitive_type", nil},
		{`type A = Vec<Foo>;`, "generic_type", []string{"Foo"}},
		{`type A = Result<Foo, Bar>;`, "generic_type", []string{"Foo", "Bar"}},
		{`type A = &Foo;`, "reference_type", []string{"Foo"}},
		{`type A = (Foo, Bar);`, "tuple_type", []string{"Foo", "Bar"}},
		{`type A = std::Foo;`, "scoped_type_identifier", []string{"Foo"}},
	}
	w := &walker{source: nil}
	for _, tc := range cases {
		root, source, done := parseRust(t, tc.src)
		w.source = source
		ti := firstNamed(root, "type_item")
		node := ti.ChildByFieldName("type")
		if node == nil || node.Kind() != tc.kind {
			done()
			t.Fatalf("%q: value kind = %v, want %s", tc.src, node, tc.kind)
		}
		got := w.resolveComposeTargets(node)
		if !equalStrings(got, tc.want) {
			t.Errorf("resolveComposeTargets(%q) = %v, want %v", tc.src, got, tc.want)
		}
		done()
	}
}

// TestResolveComposeTargetsNil confirms a nil type node composes nothing.
func TestResolveComposeTargetsNil(t *testing.T) {
	w := &walker{}
	if got := w.resolveComposeTargets(nil); got != nil {
		t.Errorf("resolveComposeTargets(nil) = %v, want nil", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newRustWalker builds a walker over source wired to a fresh recorder.
func newRustWalker(source []byte) (*walker, *recorder) {
	r := &recorder{}
	return &walker{
		source:       source,
		emit:         r,
		traitMethods: map[string]map[string]bool{},
		typeTraits:   map[string][]string{},
	}, r
}

// TestRustHandlerNameGuards drives the symbol handlers with a node that
// has no `name` (or `type`/`trait`) field — the defensive returns that
// keep a malformed item from emitting a nameless symbol or panicking. A
// well-formed parse never reaches them, so they are exercised directly.
func TestRustHandlerNameGuards(t *testing.T) {
	root, source, done := parseRust(t, `const X: u32 = 1;`)
	defer done()
	leaf := firstNamed(root, "integer_literal")
	if leaf == nil {
		t.Fatal("no integer_literal node")
	}
	w, r := newRustWalker(source)

	check := func(name string, err error) {
		if err != nil {
			t.Errorf("%s returned %v, want nil", name, err)
		}
	}
	check("handleTypeDef", w.handleTypeDef(leaf, nil, model.KindClass))
	check("handleConstItem", w.handleConstItem(leaf, nil))
	check("handleFunction", w.handleFunction(leaf, nil))
	check("handleMod", w.handleMod(leaf, nil))
	check("handleImpl", w.handleImpl(leaf, nil))
	check("emitTraitMethods", w.emitTraitMethods(leaf, "X"))
	check("emitFieldCompositions", w.emitFieldCompositions(leaf, "X"))
	check("emitEnumVariantCompositions", w.emitEnumVariantCompositions(leaf, "X"))
	// The collect* helpers are void; they must not panic on a leaf.
	w.collectTraitMethods(leaf, nil)
	w.collectImplTraits(leaf, nil)
	w.collectDeriveTraits(leaf, nil)
	w.preCollect(leaf, nil)

	if len(r.symbols) != 0 || len(r.edges) != 0 {
		t.Errorf("guards emitted %d symbols / %d edges, want 0/0", len(r.symbols), len(r.edges))
	}
}

// TestResolveTraitMethodAmbiguous confirms that when two implemented
// traits declare the same method, resolution is ambiguous and returns ""
// (the caller then falls back to inherent resolution).
func TestResolveTraitMethodAmbiguous(t *testing.T) {
	w, _ := newRustWalker(nil)
	w.typeTraits["T"] = []string{"A", "B"}
	w.traitMethods["A"] = map[string]bool{"m": true}
	w.traitMethods["B"] = map[string]bool{"m": true}
	if got := w.resolveTraitMethod("T", "m"); got != "" {
		t.Errorf("ambiguous resolveTraitMethod = %q, want \"\"", got)
	}
	// A single declaring trait resolves unambiguously.
	w.typeTraits["U"] = []string{"A"}
	if got := w.resolveTraitMethod("U", "m"); got != "A::m" {
		t.Errorf("resolveTraitMethod = %q, want A::m", got)
	}
}

// TestDerivePathSegmentSkipped confirms a path-qualified derive
// (`#[derive(a::B)]`) inherits the trailing trait name, skipping the path
// segment before `::`.
func TestDerivePathSegmentSkipped(t *testing.T) {
	r := parse(t, `#[derive(a::B)]
pub struct S {
    x: u32,
}
`)
	if findEdge(r, "S", "B", "inherits") == nil {
		t.Error("missing derive inherits edge S -> B")
	}
	if findEdge(r, "S", "a", "inherits") != nil {
		t.Error("unexpected inherits edge to path segment 'a'")
	}
}

// TestRestrictedVisibilityIsPrivate confirms a pub(crate) item is treated
// as private (Rust's true export boundary is the crate).
func TestRestrictedVisibilityIsPrivate(t *testing.T) {
	r := parse(t, `pub(crate) struct Internal {
    x: u32,
}
`)
	s := findSymbol(r, "Internal")
	if s == nil {
		t.Fatal("missing symbol Internal")
	}
	if s.Visibility != "private" {
		t.Errorf("pub(crate) Internal visibility = %q, want private", s.Visibility)
	}
}

// TestDispatchAndCallShapes exercises the call-resolution and dispatch
// branches: an Any downcast turbofish (field and bare callee), a
// non-downcast generic call, a non-self method call inside an impl, and a
// scoped-path call.
func TestDispatchAndCallShapes(t *testing.T) {
	r := parse(t, `use std::any::Any;

pub trait Shape {}

impl Account {
    fn run(&self, other: &Helper) {
        let s = self.value.downcast_ref::<Circle>();
        let t = downcast::<Square>();
        let u = make::<Plain>();
        other.assist();
        helpers::log();
    }
}
`)
	// A non-self method call resolves to its surface text.
	if findEdge(r, "Account::run", "other.assist", "calls") == nil {
		t.Error("missing calls edge for non-self method call other.assist")
	}
	// A scoped-path call resolves to its full path text.
	if findEdge(r, "Account::run", "helpers::log", "calls") == nil {
		t.Error("missing calls edge for scoped call helpers::log")
	}
}

// TestNonDeriveAttributeIgnored confirms a non-derive attribute preceding
// a type (e.g. #[inline]) is skipped by the derive walk, while a real
// derive on the same item is still recorded.
func TestNonDeriveAttributeIgnored(t *testing.T) {
	r := parse(t, `#[repr(C)]
#[derive(Clone)]
pub struct Packed {
    x: u32,
}
`)
	if findEdge(r, "Packed", "Clone", "inherits") == nil {
		t.Error("missing derive inherits edge Packed -> Clone")
	}
	if findEdge(r, "Packed", "C", "inherits") != nil {
		t.Error("unexpected inherits edge from non-derive #[repr(C)]")
	}
}

// TestEmitCallNilFunction confirms emitCall no-ops on a call node with no
// function field (a defensive guard a well-formed parse never reaches).
func TestEmitCallNilFunction(t *testing.T) {
	root, source, done := parseRust(t, `const X: u32 = 1;`)
	defer done()
	leaf := firstNamed(root, "integer_literal")
	w, r := newRustWalker(source)
	if err := w.emitCall(leaf, "S", ""); err != nil {
		t.Errorf("emitCall(leaf) = %v, want nil", err)
	}
	if len(r.edges) != 0 {
		t.Errorf("emitCall(leaf) emitted %d edges, want 0", len(r.edges))
	}
}
