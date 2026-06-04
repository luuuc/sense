package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// recorder captures emitted symbols and edges for assertions.
type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error {
	r.symbols = append(r.symbols, s)
	return nil
}
func (r *recorder) Edge(e extract.EmittedEdge) error { r.edges = append(r.edges, e); return nil }

// counter counts emitted symbols and edges.
type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }

// parse is a test helper that parses Go source and runs the extractor.
func parse(t *testing.T, src string) *recorder {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, source, "test.go", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

func findSymbol(r *recorder, qualified string) *extract.EmittedSymbol {
	for i := range r.symbols {
		if r.symbols[i].Qualified == qualified {
			return &r.symbols[i]
		}
	}
	return nil
}

func findEdge(r *recorder, source, target, kind string) *extract.EmittedEdge {
	for i := range r.edges {
		if r.edges[i].SourceQualified == source &&
			r.edges[i].TargetQualified == target &&
			string(r.edges[i].Kind) == kind {
			return &r.edges[i]
		}
	}
	return nil
}

func TestSmokeExtract(t *testing.T) {
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte("package p\n\nfunc F() {}\n")
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, src, "smoke.go", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the F function")
	}
}

func TestStructExtraction(t *testing.T) {
	r := parse(t, `package order

type Order struct {
	ID    int
	Total float64
}
`)
	s := findSymbol(r, "order.Order")
	if s == nil {
		t.Fatal("missing symbol order.Order")
	}
	if s.Kind != "class" {
		t.Errorf("Order.Kind = %q, want class", s.Kind)
	}
	if s.Visibility != "public" {
		t.Errorf("Order.Visibility = %q, want public", s.Visibility)
	}
}

func TestPrivateStruct(t *testing.T) {
	r := parse(t, `package order

type config struct {
	name string
}
`)
	s := findSymbol(r, "order.config")
	if s == nil {
		t.Fatal("missing symbol order.config")
	}
	if s.Visibility != "private" {
		t.Errorf("config.Visibility = %q, want private", s.Visibility)
	}
}

func TestInterfaceExtraction(t *testing.T) {
	r := parse(t, `package svc

type Processor interface {
	Process() error
	Validate() bool
}
`)
	s := findSymbol(r, "svc.Processor")
	if s == nil {
		t.Fatal("missing symbol svc.Processor")
	}
	if s.Kind != "interface" {
		t.Errorf("Processor.Kind = %q, want interface", s.Kind)
	}
	// Interface methods
	m := findSymbol(r, "svc.Processor.Process")
	if m == nil {
		t.Fatal("missing symbol svc.Processor.Process")
	}
	if m.Kind != "method" {
		t.Errorf("Processor.Process.Kind = %q, want method", m.Kind)
	}
	if m.ParentQualified != "svc.Processor" {
		t.Errorf("Processor.Process.Parent = %q, want svc.Processor", m.ParentQualified)
	}
}

func TestTypeAlias(t *testing.T) {
	r := parse(t, `package types

type Amount = float64
`)
	s := findSymbol(r, "types.Amount")
	if s == nil {
		t.Fatal("missing symbol types.Amount")
	}
	if s.Kind != "type" {
		t.Errorf("Amount.Kind = %q, want type", s.Kind)
	}
}

func TestNamedType(t *testing.T) {
	r := parse(t, `package types

type Handler func()
`)
	s := findSymbol(r, "types.Handler")
	if s == nil {
		t.Fatal("missing symbol types.Handler")
	}
	if s.Kind != "type" {
		t.Errorf("Handler.Kind = %q, want type", s.Kind)
	}
}

func TestConstExtraction(t *testing.T) {
	r := parse(t, `package cfg

const MaxRetries = 3
`)
	s := findSymbol(r, "cfg.MaxRetries")
	if s == nil {
		t.Fatal("missing symbol cfg.MaxRetries")
	}
	if s.Kind != "constant" {
		t.Errorf("MaxRetries.Kind = %q, want constant", s.Kind)
	}
	if s.Visibility != "public" {
		t.Errorf("MaxRetries.Visibility = %q, want public", s.Visibility)
	}
}

func TestPrivateConst(t *testing.T) {
	r := parse(t, `package cfg

const maxRetries = 3
`)
	s := findSymbol(r, "cfg.maxRetries")
	if s == nil {
		t.Fatal("missing symbol cfg.maxRetries")
	}
	if s.Visibility != "private" {
		t.Errorf("maxRetries.Visibility = %q, want private", s.Visibility)
	}
}

func TestGroupedConsts(t *testing.T) {
	r := parse(t, `package cfg

const (
	A = 1
	B = 2
	C = 3
)
`)
	for _, name := range []string{"cfg.A", "cfg.B", "cfg.C"} {
		if findSymbol(r, name) == nil {
			t.Errorf("missing symbol %s from grouped const", name)
		}
	}
}

func TestMultiNameConst(t *testing.T) {
	r := parse(t, `package p

const A, B = 1, 2
`)
	if findSymbol(r, "p.A") == nil {
		t.Error("missing symbol p.A from multi-name const")
	}
	if findSymbol(r, "p.B") == nil {
		t.Error("missing symbol p.B from multi-name const")
	}
}

func TestFunctionExtraction(t *testing.T) {
	r := parse(t, `package svc

func ProcessOrder() {
	validate()
}
`)
	s := findSymbol(r, "svc.ProcessOrder")
	if s == nil {
		t.Fatal("missing symbol svc.ProcessOrder")
	}
	if s.Kind != "function" {
		t.Errorf("ProcessOrder.Kind = %q, want function", s.Kind)
	}
	if findEdge(r, "svc.ProcessOrder", "validate", "calls") == nil {
		t.Error("missing calls edge svc.ProcessOrder -> validate")
	}
}

func TestMethodExtraction(t *testing.T) {
	r := parse(t, `package order

type Order struct{}

func (o Order) Process() {
	o.Validate()
}
`)
	s := findSymbol(r, "order.Order.Process")
	if s == nil {
		t.Fatal("missing symbol order.Order.Process")
	}
	if s.Kind != "method" {
		t.Errorf("Order.Process.Kind = %q, want method", s.Kind)
	}
	if s.ParentQualified != "order.Order" {
		t.Errorf("Order.Process.Parent = %q, want order.Order", s.ParentQualified)
	}
}

func TestPointerReceiverMethod(t *testing.T) {
	r := parse(t, `package order

type Order struct{}

func (o *Order) Save() {}
`)
	s := findSymbol(r, "order.Order.Save")
	if s == nil {
		t.Fatal("missing symbol order.Order.Save for pointer receiver")
	}
}

func TestGenericReceiverMethod(t *testing.T) {
	r := parse(t, `package cache

type Cache[T any] struct{}

func (c *Cache[T]) Get(key string) {}
`)
	s := findSymbol(r, "cache.Cache.Get")
	if s == nil {
		t.Fatal("missing symbol cache.Cache.Get for generic receiver")
	}
}

func TestCallEdges(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	fmt.Println("hello")
	helper()
}
`)
	if findEdge(r, "svc.Process", "fmt.Println", "calls") == nil {
		t.Error("missing calls edge svc.Process -> fmt.Println")
	}
	if findEdge(r, "svc.Process", "helper", "calls") == nil {
		t.Error("missing calls edge svc.Process -> helper")
	}
}

func TestSelectorCallWithTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func Process() {
	o := Order{}
	o.Validate()
}
`)
	// Short var decl with composite literal should resolve type
	e := findEdge(r, "svc.Process", "svc.Order.Validate", "calls")
	if e == nil {
		t.Fatal("missing calls edge svc.Process -> svc.Order.Validate from type-resolved selector")
	}
}

func TestPointerCompositeLiteralResolution(t *testing.T) {
	r := parse(t, `package svc

type Config struct{}

func Init() {
	c := &Config{}
	c.Load()
}
`)
	if findEdge(r, "svc.Init", "svc.Config.Load", "calls") == nil {
		t.Error("missing calls edge from pointer composite literal type resolution")
	}
}

func TestConstructorPatternResolution(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	o := NewOrder()
	o.Save()
}
`)
	e := findEdge(r, "svc.Process", "svc.Order.Save", "calls")
	if e == nil {
		t.Fatal("missing calls edge from constructor pattern NewOrder()")
	}
	if e.Confidence != extract.ConfidenceAmbiguous {
		t.Errorf("constructor confidence = %v, want %v", e.Confidence, extract.ConfidenceAmbiguous)
	}
}

func TestParameterTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func Process(o Order) {
	o.Validate()
}
`)
	if findEdge(r, "svc.Process", "svc.Order.Validate", "calls") == nil {
		t.Error("missing calls edge from parameter type resolution")
	}
}

func TestReceiverTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Service struct{}
type Order struct{}

func (s *Service) Process(o *Order) {
	o.Validate()
}
`)
	if findEdge(r, "svc.Service.Process", "svc.Order.Validate", "calls") == nil {
		t.Error("missing calls edge from receiver type resolution of parameter")
	}
}

func TestVarDeclarationTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Config struct{}

func Init() {
	var c Config
	c.Load()
}
`)
	if findEdge(r, "svc.Init", "svc.Config.Load", "calls") == nil {
		t.Error("missing calls edge from var declaration type resolution")
	}
}

func TestSliceTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func Process(orders []Order) {
	for _, o := range orders {
		o.Save()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Order.Save", "calls") == nil {
		t.Error("missing calls edge from range over typed slice")
	}
}

// TestRangeVariableEdgeCases covers the range-clause shapes that are not
// type-resolved: a single-variable `for i := range` binds the index (not the
// element), and a blank value `for k, _ := range` has nothing to bind. Both must
// extract cleanly without inventing a resolved type.
func TestRangeVariableEdgeCases(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func Scan(orders []Order) {
	for i := range orders {
		_ = i
	}
	for k, _ := range orders {
		_ = k
	}
}
`)
	if findSymbol(r, "svc.Scan") == nil {
		t.Fatal("missing svc.Scan symbol")
	}
}

func TestEmbedding(t *testing.T) {
	r := parse(t, `package model

type Base struct{}

type Order struct {
	Base
}
`)
	if findEdge(r, "model.Order", "model.Base", "includes") == nil {
		t.Error("missing includes edge model.Order -> model.Base from embedding")
	}
}

func TestPointerEmbedding(t *testing.T) {
	r := parse(t, `package model

type Logger struct{}

type Service struct {
	*Logger
}
`)
	// Pointer embedding should still produce includes edge
	// (unwrapTypeName strips the pointer)
	if findEdge(r, "model.Service", "model.Logger", "includes") == nil {
		t.Error("missing includes edge from pointer embedding")
	}
}

func TestQualifiedTypeEmbedding(t *testing.T) {
	r := parse(t, `package model

type Service struct {
	sync.Mutex
}
`)
	if findEdge(r, "model.Service", "sync.Mutex", "includes") == nil {
		t.Error("missing includes edge from qualified type embedding")
	}
}

func TestNoPackageClause(t *testing.T) {
	// Edge case: file without package clause
	r := parse(t, `func F() {}
`)
	// Should still extract, just without package prefix
	s := findSymbol(r, "F")
	if s == nil {
		t.Fatal("missing symbol F from file without package clause")
	}
}

func TestVisibility(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"Exported", "public"},
		{"exported", "private"},
		{"A", "public"},
		{"a", "private"},
	}
	for _, tc := range cases {
		if got := visibility(tc.name); got != tc.want {
			t.Errorf("visibility(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestConstructorType(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"NewOrder", "Order"},
		{"newOrder", "Order"},
		{"NewA", "A"},
		{"New", ""},   // too short
		{"Newer", ""}, // no uppercase after "New"
		{"new", ""},   // too short
		{"abc", ""},
		{"Neworder", ""}, // lowercase after "New"
	}
	for _, tc := range cases {
		if got := constructorType(tc.in); got != tc.want {
			t.Errorf("constructorType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// receiverNode parses one method declaration and returns its `receiver` child,
// the parameter_list that receiverType consumes.
func receiverNode(t *testing.T, src string) (*sitter.Node, []byte) {
	t.Helper()
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(grammars.Go()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	t.Cleanup(tree.Close)
	root := tree.RootNode()
	for i := uint(0); i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if n != nil && n.Kind() == "method_declaration" {
			return n.ChildByFieldName("receiver"), source
		}
	}
	t.Fatal("no method_declaration in source")
	return nil, source
}

// TestReceiverType drives receiverType over real method receivers — value,
// pointer, and generic — plus the nil contract, the shapes that decide whether a
// method's body calls resolve against its receiver type.
func TestReceiverType(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"value", "package p\ntype Order struct{}\nfunc (o Order) M() {}\n", "Order"},
		{"pointer", "package p\ntype Order struct{}\nfunc (o *Order) M() {}\n", "Order"},
		{"generic", "package p\ntype Cache[T any] struct{}\nfunc (c Cache[T]) M() {}\n", "Cache"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recv, src := receiverNode(t, tc.src)
			if got := receiverType(recv, src); got != tc.want {
				t.Errorf("receiverType = %q, want %q", got, tc.want)
			}
		})
	}
	// A nil receiver (defensive contract for callers outside handleMethod)
	// resolves to the empty string, never a panic.
	if got := receiverType(nil, nil); got != "" {
		t.Errorf("receiverType(nil) = %q, want empty", got)
	}
}

func TestExtractorMetadata(t *testing.T) {
	ex := Extractor{}
	if ex.Language() != "go" {
		t.Errorf("Language() = %q, want go", ex.Language())
	}
	exts := ex.Extensions()
	if len(exts) != 1 || exts[0] != ".go" {
		t.Errorf("Extensions() = %v, want [.go]", exts)
	}
	if ex.Tier() != extract.TierBasic {
		t.Errorf("Tier() = %v, want TierBasic", ex.Tier())
	}
	if ex.Grammar() == nil {
		t.Error("Grammar() returned nil")
	}
}

func TestMethodCallInBody(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func (o *Order) Process() {
	helper()
	fmt.Println("done")
}
`)
	if findEdge(r, "svc.Order.Process", "helper", "calls") == nil {
		t.Error("missing calls edge from method body")
	}
	if findEdge(r, "svc.Order.Process", "fmt.Println", "calls") == nil {
		t.Error("missing calls edge from method body selector")
	}
}

func TestTypeDeclarationGroup(t *testing.T) {
	r := parse(t, `package types

type (
	Order struct{}
	Config struct{}
)
`)
	if findSymbol(r, "types.Order") == nil {
		t.Error("missing symbol types.Order from grouped type declaration")
	}
	if findSymbol(r, "types.Config") == nil {
		t.Error("missing symbol types.Config from grouped type declaration")
	}
}

func TestLocalVariableNoFalsePositive(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	x := 42
	result := x + 1
	_ = result
}
`)
	// No call edges should be emitted for simple variable usage
	for _, e := range r.edges {
		if string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge: %s -> %s", e.SourceQualified, e.TargetQualified)
		}
	}
}

func TestUnknownLocalConfidence(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	x := something()
	x.Method()
}
`)
	// x is a local but type is unknown (something() doesn't match New pattern)
	// Should still emit edge but with lower confidence
	e := findEdge(r, "svc.Process", "x.Method", "calls")
	if e == nil {
		t.Fatal("missing calls edge for unresolved local method call")
	}
	if e.Confidence != extract.ConfidenceAmbiguous {
		t.Errorf("unresolved local confidence = %v, want %v", e.Confidence, extract.ConfidenceAmbiguous)
	}
}

func TestCompositeLiteralSliceRange(t *testing.T) {
	r := parse(t, `package svc

type Item struct{}

func Process() {
	for _, item := range []Item{{}, {}} {
		item.Save()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Item.Save", "calls") == nil {
		t.Error("missing calls edge from range over composite literal slice")
	}
}

func TestGenericTypeEmbedding(t *testing.T) {
	r := parse(t, `package model

type Cache[T any] struct{}

type Service struct {
	Cache[string]
}
`)
	if findEdge(r, "model.Service", "model.Cache", "includes") == nil {
		t.Error("missing includes edge from generic type embedding")
	}
}

func TestVarDeclSliceType(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func Process() {
	var orders []Order
	for _, o := range orders {
		o.Save()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Order.Save", "calls") == nil {
		t.Error("missing calls edge from var decl slice type resolution")
	}
}

func TestTypeAliasMultiple(t *testing.T) {
	r := parse(t, `package model

type ID = int64
type Name = string
`)
	s := findSymbol(r, "model.ID")
	if s == nil {
		t.Fatal("missing symbol model.ID for type alias")
	}
	if s.Kind != "type" {
		t.Errorf("ID.Kind = %q, want type", s.Kind)
	}
	s2 := findSymbol(r, "model.Name")
	if s2 == nil {
		t.Fatal("missing symbol model.Name for type alias")
	}
}

func TestGroupedConstants(t *testing.T) {
	r := parse(t, `package cfg

const (
	DefaultTimeout = 30
	MaxRetries     = 3
	Version        = "1.0"
)
`)
	for _, name := range []string{"cfg.DefaultTimeout", "cfg.MaxRetries", "cfg.Version"} {
		if findSymbol(r, name) == nil {
			t.Errorf("missing symbol %s from grouped const", name)
		}
	}
}

func TestSingleConstant(t *testing.T) {
	r := parse(t, `package app

const AppName = "sense"
`)
	if findSymbol(r, "app.AppName") == nil {
		t.Error("missing symbol app.AppName for single const")
	}
}

func TestEmptyInterface(t *testing.T) {
	r := parse(t, `package svc

type Any interface{}
`)
	s := findSymbol(r, "svc.Any")
	if s == nil {
		t.Fatal("missing symbol svc.Any for empty interface")
	}
	if s.Kind != "interface" {
		t.Errorf("Any.Kind = %q, want interface", s.Kind)
	}
}

func TestInterfaceWithMultipleMethods(t *testing.T) {
	r := parse(t, `package svc

type Handler interface {
	Process()
	Validate() error
	Close() error
}
`)
	for _, name := range []string{"svc.Handler.Process", "svc.Handler.Validate", "svc.Handler.Close"} {
		s := findSymbol(r, name)
		if s == nil {
			t.Errorf("missing symbol %s", name)
		} else if s.Kind != "method" {
			t.Errorf("%s.Kind = %q, want method", name, s.Kind)
		}
	}
}

func TestStructWithMultipleEmbeddings(t *testing.T) {
	r := parse(t, `package model

type Base struct{}
type Logger struct{}

type Service struct {
	Base
	Logger
	name string
}
`)
	if findEdge(r, "model.Service", "model.Base", "includes") == nil {
		t.Error("missing includes edge Service -> Base")
	}
	if findEdge(r, "model.Service", "model.Logger", "includes") == nil {
		t.Error("missing includes edge Service -> Logger")
	}
}

func TestPointerReceiverMethodWithCall(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func (o *Order) Save() error {
	validate()
	return nil
}
`)
	m := findSymbol(r, "svc.Order.Save")
	if m == nil {
		t.Fatal("missing symbol svc.Order.Save for pointer receiver")
	}
	if m.Kind != "method" {
		t.Errorf("Save.Kind = %q, want method", m.Kind)
	}
	if findEdge(r, "svc.Order.Save", "validate", "calls") == nil {
		t.Error("missing calls edge from pointer receiver method")
	}
}

func TestGenericReceiverMethodPointer(t *testing.T) {
	r := parse(t, `package svc

type Cache[T any] struct{}

func (c *Cache[T]) Get() {}
`)
	m := findSymbol(r, "svc.Cache.Get")
	if m == nil {
		t.Fatal("missing symbol svc.Cache.Get for generic pointer receiver")
	}
}

func TestGroupedTypeDeclarations(t *testing.T) {
	r := parse(t, `package model

type (
	ID     int64
	Status string
)
`)
	if findSymbol(r, "model.ID") == nil {
		t.Error("missing symbol model.ID from grouped type")
	}
	if findSymbol(r, "model.Status") == nil {
		t.Error("missing symbol model.Status from grouped type")
	}
}

func TestSelectorCallOnFunctionReturn(t *testing.T) {
	// Call on a function return value: getUser().Save()
	r := parse(t, `package svc

func getUser() {}

func Process() {
	getUser().Save()
}
`)
	// Without type info, this should still emit a call for Save()
	edges := []string{}
	for _, e := range r.edges {
		if string(e.Kind) == "calls" {
			edges = append(edges, e.TargetQualified)
		}
	}
	if len(edges) < 2 {
		t.Errorf("expected at least 2 call edges (getUser + Save), got %v", edges)
	}
}

func TestMethodOnSliceParamRange(t *testing.T) {
	r := parse(t, `package svc

type Item struct{}

func Process(items []Item) {
	for _, item := range items {
		item.Save()
	}
}
`)
	// Slice param range should resolve item as type Item
	if findEdge(r, "svc.Process", "svc.Item.Save", "calls") == nil {
		t.Error("missing calls edge from slice param range type resolution")
	}
}

func TestShortVarDeclWithConstructor(t *testing.T) {
	r := parse(t, `package svc

type Client struct{}

func NewClient() *Client { return nil }

func Run() {
	c := NewClient()
	c.Connect()
}
`)
	if findEdge(r, "svc.Run", "svc.Client.Connect", "calls") == nil {
		t.Error("missing calls edge from short var decl constructor resolution")
	}
}

func TestExportedVsUnexportedVisibility(t *testing.T) {
	r := parse(t, `package pkg

type MyType struct{}
type myPrivate struct{}
func Exported() {}
func unexported() {}
const PublicConst = 1
const privateConst = 2
`)
	tests := []struct {
		qualified  string
		visibility string
	}{
		{"pkg.MyType", "public"},
		{"pkg.myPrivate", "private"},
		{"pkg.Exported", "public"},
		{"pkg.unexported", "private"},
		{"pkg.PublicConst", "public"},
		{"pkg.privateConst", "private"},
	}
	for _, tt := range tests {
		s := findSymbol(r, tt.qualified)
		if s == nil {
			t.Errorf("missing symbol %s", tt.qualified)
			continue
		}
		if s.Visibility != tt.visibility {
			t.Errorf("%s.Visibility = %q, want %q", tt.qualified, s.Visibility, tt.visibility)
		}
	}
}

// --- error propagation tests ---

var errForced = &testErr{"forced"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

type failAfterN struct {
	symbolsLeft int
	edgesLeft   int
}

func (f *failAfterN) Symbol(_ extract.EmittedSymbol) error {
	if f.symbolsLeft <= 0 {
		return errForced
	}
	f.symbolsLeft--
	return nil
}

func (f *failAfterN) Edge(_ extract.EmittedEdge) error {
	if f.edgesLeft <= 0 {
		return errForced
	}
	f.edgesLeft--
	return nil
}

func parseWithEmitter(t *testing.T, src string, emit extract.Emitter) error {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()
	return ex.Extract(tree, source, "test.go", emit)
}

func TestFuncSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `package main
func Hello() {}
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on function symbol emit")
	}
}

func TestMethodSymbolError(t *testing.T) {
	// Type symbol succeeds, method fails
	err := parseWithEmitter(t, `package main
type Server struct{}
func (s *Server) Handle() {}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on method symbol emit")
	}
}

func TestCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `package main
func main() {
    helper()
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on call edge emit")
	}
}

func TestInterfaceMethodError(t *testing.T) {
	// Interface symbol succeeds, method symbol fails
	err := parseWithEmitter(t, `package main
type Reader interface {
    Read(p []byte) (int, error)
}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on interface method symbol emit")
	}
}

func TestEmbeddingEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type Outer struct {
    Inner
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on embedding composes edge emit")
	}
}

func TestTypeDeclarationError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type Config struct {
    Name string
}
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on type declaration symbol emit")
	}
}

func TestConstantError(t *testing.T) {
	err := parseWithEmitter(t, `package main
const MaxSize = 1024
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on constant symbol emit")
	}
}

func TestReceiverTypeQualifiedType(t *testing.T) {
	r := parse(t, `package svc

import "io"

func Process(r io.Reader) {}
`)
	// io.Reader is a qualified_type — can't be resolved in Tier-Basic,
	// but should not crash and should still emit the function.
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
}

func TestGenericReceiverUnwrap(t *testing.T) {
	r := parse(t, `package store

type Store[K comparable, V any] struct{}

func (s Store[K, V]) Get(key K) V { return *new(V) }
`)
	s := findSymbol(r, "store.Store.Get")
	if s == nil {
		t.Fatal("missing symbol store.Store.Get for generic receiver")
	}
	if s.ParentQualified != "store.Store" {
		t.Errorf("Get.ParentQualified = %q, want store.Store", s.ParentQualified)
	}
}

func TestReceiverNilHandling(t *testing.T) {
	// Method with empty receiver — parse anyway
	r := parse(t, `package pkg

type T struct{}
func (T) Method() {}
`)
	// receiver has no name, but type is T
	s := findSymbol(r, "pkg.T.Method")
	if s == nil {
		t.Fatal("missing symbol pkg.T.Method for unnamed receiver")
	}
}

func TestInterfaceEmbedding(t *testing.T) {
	r := parse(t, `package svc

type Reader interface {
	Read(p []byte) (int, error)
}

type ReadWriter interface {
	Reader
	Write(p []byte) (int, error)
}
`)
	// ReadWriter should have Write as a method, Reader is an embedding
	if findSymbol(r, "svc.ReadWriter.Write") == nil {
		t.Error("missing method svc.ReadWriter.Write")
	}
}

func TestPointerVarDecl(t *testing.T) {
	r := parse(t, `package svc

type Config struct{}

func Init() {
	var c *Config
	c.Load()
}
`)
	if findEdge(r, "svc.Init", "svc.Config.Load", "calls") == nil {
		t.Error("missing calls edge from pointer var declaration type resolution")
	}
}

func TestArrayTypeResolution(t *testing.T) {
	r := parse(t, `package svc

type Item struct{}

func Process(items [5]Item) {
	for _, item := range items {
		item.Save()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Item.Save", "calls") == nil {
		t.Error("missing calls edge from array type range resolution")
	}
}

func TestMapValueRangeNoResolve(t *testing.T) {
	r := parse(t, `package svc

func Process(data interface{}) {
	for _, v := range data.([]string) {
		_ = v
	}
}
`)
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
}

func TestMultipleReturnShortVar(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	result, err := transform()
	_ = err
	_ = result
}
`)
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
	if findEdge(r, "svc.Process", "transform", "calls") == nil {
		t.Error("missing calls edge from short var decl call")
	}
}

func TestNestedCallExpression(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	getHandler()()
}
`)
	// The outer call's function is a call_expression which hits
	// the default branch in emitCall (returns nil).
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
	// Should still emit the inner call
	if findEdge(r, "svc.Process", "getHandler", "calls") == nil {
		t.Error("missing calls edge to getHandler from nested call")
	}
}

func TestSelectorOnSelectorCall(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	a.b.Method()
}
`)
	// a.b is a selector_expression, not an identifier, so operand.Kind()
	// != "identifier" — should return the full text.
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
}

func TestQualifiedTypeInStructField(t *testing.T) {
	r := parse(t, `package model

type Service struct {
	http.Handler
}
`)
	if findEdge(r, "model.Service", "http.Handler", "includes") == nil {
		t.Error("missing includes edge from qualified type embedding")
	}
}

func TestInferTypeNilValue(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	x := 42
	_ = x
}
`)
	// x := 42 — integer literal, not composite/unary/call — inferType
	// returns false. x should still be in locals but not in types.
	if findSymbol(r, "svc.Process") == nil {
		t.Fatal("missing symbol svc.Process")
	}
}

func TestEmbeddingGenericWithPointer(t *testing.T) {
	r := parse(t, `package model

type Cache[T any] struct{}

type Service struct {
	*Cache[string]
}
`)
	if findEdge(r, "model.Service", "model.Cache", "includes") == nil {
		t.Error("missing includes edge from pointer+generic embedding")
	}
}

func TestIotaConstGroup(t *testing.T) {
	r := parse(t, `package status

const (
	Active = iota
	Inactive
	Deleted
)
`)
	if findSymbol(r, "status.Active") == nil {
		t.Error("missing symbol status.Active")
	}
	if findSymbol(r, "status.Inactive") == nil {
		t.Error("missing symbol status.Inactive")
	}
	if findSymbol(r, "status.Deleted") == nil {
		t.Error("missing symbol status.Deleted")
	}
}

func TestInterfaceMethodWithConstraint(t *testing.T) {
	r := parse(t, `package svc

type Comparable interface {
	~int | ~string
	Compare(other any) int
}
`)
	// The ~int | ~string constraint is a type_elem, not method_elem,
	// so it should be skipped. The Compare method should be emitted.
	if findSymbol(r, "svc.Comparable.Compare") == nil {
		t.Error("missing method svc.Comparable.Compare from interface with constraints")
	}
}

func TestEmbeddingWithNamedAndUnnamedFields(t *testing.T) {
	r := parse(t, `package model

type Base struct{}

type Order struct {
	Base
	Name string
	ID   int
}
`)
	// Should emit includes edge for Base only (unnamed), not Name/ID (named).
	if findEdge(r, "model.Order", "model.Base", "includes") == nil {
		t.Error("missing includes edge from embedding")
	}
	// Should NOT emit includes edge for named fields
	for _, e := range r.edges {
		if e.TargetQualified == "model.string" || e.TargetQualified == "model.int" {
			t.Errorf("unexpected includes edge to %q from named field", e.TargetQualified)
		}
	}
}

func TestSelectorCallWithEmptyReturn(t *testing.T) {
	r := parse(t, `package svc

type Order struct{}

func (o Order) Process() {
	o.Save()
	helper()
}
`)
	if findEdge(r, "svc.Order.Process", "svc.Order.Save", "calls") == nil {
		t.Error("missing calls edge from receiver method")
	}
}

func TestSlicePointerTypeInParameter(t *testing.T) {
	r := parse(t, `package svc

type Item struct{}

func Process(items []*Item) {
	for _, item := range items {
		item.Save()
	}
}
`)
	// *Item in slice — unwrapTypeName peels pointer → Item
	if findEdge(r, "svc.Process", "svc.Item.Save", "calls") == nil {
		t.Error("missing calls edge from pointer slice parameter range")
	}
}

func TestVarDeclArrayType(t *testing.T) {
	r := parse(t, `package svc

type Point struct{}

func Process() {
	var points [10]Point
	for _, p := range points {
		p.Draw()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Point.Draw", "calls") == nil {
		t.Error("missing calls edge from var decl array type resolution")
	}
}

func TestGroupedConstDeclarationError(t *testing.T) {
	err := parseWithEmitter(t, `package main
const (
	A = 1
	B = 2
)
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on second const emit in grouped const")
	}
}

func TestTypeDeclarationGroupError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type (
	First struct{}
	Second struct{}
)
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on second type spec emit")
	}
}

func TestTypeAliasBranch(t *testing.T) {
	r := parse(t, `package model

type OrderID = int64
type Name = string
`)
	s := findSymbol(r, "model.OrderID")
	if s == nil {
		t.Fatal("missing symbol model.OrderID from type alias")
	}
	if s.Kind != "type" {
		t.Errorf("OrderID.Kind = %q, want type", s.Kind)
	}
	s2 := findSymbol(r, "model.Name")
	if s2 == nil {
		t.Fatal("missing symbol model.Name from type alias")
	}
}

func TestGenericReceiverWithPointer(t *testing.T) {
	r := parse(t, `package store

type Cache[K comparable, V any] struct{}

func (c *Cache[K, V]) Set(key K, value V) {}
`)
	s := findSymbol(r, "store.Cache.Set")
	if s == nil {
		t.Fatal("missing symbol store.Cache.Set for pointer+generic receiver")
	}
}

func TestConstructorTypeInference(t *testing.T) {
	r := parse(t, `package svc

type Config struct{}

func Init() {
	cfg := NewConfig()
	cfg.Load()
}
`)
	if findEdge(r, "svc.Init", "svc.Config.Load", "calls") == nil {
		t.Error("missing calls edge from constructor-inferred type resolution")
	}
}

func TestUnaryExpressionTypeInference(t *testing.T) {
	r := parse(t, `package svc

type Server struct{}

func Init() {
	s := &Server{}
	s.Start()
}
`)
	if findEdge(r, "svc.Init", "svc.Server.Start", "calls") == nil {
		t.Error("missing calls edge from &Server{} type inference")
	}
}

func TestCompositeLiteralSliceTypeInference(t *testing.T) {
	r := parse(t, `package svc

type Item struct{}

func Process() {
	items := []Item{{}, {}}
	for _, it := range items {
		it.Save()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Item.Save", "calls") == nil {
		t.Error("missing calls edge from composite literal slice type inference")
	}
}

func TestResolverLocalUnknownType(t *testing.T) {
	r := parse(t, `package svc

func Process() {
	x := getUnknown()
	x.Method()
}
`)
	// x is a local (from short var decl) but type can't be resolved
	// Should still produce a calls edge to x.Method at lower confidence
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.TargetQualified == "x.Method" {
			found = true
		}
	}
	if !found {
		t.Error("missing calls edge x.Method from unresolved local")
	}
}

func TestInterfaceTypeDeclaration(t *testing.T) {
	r := parse(t, `package repo

type Repository interface {
	Find(id string) error
	Save(data interface{}) error
}
`)
	s := findSymbol(r, "repo.Repository")
	if s == nil {
		t.Fatal("missing symbol repo.Repository")
	}
	if s.Kind != "interface" {
		t.Errorf("Repository.Kind = %q, want interface", s.Kind)
	}
	if findSymbol(r, "repo.Repository.Find") == nil {
		t.Error("missing method repo.Repository.Find")
	}
	if findSymbol(r, "repo.Repository.Save") == nil {
		t.Error("missing method repo.Repository.Save")
	}
}

func TestVarDeclSliceTypeCoverage(t *testing.T) {
	r := parse(t, `package svc

type Widget struct{}

func Process() {
	var widgets []Widget
	for _, w := range widgets {
		w.Draw()
	}
}
`)
	if findEdge(r, "svc.Process", "svc.Widget.Draw", "calls") == nil {
		t.Error("missing calls edge from var decl slice type resolution")
	}
}

func TestMethodWithPointerReceiverAndCalls(t *testing.T) {
	r := parse(t, `package svc

type Handler struct{}

func (h *Handler) ServeHTTP() {
	validate()
	h.log()
}
`)
	if findEdge(r, "svc.Handler.ServeHTTP", "validate", "calls") == nil {
		t.Error("missing calls edge to free function")
	}
	if findEdge(r, "svc.Handler.ServeHTTP", "svc.Handler.log", "calls") == nil {
		t.Error("missing calls edge via receiver self-call")
	}
}

func TestEmptyStructNoEmbeddings(t *testing.T) {
	r := parse(t, `package model

type Empty struct{}
`)
	for _, e := range r.edges {
		if string(e.Kind) == "includes" && e.SourceQualified == "model.Empty" {
			t.Errorf("unexpected includes edge from empty struct")
		}
	}
}

func TestNoPackageClauseCoverage(t *testing.T) {
	// Without a package clause, qualify returns bare name
	r := parse(t, `
func Process() {}
`)
	if findSymbol(r, "Process") == nil {
		t.Error("missing symbol Process without package clause")
	}
}

// --- more error propagation tests ---

func TestFunctionSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `package main
func hello() {}
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on function symbol emit")
	}
}

func TestFunctionCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `package main
func f() { g() }
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on function call edge emit")
	}
}

func TestMethodSymbolErrorGo(t *testing.T) {
	err := parseWithEmitter(t, `package main
type S struct{}
func (s S) M() {}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on method symbol emit")
	}
}

func TestMethodCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type S struct{}
func (s S) M() { helper() }
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on method call edge emit")
	}
}

func TestEmbeddingEdgeErrorBoost(t *testing.T) {
	err := parseWithEmitter(t, `package main
type Base struct{}
type Child struct { Base }
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on embedding edge emit")
	}
}

func TestInterfaceMethodSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type Reader interface { Read() }
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on interface method symbol emit")
	}
}

func TestTypeSpecSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type Order struct{}
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on type spec symbol emit")
	}
}

func TestConstGroupThirdError(t *testing.T) {
	err := parseWithEmitter(t, `package main
const (
	A = 1
	B = 2
	C = 3
)
`, &failAfterN{symbolsLeft: 2, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on third const in group")
	}
}

func TestTypeGroupThirdError(t *testing.T) {
	err := parseWithEmitter(t, `package main
type (
	A struct{}
	B struct{}
	C struct{}
)
`, &failAfterN{symbolsLeft: 2, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on third type in group")
	}
}

func TestConstReferenceEdge(t *testing.T) {
	r := parse(t, `package utils

const MaxRetries = 5

func Process() {
	x := MaxRetries
	_ = x
}
`)
	if findEdge(r, "utils.Process", "utils.MaxRetries", "references") == nil {
		t.Error("missing references edge utils.Process -> utils.MaxRetries")
	}
}

func TestConstReferenceSkipsLocals(t *testing.T) {
	r := parse(t, `package utils

const Limit = 10

func Process() {
	Limit := 99
	_ = Limit
}
`)
	if findEdge(r, "utils.Process", "utils.Limit", "references") != nil {
		t.Error("should not emit references edge when local shadows constant")
	}
}

func TestConstReferenceSkipsCallTargets(t *testing.T) {
	r := parse(t, `package utils

const Debug = true

func Process() {
	if Debug {
		fmt.Println("debug")
	}
}
`)
	if findEdge(r, "utils.Process", "utils.Debug", "references") == nil {
		t.Error("missing references edge for constant used in condition")
	}
	// fmt.Println is a call, not a references edge
	if findEdge(r, "utils.Process", "fmt.Println", "references") != nil {
		t.Error("should not emit references edge for call targets")
	}
}

func TestConstReferenceFromMethod(t *testing.T) {
	r := parse(t, `package svc

const timeout = 30

type Server struct{}

func (s *Server) Run() {
	t := timeout
	_ = t
}
`)
	if findEdge(r, "svc.Server.Run", "svc.timeout", "references") == nil {
		t.Error("missing references edge from method to constant")
	}
}

func TestConstReferenceDedup(t *testing.T) {
	r := parse(t, `package svc

const X = 1

func F() {
	a := X
	b := X
	_ = a + b
}
`)
	count := 0
	for _, e := range r.edges {
		if string(e.Kind) == "references" && e.TargetQualified == "svc.X" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 references edge to svc.X, got %d", count)
	}
}

func TestVarDeclarationSymbol(t *testing.T) {
	r := parse(t, `package utils

var localhostIP = "127.0.0.1"
`)
	s := findSymbol(r, "utils.localhostIP")
	if s == nil {
		t.Fatal("missing symbol for package-level var")
	}
	if s.Kind != "constant" {
		t.Errorf("kind = %q, want constant", s.Kind)
	}
}

func TestVarReferenceEdge(t *testing.T) {
	r := parse(t, `package utils

var localhostIP = "127.0.0.1"

func Serve() {
	addr := localhostIP + ":8080"
	_ = addr
}
`)
	if findEdge(r, "utils.Serve", "utils.localhostIP", "references") == nil {
		t.Error("missing references edge for package-level var")
	}
}

func TestConstReferenceGroupedConsts(t *testing.T) {
	r := parse(t, `package svc

const (
	A = 1
	B = 2
)

func F() {
	_ = A + B
}
`)
	if findEdge(r, "svc.F", "svc.A", "references") == nil {
		t.Error("missing references edge for grouped const A")
	}
	if findEdge(r, "svc.F", "svc.B", "references") == nil {
		t.Error("missing references edge for grouped const B")
	}
}

func TestConstReferenceSkipsSelectorOperand(t *testing.T) {
	r := parse(t, `package svc

var cfg = "val"

func F() {
	_ = cfg
	fmt.Println(cfg)
}
`)
	edges := 0
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "svc.cfg" {
			edges++
		}
	}
	if edges != 1 {
		t.Errorf("expected 1 references edge (skip selector operand fmt), got %d", edges)
	}
}

func TestConstReferenceSkipsBlankIdentifier(t *testing.T) {
	r := parse(t, `package svc

const _ = "unused"

func F() {
	_ = 42
}
`)
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "svc._" {
			t.Error("should not emit references edge for blank identifier")
		}
	}
}

func TestConstReferenceFromInit(t *testing.T) {
	r := parse(t, `package svc

const Version = "1.0"

func init() {
	_ = Version
}
`)
	if findEdge(r, "svc.init", "svc.Version", "references") == nil {
		t.Error("missing references edge from init() to constant")
	}
}

func TestConstReferenceSkipsIotaInBody(t *testing.T) {
	r := parse(t, `package svc

func F() {
	_ = iota
}
`)
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "svc.iota" {
			t.Error("should not emit references edge for iota builtin")
		}
	}
}

func TestConstReferenceSkipsSelectorField(t *testing.T) {
	r := parse(t, `package svc

var Timeout = 30

type Config struct {
	Timeout int
}

func F() {
	c := Config{}
	_ = c.Timeout
}
`)
	if findEdge(r, "svc.F", "svc.Timeout", "references") != nil {
		t.Error("should not emit references edge for struct field access via selector")
	}
}

func TestGroupedVarDeclaration(t *testing.T) {
	r := parse(t, `package svc

var (
	X = 1
	Y = "hello"
)

func F() {
	_ = X
	_ = Y
}
`)
	if findEdge(r, "svc.F", "svc.X", "references") == nil {
		t.Error("missing references edge for grouped var X")
	}
	if findEdge(r, "svc.F", "svc.Y", "references") == nil {
		t.Error("missing references edge for grouped var Y")
	}
}
