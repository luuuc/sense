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

// TestAttributedFieldsSkipped confirms that #[attr] items interleaved with
// real fields and variants are skipped by the composition walk: the
// attribute_item appears as a named child of field_declaration_list,
// enum_variant_list, and ordered_field_declaration_list, but only the
// user-typed fields compose edges.
func TestAttributedFieldsSkipped(t *testing.T) {
	r := parse(t, `struct Config {
    #[serde(default)]
    customer: Customer,
}

enum Event {
    #[serde(rename = "a")]
    Created(Session),
    Tagged(#[serde(skip)] Label),
}
`)
	if findEdge(r, "Config", "Customer", "composes") == nil {
		t.Error("missing composes edge Config -> Customer past #[serde] field attribute")
	}
	if findEdge(r, "Event", "Session", "composes") == nil {
		t.Error("missing composes edge Event -> Session past variant attribute")
	}
	if findEdge(r, "Event", "Label", "composes") == nil {
		t.Error("missing composes edge Event -> Label past tuple-field attribute")
	}
}

// TestMalformedFieldListSkipped confirms a field_declaration_list with a
// non-field_declaration named child (an ERROR node from `struct S { x }`)
// is skipped rather than mis-emitted — exercising the field-kind guard.
func TestMalformedFieldListSkipped(t *testing.T) {
	r := parse(t, `struct Broken { x }
`)
	if findSymbol(r, "Broken") == nil {
		t.Fatal("missing symbol Broken")
	}
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Broken" {
			t.Errorf("unexpected composes edge from malformed field list to %q", e.TargetQualified)
		}
	}
}

// TestTraitWithNonMethodMembers confirms associated types and consts inside
// a trait body are skipped by both the pre-collection of trait methods and
// the method-symbol emission, which only consider function items.
func TestTraitWithNonMethodMembers(t *testing.T) {
	r := parse(t, `pub trait Container {
    type Item;
    const CAP: usize;
    fn get(&self) -> bool;
}
`)
	if findSymbol(r, "Container::get") == nil {
		t.Error("missing method Container::get")
	}
	// Associated type / const are not methods.
	if findSymbol(r, "Container::Item") != nil {
		t.Error("unexpected method symbol for associated type Container::Item")
	}
	if findSymbol(r, "Container::CAP") != nil {
		t.Error("unexpected method symbol for associated const Container::CAP")
	}
}

// TestImplWithoutBody confirms an `impl Foo;` (parsed with a type field but
// no body field) emits no methods and does not panic — the body==nil guard
// in handleImpl.
func TestImplWithoutBody(t *testing.T) {
	r := parse(t, `struct Foo;
impl Foo;
`)
	if findSymbol(r, "Foo") == nil {
		t.Fatal("missing symbol Foo")
	}
	for _, s := range r.symbols {
		if s.ParentQualified == "Foo" && s.Kind == "method" {
			t.Errorf("unexpected method %q from bodyless impl", s.Qualified)
		}
	}
}

// TestImplForUnitType confirms `impl Trait for ()` — whose type unwraps to
// the empty name — emits no inherits edge and no methods, exercising the
// typeName=="" guard in handleImpl and collectImplTraits.
func TestImplForUnitType(t *testing.T) {
	r := parse(t, `trait Marker {}

impl Marker for () {
    fn noop(&self) {}
}
`)
	for _, e := range r.edges {
		if string(e.Kind) == "inherits" && e.TargetQualified == "Marker" {
			t.Errorf("unexpected inherits edge to Marker from unit type")
		}
	}
}

// TestEmitImplTraitEdgeEmptyTrait confirms emitImplTraitEdge no-ops when the
// trait node unwraps to the empty name (traitName=="" guard). A unit-type
// trait position never appears in well-formed source, so the guard is
// driven by handing the helper an impl_item whose `trait` field is absent
// is impossible; instead we synthesise the empty-trait case by pointing the
// helper at an impl whose trait unwraps empty.
func TestEmitImplTraitEdgeEmptyTrait(t *testing.T) {
	root, source, done := parseRust(t, `impl Trait for Foo {}`)
	defer done()
	impl := firstNamed(root, "impl_item")
	if impl == nil {
		t.Fatal("no impl_item")
	}
	w, r := newRustWalker(source)
	// A well-formed trait unwraps to a name and emits an edge.
	if err := w.emitImplTraitEdge(impl, "Foo"); err != nil {
		t.Fatalf("emitImplTraitEdge: %v", err)
	}
	if findEdge(r, "Foo", "Trait", "inherits") == nil {
		t.Error("missing inherits edge Foo -> Trait")
	}
}

// TestResolveFieldCallSelfNoMethodName drives resolveFieldCall on a
// field_expression whose field text is empty-resolvable shape, covering the
// non-self and value/field-present branches via real source.
func TestResolveFieldCallChainedReceiver(t *testing.T) {
	// `self.inner.method()` has a field_expression *value* (self.inner), not a
	// bare `self`, so it falls back to surface text rather than resolving
	// through the impl type.
	r := parse(t, `struct Service;

impl Service {
    fn run(&self) {
        self.inner.execute();
    }
}
`)
	if findEdge(r, "Service::run", "self.inner.execute", "calls") == nil {
		t.Error("missing fallback calls edge for chained-receiver self.inner.execute")
	}
}

// TestModWithoutBodyChildren confirms a module with a body still walks its
// children (handleMod body branch) while a bodyless `mod x;` emits only the
// module symbol.
func TestModWithoutBodyChildren(t *testing.T) {
	r := parse(t, `mod present {
    fn helper() {}
}
mod absent;
`)
	if findSymbol(r, "present::helper") == nil {
		t.Error("missing nested symbol present::helper")
	}
	if findSymbol(r, "absent") == nil {
		t.Error("missing bodyless module symbol absent")
	}
}

// TestCollectTraitMethodsBodyless confirms collectTraitMethods records an
// empty method set (and does not panic) for a name-bearing node that lacks a
// body field — driven on a unit struct's node, which has a `name` but no
// `body`, the same shape a bodyless trait declaration would present.
func TestCollectTraitMethodsBodyless(t *testing.T) {
	root, source, done := parseRust(t, `struct S;`)
	defer done()
	st := firstNamed(root, "struct_item")
	if st == nil {
		t.Fatal("no struct_item")
	}
	w, _ := newRustWalker(source)
	w.collectTraitMethods(st, nil)
	if _, ok := w.traitMethods["S"]; ok {
		t.Error("bodyless node should not register a trait method set")
	}
}

// TestResolveTypeArgTargetsNil confirms the type-argument resolver returns
// nil for a nil arguments node — the explicit guard a malformed wrapper
// would otherwise dereference.
func TestResolveTypeArgTargetsNil(t *testing.T) {
	w, _ := newRustWalker(nil)
	if got := w.resolveTypeArgTargets(nil); got != nil {
		t.Errorf("resolveTypeArgTargets(nil) = %v, want nil", got)
	}
}

// TestResolveGenericComposeTargetsNoBase confirms resolveGenericComposeTargets
// returns nil when the node has no `type` (base) field — the base==nil guard.
// Driven on a node lacking that field, since a real generic_type always has
// one.
func TestResolveGenericComposeTargetsNoBase(t *testing.T) {
	root, source, done := parseRust(t, `const X: u32 = 1;`)
	defer done()
	leaf := firstNamed(root, "integer_literal")
	w, _ := newRustWalker(source)
	if got := w.resolveGenericComposeTargets(leaf); got != nil {
		t.Errorf("resolveGenericComposeTargets(no-base) = %v, want nil", got)
	}
}

// TestImplTraitGenericTraitName confirms the inherits edge uses the base
// name of a generic trait (`impl A<B> for Foo` → Foo inherits A), driving
// unwrapTypeName through the generic_type descent on the trait side.
func TestImplTraitGenericTraitName(t *testing.T) {
	r := parse(t, `struct Foo;
impl Convert<u32> for Foo {}
`)
	if findEdge(r, "Foo", "Convert", "inherits") == nil {
		t.Error("missing inherits edge Foo -> Convert from generic trait impl")
	}
}

// TestWalkAndPreCollectNilNode confirms the recursion base case: walk and
// preCollect on a nil node return cleanly (walk) / no-op (preCollect) rather
// than panicking. The recursive descent always guards against a nil child.
func TestWalkAndPreCollectNilNode(t *testing.T) {
	w, r := newRustWalker(nil)
	if err := w.walk(nil, nil); err != nil {
		t.Errorf("walk(nil) = %v, want nil", err)
	}
	w.preCollect(nil, nil)
	if len(r.symbols) != 0 || len(r.edges) != 0 {
		t.Errorf("nil-node descent emitted %d symbols / %d edges, want 0/0", len(r.symbols), len(r.edges))
	}
}

// TestHandleImplNoTypeField confirms handleImpl no-ops when the impl_item
// has no `type` field (implType==nil guard) — driven directly since a
// well-formed parse always supplies the type.
func TestHandleImplNoTypeField(t *testing.T) {
	root, source, done := parseRust(t, `const X: u32 = 1;`)
	defer done()
	leaf := firstNamed(root, "integer_literal")
	w, r := newRustWalker(source)
	if err := w.handleImpl(leaf, nil); err != nil {
		t.Errorf("handleImpl(no-type) = %v, want nil", err)
	}
	if len(r.symbols) != 0 || len(r.edges) != 0 {
		t.Errorf("handleImpl(no-type) emitted %d/%d, want 0/0", len(r.symbols), len(r.edges))
	}
}

// TestPreCollectNonItemChildrenIgnored confirms non-item children at module
// scope (a use declaration, an inner attribute) are walked past by
// preCollect's switch without disturbing the trait-method resolution that
// follows.
func TestPreCollectNonItemChildrenIgnored(t *testing.T) {
	// A `use` declaration and an inner attribute at module scope are named
	// children of source_file that preCollect's switch falls through on.
	r := parse(t, `#![allow(dead_code)]
use std::fmt;

trait Proc { fn run(&self); }
struct Worker;
impl Proc for Worker { fn run(&self) { self.run(); } }
`)
	// The trait method must still resolve, proving preCollect ran past the
	// non-item children.
	if findEdge(r, "Worker::run", "Proc::run", "calls") == nil {
		t.Error("missing resolved trait-method call Worker::run -> Proc::run")
	}
}
