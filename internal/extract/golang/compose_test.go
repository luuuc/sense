package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// These tests cover composition-edge emission for named struct fields: a
// field's declared type is a has-a fact recorded as a composes edge. Embedded
// fields keep their includes edges (method-set semantics); named fields are
// the exact complement. Wrapper types (pointer, slice, array, map, channel,
// parens, generics) unwrap to the user-defined types they name; predeclared
// types, type parameters, function types, and inline type literals compose
// no edge.

func composeEdges(r *recorder) []string {
	var out []string
	for _, e := range r.edges {
		if e.Kind == "composes" {
			out = append(out, e.SourceQualified+"->"+e.TargetQualified)
		}
	}
	return out
}

func TestComposeSamePackageNamedField(t *testing.T) {
	r := parse(t, `package shop

type Customer struct{}

type Order struct {
	c Customer
}
`)
	if findEdge(r, "shop.Order", "shop.Customer", "composes") == nil {
		t.Error("missing composes edge shop.Order -> shop.Customer for named field")
	}
}

func TestComposePredeclaredTypesSkipped(t *testing.T) {
	r := parse(t, `package shop

type Row struct {
	a bool
	b byte
	c complex64
	d complex128
	e error
	f float32
	g float64
	h int
	i int8
	j int16
	k int32
	l int64
	m rune
	n string
	o uint
	p uint8
	q uint16
	r uint32
	s uint64
	u uintptr
	v any
}
`)
	if got := composeEdges(r); len(got) != 0 {
		t.Errorf("predeclared-typed fields must compose no edge, got %v", got)
	}
}

func TestComposeQualifiedTypeField(t *testing.T) {
	r := parse(t, `package commithook

import "github.com/dolthub/dolt/go/libraries/doltcore/doltdb"

type pushOnWrite struct {
	srcDB doltdb.DoltDB
}
`)
	if findEdge(r, "commithook.pushOnWrite", "doltdb.DoltDB", "composes") == nil {
		t.Error("missing composes edge for qualified-type field (raw text target)")
	}
}

func TestComposePointerField(t *testing.T) {
	r := parse(t, `package commithook

import "github.com/dolthub/dolt/go/libraries/doltcore/doltdb"

type Local struct{}

type pushOnWrite struct {
	destDB *doltdb.DoltDB
	local  *Local
}
`)
	if findEdge(r, "commithook.pushOnWrite", "doltdb.DoltDB", "composes") == nil {
		t.Error("missing composes edge through pointer to qualified type")
	}
	if findEdge(r, "commithook.pushOnWrite", "commithook.Local", "composes") == nil {
		t.Error("missing composes edge through pointer to same-package type")
	}
}

func TestComposeSliceAndArrayFields(t *testing.T) {
	r := parse(t, `package shop

type Item struct{}
type Tag struct{}

type Cart struct {
	items []Item
	tags  [4]Tag
	names []string
}
`)
	if findEdge(r, "shop.Cart", "shop.Item", "composes") == nil {
		t.Error("missing composes edge through slice element")
	}
	if findEdge(r, "shop.Cart", "shop.Tag", "composes") == nil {
		t.Error("missing composes edge through array element")
	}
	if got := composeEdges(r); len(got) != 2 {
		t.Errorf("expected exactly 2 composes edges ([]string composes none), got %v", got)
	}
}

func TestComposeMapKeyAndValue(t *testing.T) {
	r := parse(t, `package auth

type UserID struct{}
type Session struct{}

type Store struct {
	byUser map[UserID]Session
	names  map[string]int
}
`)
	if findEdge(r, "auth.Store", "auth.UserID", "composes") == nil {
		t.Error("missing composes edge for map key type")
	}
	if findEdge(r, "auth.Store", "auth.Session", "composes") == nil {
		t.Error("missing composes edge for map value type")
	}
	if got := composeEdges(r); len(got) != 2 {
		t.Errorf("expected exactly 2 composes edges (string/int keys compose none), got %v", got)
	}
}

func TestComposeChannelField(t *testing.T) {
	r := parse(t, `package bus

type Event struct{}

type Fanout struct {
	events chan Event
	acks   <-chan bool
}
`)
	if findEdge(r, "bus.Fanout", "bus.Event", "composes") == nil {
		t.Error("missing composes edge through channel element")
	}
	if got := composeEdges(r); len(got) != 1 {
		t.Errorf("expected exactly 1 composes edge (chan bool composes none), got %v", got)
	}
}

func TestComposeNestedWrappers(t *testing.T) {
	r := parse(t, `package deep

type Foo struct{}

type Holder struct {
	grid []map[string]*Foo
	flat (Foo)
}
`)
	if findEdge(r, "deep.Holder", "deep.Foo", "composes") == nil {
		t.Error("missing composes edge through []map[string]*Foo nesting")
	}
	edges := composeEdges(r)
	if len(edges) != 2 {
		t.Errorf("expected 2 composes edges (nested + parenthesized), got %v", edges)
	}
}

func TestComposeGenericBaseAndArgs(t *testing.T) {
	// A generic field composes its base type AND its type arguments; a
	// qualified generic base must keep its package prefix (never re-qualified
	// into the current package).
	r := parse(t, `package reg

import "example.com/pool"

type Conn struct{}
type Registry[T any] struct{}

type Server struct {
	conns  Registry[*Conn]
	remote pool.Registry[*Conn]
}
`)
	if findEdge(r, "reg.Server", "reg.Registry", "composes") == nil {
		t.Error("missing composes edge to generic base type")
	}
	if findEdge(r, "reg.Server", "pool.Registry", "composes") == nil {
		t.Error("missing composes edge to qualified generic base (must not be re-qualified)")
	}
	if findEdge(r, "reg.Server", "reg.Conn", "composes") == nil {
		t.Error("missing composes edge to generic type argument")
	}
	for _, e := range r.edges {
		if e.Kind == "composes" && e.TargetQualified == "reg.pool.Registry" {
			t.Error("qualified generic base was re-qualified into the current package")
		}
	}
}

func TestComposeTypeParameterNotEmittedWithDecoy(t *testing.T) {
	// The decoy: a REAL type named T exists in the package. A generic
	// struct's fields typed by its own type parameter T must not emit a
	// composes edge to it — bare, wrapped, or as a generic argument.
	r := parse(t, `package box

type T struct{}
type Registry[X any] struct{}

type Box[ /* the payload type */ T any] struct {
	v     T
	items []T
	m     map[string]T
	r     Registry[T]
}
`)
	if e := findEdge(r, "box.Box", "box.T", "composes"); e != nil {
		t.Error("type-parameter field must not compose to the decoy real type T")
	}
	edges := composeEdges(r)
	if len(edges) != 1 {
		t.Errorf("expected exactly 1 composes edge (Box -> Registry only), got %v", edges)
	}
	if findEdge(r, "box.Box", "box.Registry", "composes") == nil {
		t.Error("missing composes edge Box -> Registry (generic base survives the param-arg skip)")
	}
}

func TestComposeFunctionTypeSkipped(t *testing.T) {
	r := parse(t, `package hooks

type Payload struct{}

type Handler struct {
	fn func(Payload) error
}
`)
	if got := composeEdges(r); len(got) != 0 {
		t.Errorf("function-typed field must compose no edge, got %v", got)
	}
}

func TestComposeInlineTypeLiteralsSkipped(t *testing.T) {
	r := parse(t, `package cfg

type Nested struct{}

type Config struct {
	meta struct {
		n Nested
	}
	sink interface {
		Write() error
	}
}
`)
	if got := composeEdges(r); len(got) != 0 {
		t.Errorf("inline struct/interface fields must compose no edge, got %v", got)
	}
}

func TestComposeEmbeddedNamedComplement(t *testing.T) {
	// One struct, both field shapes: embedded fields emit includes and never
	// composes; named fields emit composes and never includes. Embedded
	// pointer (*Bar) keeps its includes edge — pinned current behavior.
	r := parse(t, `package mix

type Foo struct{}
type Bar struct{}
type Baz struct{}

type Holder struct {
	Foo
	*Bar
	baz Baz
}
`)
	if findEdge(r, "mix.Holder", "mix.Foo", "includes") == nil {
		t.Error("embedded value field lost its includes edge")
	}
	if findEdge(r, "mix.Holder", "mix.Bar", "includes") == nil {
		t.Error("embedded pointer field lost its includes edge")
	}
	if findEdge(r, "mix.Holder", "mix.Foo", "composes") != nil ||
		findEdge(r, "mix.Holder", "mix.Bar", "composes") != nil {
		t.Error("embedded fields must not double-emit as composes")
	}
	if findEdge(r, "mix.Holder", "mix.Baz", "composes") == nil {
		t.Error("missing composes edge for the named field")
	}
	if findEdge(r, "mix.Holder", "mix.Baz", "includes") != nil {
		t.Error("named field must not emit an includes edge")
	}
}

func TestComposeMultiNameFieldSingleEdge(t *testing.T) {
	// `a, b *Foo` is ONE field_declaration with two name children: the
	// has-a fact is the same either way — exactly one edge, never two.
	r := parse(t, `package dup

type Foo struct{}

type Pair struct {
	a, b *Foo
}
`)
	edges := composeEdges(r)
	if len(edges) != 1 {
		t.Errorf("expected exactly 1 composes edge for multi-name field, got %v", edges)
	}
}

func TestComposeAliasDeclarationUnchanged(t *testing.T) {
	// Aliases never carry a struct body through emitTypeSpec (structNode is
	// only set for non-alias specs) — no composes edges from alias specs.
	r := parse(t, `package al

type Base struct {
	n int
}

type A = Base
`)
	if got := composeEdges(r); len(got) != 0 {
		t.Errorf("alias declarations must compose no edge, got %v", got)
	}
}

func TestComposeAliasedImportKeepsWrittenText(t *testing.T) {
	// The walker has no import map: a renamed import's qualifier reaches the
	// edge as written (`user_model.User`), resolving only by leaf downstream.
	// Pinned so alias normalization (G-7b residue) is a conscious change.
	r := parse(t, `package issue

import user_model "code.example/models/user"

type Issue struct {
	Poster user_model.User
}
`)
	if findEdge(r, "issue.Issue", "user_model.User", "composes") == nil {
		t.Error("aliased qualified field must compose to the written text")
	}
}

func TestComposeGuardsOnMalformedNodes(t *testing.T) {
	src := []byte("package p\n\ntype I interface{}\n")
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	_ = p.SetLanguage(ex.Grammar())
	tree := p.Parse(src, nil)
	defer tree.Close()

	r := &recorder{}
	w := &walker{source: src, emit: r, pkg: "p"}
	ifaceType := findNodeOfKind(tree.RootNode(), "interface_type")
	if ifaceType == nil {
		t.Fatal("no interface_type node in fixture")
	}
	// A non-struct node has no field_declaration_list child: the guard
	// returns cleanly instead of walking garbage.
	if err := w.emitFieldCompositions(ifaceType, "p.I", nil); err != nil {
		t.Fatalf("emitFieldCompositions on non-struct node: %v", err)
	}
	if len(r.edges) != 0 {
		t.Errorf("non-struct node must emit no edges, got %d", len(r.edges))
	}
	if got := w.composeTargets(nil, nil); got != nil {
		t.Errorf("composeTargets(nil) = %v, want nil", got)
	}
	if got := w.composeTypeArgTargets(nil, nil); got != nil {
		t.Errorf("composeTypeArgTargets(nil) = %v, want nil", got)
	}
	// Named children that are not type_elem (here the root's package_clause
	// and type_declaration) are skipped, never recursed into.
	if got := w.composeTypeArgTargets(tree.RootNode(), nil); got != nil {
		t.Errorf("composeTypeArgTargets on non-argument node = %v, want nil", got)
	}
}

func TestComposeEdgeEmitErrorPropagates(t *testing.T) {
	err := parseWithEmitter(t, `package shop

type Customer struct{}

type Order struct {
	c Customer
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error when composes edge emit fails")
	}
}
