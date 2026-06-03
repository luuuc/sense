package rust

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
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

// parse is a test helper that parses Rust source and runs the extractor.
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
	if err := ex.Extract(tree, source, "test.rs", r); err != nil {
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
	src := []byte("pub struct Foo;\n")
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, src, "smoke.rs", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the Foo struct")
	}
}

func TestStructExtraction(t *testing.T) {
	r := parse(t, `pub struct Order {
    id: u64,
    total: f64,
}
`)
	s := findSymbol(r, "Order")
	if s == nil {
		t.Fatal("missing symbol Order")
	}
	if s.Kind != "class" {
		t.Errorf("Order.Kind = %q, want class", s.Kind)
	}
	if s.Visibility != "public" {
		t.Errorf("Order.Visibility = %q, want public", s.Visibility)
	}
}

func TestPrivateStruct(t *testing.T) {
	r := parse(t, `struct Inner {
    data: Vec<u8>,
}
`)
	s := findSymbol(r, "Inner")
	if s == nil {
		t.Fatal("missing symbol Inner")
	}
	if s.Visibility != "private" {
		t.Errorf("Inner.Visibility = %q, want private", s.Visibility)
	}
}

func TestPubCrateVisibility(t *testing.T) {
	r := parse(t, `pub(crate) struct Internal;
`)
	s := findSymbol(r, "Internal")
	if s == nil {
		t.Fatal("missing symbol Internal")
	}
	if s.Visibility != "private" {
		t.Errorf("Internal.Visibility = %q, want private for pub(crate)", s.Visibility)
	}
}

func TestEnumExtraction(t *testing.T) {
	r := parse(t, `pub enum Status {
    Active,
    Inactive,
}
`)
	s := findSymbol(r, "Status")
	if s == nil {
		t.Fatal("missing symbol Status")
	}
	if s.Kind != "class" {
		t.Errorf("Status.Kind = %q, want class", s.Kind)
	}
}

func TestTraitExtraction(t *testing.T) {
	r := parse(t, `pub trait Processor {
    fn process(&self);
    fn validate(&self) -> bool;
}
`)
	s := findSymbol(r, "Processor")
	if s == nil {
		t.Fatal("missing symbol Processor")
	}
	if s.Kind != "interface" {
		t.Errorf("Processor.Kind = %q, want interface", s.Kind)
	}
	// Trait methods should be emitted
	m := findSymbol(r, "Processor::process")
	if m == nil {
		t.Fatal("missing symbol Processor::process")
	}
	if m.Kind != "method" {
		t.Errorf("Processor::process.Kind = %q, want method", m.Kind)
	}
	if m.ParentQualified != "Processor" {
		t.Errorf("Processor::process.Parent = %q, want Processor", m.ParentQualified)
	}
	m2 := findSymbol(r, "Processor::validate")
	if m2 == nil {
		t.Fatal("missing symbol Processor::validate")
	}
}

func TestTypeAlias(t *testing.T) {
	r := parse(t, `type Amount = f64;
`)
	s := findSymbol(r, "Amount")
	if s == nil {
		t.Fatal("missing symbol Amount")
	}
	if s.Kind != "type" {
		t.Errorf("Amount.Kind = %q, want type", s.Kind)
	}
}

func TestConstItem(t *testing.T) {
	r := parse(t, `pub const MAX_RETRIES: u32 = 3;
`)
	s := findSymbol(r, "MAX_RETRIES")
	if s == nil {
		t.Fatal("missing symbol MAX_RETRIES")
	}
	if s.Kind != "constant" {
		t.Errorf("MAX_RETRIES.Kind = %q, want constant", s.Kind)
	}
	if s.Visibility != "public" {
		t.Errorf("MAX_RETRIES.Visibility = %q, want public", s.Visibility)
	}
}

func TestStaticItem(t *testing.T) {
	r := parse(t, `static COUNTER: u32 = 0;
`)
	s := findSymbol(r, "COUNTER")
	if s == nil {
		t.Fatal("missing symbol COUNTER")
	}
	if s.Kind != "constant" {
		t.Errorf("COUNTER.Kind = %q, want constant", s.Kind)
	}
}

func TestFunctionExtraction(t *testing.T) {
	r := parse(t, `pub fn process_order(id: u64) -> Result<(), Error> {
    validate(id);
}
`)
	s := findSymbol(r, "process_order")
	if s == nil {
		t.Fatal("missing symbol process_order")
	}
	if s.Kind != "function" {
		t.Errorf("process_order.Kind = %q, want function", s.Kind)
	}
	if s.Visibility != "public" {
		t.Errorf("process_order.Visibility = %q, want public", s.Visibility)
	}
	if findEdge(r, "process_order", "validate", "calls") == nil {
		t.Error("missing calls edge process_order -> validate")
	}
}

func TestImplMethods(t *testing.T) {
	r := parse(t, `struct Order {
    id: u64,
}

impl Order {
    pub fn new(id: u64) -> Self {
        Order { id }
    }
    fn validate(&self) {
        check(self.id);
    }
}
`)
	m := findSymbol(r, "Order::new")
	if m == nil {
		t.Fatal("missing symbol Order::new")
	}
	if m.Kind != "method" {
		t.Errorf("Order::new.Kind = %q, want method", m.Kind)
	}
	if m.Visibility != "public" {
		t.Errorf("Order::new.Visibility = %q, want public", m.Visibility)
	}
	if m.ParentQualified != "Order" {
		t.Errorf("Order::new.Parent = %q, want Order", m.ParentQualified)
	}

	v := findSymbol(r, "Order::validate")
	if v == nil {
		t.Fatal("missing symbol Order::validate")
	}
	if v.Visibility != "private" {
		t.Errorf("Order::validate.Visibility = %q, want private", v.Visibility)
	}
	if findEdge(r, "Order::validate", "check", "calls") == nil {
		t.Error("missing calls edge Order::validate -> check")
	}
}

func TestImplTraitForType(t *testing.T) {
	r := parse(t, `trait Formatter {
    fn format(&self) -> String;
}

struct Money;

impl Formatter for Money {
    fn format(&self) -> String {
        String::new()
    }
}
`)
	// Inherits edge from type to trait
	e := findEdge(r, "Money", "Formatter", "inherits")
	if e == nil {
		t.Fatal("missing inherits edge Money -> Formatter")
	}
	if e.Confidence != 1.0 {
		t.Errorf("inherits confidence = %v, want 1.0", e.Confidence)
	}
	// Method should be qualified through the type
	m := findSymbol(r, "Money::format")
	if m == nil {
		t.Fatal("missing symbol Money::format")
	}
}

func TestDeriveTraits(t *testing.T) {
	r := parse(t, `#[derive(Debug, Clone, Serialize)]
pub struct Config {
    name: String,
}
`)
	if findEdge(r, "Config", "Debug", "inherits") == nil {
		t.Error("missing inherits edge Config -> Debug")
	}
	if findEdge(r, "Config", "Clone", "inherits") == nil {
		t.Error("missing inherits edge Config -> Clone")
	}
	if findEdge(r, "Config", "Serialize", "inherits") == nil {
		t.Error("missing inherits edge Config -> Serialize")
	}
}

func TestModuleExtraction(t *testing.T) {
	r := parse(t, `mod engine {
    pub fn start() {}
    fn stop() {}
}
`)
	s := findSymbol(r, "engine")
	if s == nil {
		t.Fatal("missing symbol engine")
	}
	if s.Kind != "module" {
		t.Errorf("engine.Kind = %q, want module", s.Kind)
	}
	f := findSymbol(r, "engine::start")
	if f == nil {
		t.Fatal("missing symbol engine::start")
	}
	if f.Kind != "function" {
		t.Errorf("engine::start.Kind = %q, want function", f.Kind)
	}
	if f.ParentQualified != "engine" {
		t.Errorf("engine::start.Parent = %q, want engine", f.ParentQualified)
	}
}

func TestNestedModules(t *testing.T) {
	r := parse(t, `mod outer {
    mod inner {
        pub fn helper() {}
    }
}
`)
	s := findSymbol(r, "outer::inner")
	if s == nil {
		t.Fatal("missing symbol outer::inner")
	}
	f := findSymbol(r, "outer::inner::helper")
	if f == nil {
		t.Fatal("missing symbol outer::inner::helper")
	}
}

func TestStructFieldComposition(t *testing.T) {
	r := parse(t, `struct Order {
    customer: Customer,
    total: f64,
}
`)
	e := findEdge(r, "Order", "Customer", "composes")
	if e == nil {
		t.Fatal("missing composes edge Order -> Customer")
	}
	// f64 is a primitive, should not produce a composes edge
	for _, edge := range r.edges {
		if edge.TargetQualified == "f64" {
			t.Error("unexpected composes edge for primitive f64")
		}
	}
}

func TestStructFieldVecWrapper(t *testing.T) {
	r := parse(t, `struct Warehouse {
    items: Vec<Item>,
}
`)
	if findEdge(r, "Warehouse", "Item", "composes") == nil {
		t.Error("missing composes edge Warehouse -> Item from Vec<Item>")
	}
}

func TestStructFieldOptionWrapper(t *testing.T) {
	r := parse(t, `struct Order {
    discount: Option<Discount>,
}
`)
	if findEdge(r, "Order", "Discount", "composes") == nil {
		t.Error("missing composes edge Order -> Discount from Option<Discount>")
	}
}

func TestStructFieldBoxWrapper(t *testing.T) {
	r := parse(t, `struct Tree {
    left: Box<Node>,
}
`)
	if findEdge(r, "Tree", "Node", "composes") == nil {
		t.Error("missing composes edge Tree -> Node from Box<Node>")
	}
}

func TestStructFieldReferenceType(t *testing.T) {
	r := parse(t, `struct View<'a> {
    data: &'a Config,
}
`)
	if findEdge(r, "View", "Config", "composes") == nil {
		t.Error("missing composes edge View -> Config from reference type")
	}
}

func TestEnumVariantComposition(t *testing.T) {
	r := parse(t, `enum Shape {
    Circle(Radius),
    Rectangle { width: Dimension, height: Dimension },
}
`)
	// Tuple variant
	if findEdge(r, "Shape", "Radius", "composes") == nil {
		t.Error("missing composes edge Shape -> Radius from tuple variant")
	}
	// Struct variant
	if findEdge(r, "Shape", "Dimension", "composes") == nil {
		t.Error("missing composes edge Shape -> Dimension from struct variant")
	}
}

func TestSelfMethodCallResolution(t *testing.T) {
	r := parse(t, `struct Service;

impl Service {
    fn process(&self) {
        self.validate();
    }
    fn validate(&self) {}
}
`)
	e := findEdge(r, "Service::process", "Service::validate", "calls")
	if e == nil {
		t.Fatal("missing calls edge Service::process -> Service::validate")
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("self.method() confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
}

func TestTraitMethodResolution(t *testing.T) {
	r := parse(t, `trait Processor {
    fn process(&self);
}

struct Worker;

impl Processor for Worker {
    fn process(&self) {
        self.process();
    }
}
`)
	// self.process() should resolve to Processor::process through trait
	e := findEdge(r, "Worker::process", "Processor::process", "calls")
	if e == nil {
		t.Fatal("missing calls edge Worker::process -> Processor::process")
	}
}

func TestTraitMethodResolutionAmbiguous(t *testing.T) {
	// Two traits declare the same method on the same struct → ambiguous.
	// resolveTraitMethod returns "" and the caller falls back to surface
	// text "self.run" / "method_name" instead of qualifying with a trait.
	r := parse(t, `trait A { fn run(&self); }
trait B { fn run(&self); }

struct Worker;

impl A for Worker { fn run(&self) {} }
impl B for Worker { fn run(&self) {} }

impl Worker {
    fn caller(&self) { self.run(); }
}
`)
	// With both A::run and B::run available, ambiguity falls back to the
	// surface call "Worker::run" — not a trait-qualified resolution.
	if findEdge(r, "Worker::caller", "A::run", "calls") != nil ||
		findEdge(r, "Worker::caller", "B::run", "calls") != nil {
		t.Error("ambiguous trait method should not resolve to a specific trait")
	}
	// Inherent fallback: caller still emits a call edge to the surface
	// receiver-qualified name.
	if findEdge(r, "Worker::caller", "Worker::run", "calls") == nil {
		t.Error("expected fallback call edge Worker::caller -> Worker::run")
	}
}

func TestStructFieldUserGenericTypeComposes(t *testing.T) {
	// A field with a user-defined generic type that isn't in wrapperTypes
	// should produce a composes edge to the generic's base name
	// (rust.go:796-797).
	r := parse(t, `struct MyCache<T> { items: Vec<T> }

struct Server {
    cache: MyCache<String>,
}
`)
	if findEdge(r, "Server", "MyCache", "composes") == nil {
		t.Error("expected composes edge Server -> MyCache for user-defined generic field type")
	}
}

func TestScopedIdentifierCall(t *testing.T) {
	r := parse(t, `fn main() {
    std::io::read();
}
`)
	if findEdge(r, "main", "std::io::read", "calls") == nil {
		t.Error("missing calls edge main -> std::io::read")
	}
}

func TestFieldExpressionCallWithoutImpl(t *testing.T) {
	r := parse(t, `fn process() {
    obj.method();
}
`)
	if findEdge(r, "process", "obj.method", "calls") == nil {
		t.Error("missing calls edge process -> obj.method")
	}
}

func TestExtractorMetadata(t *testing.T) {
	ex := Extractor{}
	if ex.Language() != "rust" {
		t.Errorf("Language() = %q, want rust", ex.Language())
	}
	exts := ex.Extensions()
	if len(exts) != 1 || exts[0] != ".rs" {
		t.Errorf("Extensions() = %v, want [.rs]", exts)
	}
	if ex.Tier() != extract.TierBasic {
		t.Errorf("Tier() = %v, want TierBasic", ex.Tier())
	}
	if ex.Grammar() == nil {
		t.Error("Grammar() returned nil")
	}
}

func TestModuleScopedImplMethods(t *testing.T) {
	r := parse(t, `mod engine {
    struct Motor;
    impl Motor {
        pub fn start(&self) {}
    }
}
`)
	m := findSymbol(r, "engine::Motor::start")
	if m == nil {
		t.Fatal("missing symbol engine::Motor::start")
	}
	if m.ParentQualified != "engine::Motor" {
		t.Errorf("engine::Motor::start.Parent = %q, want engine::Motor", m.ParentQualified)
	}
}

func TestModuleScopedTraitImpl(t *testing.T) {
	r := parse(t, `mod engine {
    trait Runner {
        fn run(&self);
    }
    struct Motor;
    impl Runner for Motor {
        fn run(&self) {}
    }
}
`)
	if findEdge(r, "engine::Motor", "Runner", "inherits") == nil {
		t.Error("missing inherits edge engine::Motor -> Runner")
	}
}

func TestDeriveOnEnum(t *testing.T) {
	r := parse(t, `#[derive(Debug, PartialEq)]
enum Color {
    Red,
    Green,
    Blue,
}
`)
	if findEdge(r, "Color", "Debug", "inherits") == nil {
		t.Error("missing inherits edge Color -> Debug")
	}
	if findEdge(r, "Color", "PartialEq", "inherits") == nil {
		t.Error("missing inherits edge Color -> PartialEq")
	}
}

func TestTupleType(t *testing.T) {
	r := parse(t, `struct Pair {
    data: (Config, Settings),
}
`)
	if findEdge(r, "Pair", "Config", "composes") == nil {
		t.Error("missing composes edge Pair -> Config from tuple type")
	}
	if findEdge(r, "Pair", "Settings", "composes") == nil {
		t.Error("missing composes edge Pair -> Settings from tuple type")
	}
}

func TestGenericStructField(t *testing.T) {
	_ = parse(t, `struct Cache {
    store: HashMap<String, Config>,
}
`)
	// HashMap is in rustStdTypes but not in wrapperTypes, so it does
	// not unwrap to resolve inner type args. No composes edge expected.
}

func TestPubTraitMethodVisibility(t *testing.T) {
	r := parse(t, `pub trait Formatter {
    fn format(&self) -> String;
}
`)
	m := findSymbol(r, "Formatter::format")
	if m == nil {
		t.Fatal("missing symbol Formatter::format")
	}
	// Trait methods inherit visibility from the trait
	if m.Visibility != "public" {
		t.Errorf("Formatter::format.Visibility = %q, want public", m.Visibility)
	}
}

func TestPrivateTraitMethodVisibility(t *testing.T) {
	r := parse(t, `trait Internal {
    fn helper(&self);
}
`)
	m := findSymbol(r, "Internal::helper")
	if m == nil {
		t.Fatal("missing symbol Internal::helper")
	}
	if m.Visibility != "private" {
		t.Errorf("Internal::helper.Visibility = %q, want private", m.Visibility)
	}
}

func TestConstInModule(t *testing.T) {
	r := parse(t, `mod config {
    pub const VERSION: &str = "1.0";
}
`)
	s := findSymbol(r, "config::VERSION")
	if s == nil {
		t.Fatal("missing symbol config::VERSION")
	}
	if s.Kind != "constant" {
		t.Errorf("config::VERSION.Kind = %q, want constant", s.Kind)
	}
}

func TestStructInModule(t *testing.T) {
	r := parse(t, `mod models {
    pub struct User {
        name: String,
    }
}
`)
	s := findSymbol(r, "models::User")
	if s == nil {
		t.Fatal("missing symbol models::User")
	}
	if s.ParentQualified != "models" {
		t.Errorf("models::User.Parent = %q, want models", s.ParentQualified)
	}
}

func TestTraitWithDefaultMethod(t *testing.T) {
	r := parse(t, `trait Greet {
    fn hello(&self) {
        println!("hello");
    }
}
`)
	m := findSymbol(r, "Greet::hello")
	if m == nil {
		t.Fatal("missing symbol Greet::hello for default method")
	}
}

func TestScopedTypeComposition(t *testing.T) {
	r := parse(t, `struct Wrapper {
    inner: other::Config,
}
`)
	if findEdge(r, "Wrapper", "Config", "composes") == nil {
		t.Error("missing composes edge Wrapper -> Config from scoped type")
	}
}

func TestExternalModuleDeclaration(t *testing.T) {
	// `mod foo;` without a body should still emit the module symbol
	r := parse(t, `mod config;
mod utils;
`)
	s := findSymbol(r, "config")
	if s == nil {
		t.Fatal("missing symbol config for external module")
	}
	if s.Kind != "module" {
		t.Errorf("config.Kind = %q, want module", s.Kind)
	}
	s2 := findSymbol(r, "utils")
	if s2 == nil {
		t.Fatal("missing symbol utils for external module")
	}
}

func TestImplForGenericType(t *testing.T) {
	// impl block for a generic type like `impl<T> Handler<T> { ... }`
	r := parse(t, `struct Handler<T> {
    data: T,
}

impl<T> Handler<T> {
    pub fn new(data: T) -> Self {
        Handler { data }
    }
}
`)
	m := findSymbol(r, "Handler::new")
	if m == nil {
		t.Fatal("missing symbol Handler::new from generic impl")
	}
	if m.ParentQualified != "Handler" {
		t.Errorf("Handler::new.Parent = %q, want Handler", m.ParentQualified)
	}
}

func TestImplForScopedType(t *testing.T) {
	// impl block with scoped_type_identifier: `impl other::MyType { ... }`
	r := parse(t, `impl other::MyType {
    fn process(&self) {}
}
`)
	m := findSymbol(r, "MyType::process")
	if m == nil {
		t.Fatal("missing symbol MyType::process from scoped type impl")
	}
}

func TestImplTraitForGenericType(t *testing.T) {
	// `impl Trait for GenericType<T>` exercises unwrapTypeName for generic_type on the type side
	r := parse(t, `trait Display {
    fn fmt(&self);
}

struct Wrapper<T>;

impl<T> Display for Wrapper<T> {
    fn fmt(&self) {}
}
`)
	if findEdge(r, "Wrapper", "Display", "inherits") == nil {
		t.Error("missing inherits edge Wrapper -> Display for generic type")
	}
}

func TestImplTraitForReferenceType(t *testing.T) {
	// `impl Trait for &Type` exercises unwrapTypeName for reference_type
	r := parse(t, `trait AsRef {
    fn as_ref(&self);
}

struct Data;

impl AsRef for &Data {
    fn as_ref(&self) {}
}
`)
	if findEdge(r, "Data", "AsRef", "inherits") == nil {
		t.Error("missing inherits edge Data -> AsRef for reference type")
	}
}

func TestEnumVariantWithVecWrapper(t *testing.T) {
	// Enum variant with wrapper type that should unwrap
	r := parse(t, `enum Container {
    Multiple(Vec<Widget>),
    Single(Widget),
}
`)
	if findEdge(r, "Container", "Widget", "composes") == nil {
		t.Error("missing composes edge Container -> Widget from Vec<Widget> in enum variant")
	}
}

func TestEnumVariantWithOptionWrapper(t *testing.T) {
	r := parse(t, `enum Node {
    Some(Option<Child>),
}
`)
	if findEdge(r, "Node", "Child", "composes") == nil {
		t.Error("missing composes edge Node -> Child from Option<Child> in enum variant")
	}
}

func TestStructFieldArcWrapper(t *testing.T) {
	r := parse(t, `struct Service {
    handler: Arc<Handler>,
}
`)
	if findEdge(r, "Service", "Handler", "composes") == nil {
		t.Error("missing composes edge Service -> Handler from Arc<Handler>")
	}
}

func TestStructFieldMutexWrapper(t *testing.T) {
	r := parse(t, `struct Pool {
    connections: Mutex<Connection>,
}
`)
	if findEdge(r, "Pool", "Connection", "composes") == nil {
		t.Error("missing composes edge Pool -> Connection from Mutex<Connection>")
	}
}

func TestStructFieldNestedWrapper(t *testing.T) {
	// Arc<Mutex<Config>> should unwrap through both wrappers
	r := parse(t, `struct Shared {
    config: Arc<Mutex<Config>>,
}
`)
	if findEdge(r, "Shared", "Config", "composes") == nil {
		t.Error("missing composes edge Shared -> Config from Arc<Mutex<Config>>")
	}
}

func TestImplScopedTrait(t *testing.T) {
	// `impl std::fmt::Display for Foo` uses scoped_type_identifier for the trait
	r := parse(t, `struct Foo;

impl std::fmt::Display for Foo {
    fn fmt(&self) {}
}
`)
	if findEdge(r, "Foo", "Display", "inherits") == nil {
		t.Error("missing inherits edge Foo -> Display from scoped trait")
	}
}

func TestMultipleDeriveAttributes(t *testing.T) {
	// Two separate derive attributes on the same struct
	r := parse(t, `#[derive(Debug)]
#[derive(Clone)]
struct Item;
`)
	if findEdge(r, "Item", "Debug", "inherits") == nil {
		t.Error("missing inherits edge Item -> Debug")
	}
	if findEdge(r, "Item", "Clone", "inherits") == nil {
		t.Error("missing inherits edge Item -> Clone")
	}
}

func TestTraitWithMultipleMethods(t *testing.T) {
	// Trait with both signature items and default implementations
	r := parse(t, `pub trait Handler {
    fn handle(&self);
    fn pre_process(&self) {
        self.validate();
    }
    fn post_process(&self);
}
`)
	if findSymbol(r, "Handler::handle") == nil {
		t.Error("missing symbol Handler::handle")
	}
	if findSymbol(r, "Handler::pre_process") == nil {
		t.Error("missing symbol Handler::pre_process")
	}
	if findSymbol(r, "Handler::post_process") == nil {
		t.Error("missing symbol Handler::post_process")
	}
}

func TestFieldCallOnNonSelfReceiver(t *testing.T) {
	// field_expression on a non-self receiver should fall back to text
	r := parse(t, `struct App;

impl App {
    fn run(&self) {
        other.process();
    }
}
`)
	// Should emit a call edge with the raw text
	e := findEdge(r, "App::run", "other.process", "calls")
	if e == nil {
		t.Error("missing calls edge App::run -> other.process for non-self receiver")
	}
}

func TestStructFieldResultWrapper(t *testing.T) {
	r := parse(t, `struct Response {
    data: Result<Payload, Error>,
}
`)
	// Result is a wrapper type; both Payload and Error should be composed
	if findEdge(r, "Response", "Payload", "composes") == nil {
		t.Error("missing composes edge Response -> Payload from Result<Payload, Error>")
	}
}

func TestModuleWithStructAndImpl(t *testing.T) {
	// Full module with struct and impl to cover nested scope propagation
	r := parse(t, `mod api {
    struct Handler;
    impl Handler {
        pub fn process(&self) {
            validate();
        }
    }
}
`)
	if findSymbol(r, "api::Handler") == nil {
		t.Error("missing symbol api::Handler")
	}
	if findSymbol(r, "api::Handler::process") == nil {
		t.Error("missing symbol api::Handler::process")
	}
	if findEdge(r, "api::Handler::process", "validate", "calls") == nil {
		t.Error("missing calls edge api::Handler::process -> validate")
	}
}

// errForced is a sentinel error used to test error propagation.
var errForced = &testErr{"forced"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// failAfterN is an emitter that returns an error after N successful emits.
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
	return ex.Extract(tree, source, "test.rs", emit)
}

func TestTraitMethodSymbolError(t *testing.T) {
	// Trait emits symbol for itself first, then methods.
	// Fail on second symbol (first trait method).
	err := parseWithEmitter(t, `trait Foo {
    fn bar(&self);
}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing emitter on trait method")
	}
}

func TestStructFieldEdgeError(t *testing.T) {
	// Struct emits symbol + composes edge. Fail on edge.
	err := parseWithEmitter(t, `struct Config {
    inner: Settings,
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on struct field composes edge")
	}
}

func TestEnumVariantCompositionError(t *testing.T) {
	// Enum emits symbol + derives + variant composes. Fail on edge.
	err := parseWithEmitter(t, `enum Msg {
    Data(Config),
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on enum variant composition")
	}
}

func TestImplMethodEdgeError(t *testing.T) {
	// impl block emits inherits edge. Fail on first edge.
	err := parseWithEmitter(t, `struct Foo;
trait Bar {}
impl Bar for Foo {}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on impl inherits edge")
	}
}

func TestFunctionCallEdgeError(t *testing.T) {
	// Function emits symbol + calls edge. Fail on edge.
	err := parseWithEmitter(t, `fn main() {
    helper();
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on function call edge")
	}
}

func TestDeriveEdgeError(t *testing.T) {
	// Struct with derive emits symbol + inherits edges for derives.
	err := parseWithEmitter(t, `#[derive(Clone, Debug)]
struct Config;
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on derive edge")
	}
}

func TestModSymbolError(t *testing.T) {
	// Module emits a symbol. Fail on first symbol.
	err := parseWithEmitter(t, `mod api {
    fn handle() {}
}
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing emitter on module symbol")
	}
}

func TestEnumWithStructVariant(t *testing.T) {
	r := parse(t, `enum Shape {
    Circle { center: Point, radius: f64 },
    Rectangle { top_left: Point, bottom_right: Point },
    Empty,
}
`)
	if findSymbol(r, "Shape") == nil {
		t.Fatal("missing symbol Shape")
	}
	// Struct variant fields with user-defined types emit composes edges
	if findEdge(r, "Shape", "Point", "composes") == nil {
		t.Error("missing composes edge Shape -> Point from struct variant")
	}
}

func TestTraitWithSignatureOnly(t *testing.T) {
	// function_signature_item (no body) vs function_item (with body)
	r := parse(t, `trait Drawable {
    fn draw(&self);
    fn resize(&self, factor: f64);
}
`)
	if findSymbol(r, "Drawable") == nil {
		t.Fatal("missing symbol Drawable")
	}
	if findSymbol(r, "Drawable::draw") == nil {
		t.Error("missing symbol Drawable::draw from signature")
	}
	if findSymbol(r, "Drawable::resize") == nil {
		t.Error("missing symbol Drawable::resize from signature")
	}
}

func TestFreeFunction(t *testing.T) {
	r := parse(t, `fn process(data: &[u8]) -> Result<(), Error> {
    validate(&data);
    Ok(())
}
`)
	s := findSymbol(r, "process")
	if s == nil {
		t.Fatal("missing symbol process")
	}
	if s.Kind != "function" {
		t.Errorf("process.Kind = %q, want function", s.Kind)
	}
	if findEdge(r, "process", "validate", "calls") == nil {
		t.Error("missing calls edge process -> validate")
	}
}

func TestConstItemMultiple(t *testing.T) {
	r := parse(t, `const MAX_SIZE: usize = 1024;
pub const VERSION: &str = "1.0";
`)
	if findSymbol(r, "MAX_SIZE") == nil {
		t.Error("missing symbol MAX_SIZE")
	}
	if findSymbol(r, "VERSION") == nil {
		t.Error("missing symbol VERSION")
	}
}

func TestImplWithMultipleMethods(t *testing.T) {
	r := parse(t, `struct Server;

impl Server {
    pub fn new() -> Self {
        Server
    }

    fn start(&self) {
        self.bind();
    }

    fn bind(&self) {}
}
`)
	if findSymbol(r, "Server::new") == nil {
		t.Error("missing symbol Server::new")
	}
	if findSymbol(r, "Server::start") == nil {
		t.Error("missing symbol Server::start")
	}
	if findSymbol(r, "Server::bind") == nil {
		t.Error("missing symbol Server::bind")
	}
	// Check that self.bind() call is captured (may resolve as "self.bind" or "bind")
	foundBind := false
	for _, e := range r.edges {
		if e.SourceQualified == "Server::start" && string(e.Kind) == "calls" {
			foundBind = true
		}
	}
	if !foundBind {
		t.Error("missing calls edge from Server::start")
	}
}

func TestEnumWithTupleAndStructVariants(t *testing.T) {
	r := parse(t, `enum Message {
    Text(String),
    Move { x: Config, y: Config },
    Quit,
    ChangeColor(Color, f32),
}
`)
	if findSymbol(r, "Message") == nil {
		t.Fatal("missing symbol Message")
	}
	// Config from struct variant, Color from tuple variant
	if findEdge(r, "Message", "Config", "composes") == nil {
		t.Error("missing composes edge Message -> Config from struct variant")
	}
	if findEdge(r, "Message", "Color", "composes") == nil {
		t.Error("missing composes edge Message -> Color from tuple variant")
	}
}

func TestUseDeclarationNoImportEdge(t *testing.T) {
	// Rust extractor doesn't emit imports edges for use declarations in Tier-Basic
	r := parse(t, `use std::collections::HashMap;
use crate::config::Settings;
`)
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Errorf("unexpected imports edge in Rust extractor: %v", e.TargetQualified)
		}
	}
}

func TestTypeAliasGeneric(t *testing.T) {
	r := parse(t, `type Result<T> = std::result::Result<T, Error>;
`)
	if findSymbol(r, "Result") == nil {
		t.Error("missing symbol Result from type alias")
	}
}

func TestPublicVisibility(t *testing.T) {
	r := parse(t, `pub struct Public;
struct Private;
pub(crate) struct CrateLocal;
`)
	pub := findSymbol(r, "Public")
	if pub == nil {
		t.Fatal("missing symbol Public")
	}
	if pub.Visibility != "public" {
		t.Errorf("Public.Visibility = %q, want public", pub.Visibility)
	}
	priv := findSymbol(r, "Private")
	if priv == nil {
		t.Fatal("missing symbol Private")
	}
	if priv.Visibility != "private" {
		t.Errorf("Private.Visibility = %q, want private", priv.Visibility)
	}
}

func TestMethodCallChain(t *testing.T) {
	r := parse(t, `struct Parser;

impl Parser {
    fn parse(&self) {
        self.tokenize();
        self.build_ast();
    }

    fn tokenize(&self) {}
    fn build_ast(&self) {}
}
`)
	// The parse method should have calls edges
	calls := 0
	for _, e := range r.edges {
		if e.SourceQualified == "Parser::parse" && string(e.Kind) == "calls" {
			calls++
		}
	}
	if calls < 2 {
		t.Errorf("expected at least 2 calls from Parser::parse, got %d", calls)
	}
}

func TestUnwrapReferenceType(t *testing.T) {
	r := parse(t, `
struct Config;

impl Config {
    fn from_ref(r: &Config) {}
}
`)
	if findSymbol(r, "Config::from_ref") == nil {
		t.Error("missing symbol Config::from_ref")
	}
}

func TestUnwrapDefaultBranch(t *testing.T) {
	r := parse(t, `
struct Handler {
    callback: fn(u32) -> bool,
}
`)
	// fn pointer type hits the default branch -> no composition edge
	if findSymbol(r, "Handler") == nil {
		t.Fatal("missing symbol Handler")
	}
}

func TestScopedTypeImpl(t *testing.T) {
	r := parse(t, `
mod inner {
    pub struct Widget;
}

impl inner::Widget {
    pub fn draw(&self) {}
}
`)
	_ = findSymbol(r, "inner::Widget")
	if findSymbol(r, "inner") == nil {
		t.Error("missing module symbol inner")
	}
}

func TestEnumVariantWithStructField(t *testing.T) {
	r := parse(t, `
struct Config;
struct Data;

enum Event {
    Created { config: Config },
    Updated { data: Data },
}
`)
	if findEdge(r, "Event", "Config", "composes") == nil {
		t.Error("missing composes edge Event -> Config from struct variant field")
	}
	if findEdge(r, "Event", "Data", "composes") == nil {
		t.Error("missing composes edge Event -> Data from struct variant field")
	}
}

func TestImplExternalWithTraitOnly(t *testing.T) {
	r := parse(t, `
trait Display {}

struct Point {
    x: f64,
    y: f64,
}

impl Display for Point {}
`)
	if findEdge(r, "Point", "Display", "inherits") == nil {
		t.Error("missing inherits edge Point -> Display")
	}
}

func TestFieldCallNonSelfReceiver(t *testing.T) {
	r := parse(t, `
struct Server;

impl Server {
    fn run(&self) {
        self.conn.execute();
    }
}
`)
	s := findSymbol(r, "Server::run")
	if s == nil {
		t.Fatal("missing symbol Server::run")
	}
}

func TestTraitWithTypeAlias(t *testing.T) {
	r := parse(t, `
pub trait Iterator {
    fn next(&mut self) -> bool;
    fn size_hint(&self) -> usize;
}
`)
	if findSymbol(r, "Iterator::next") == nil {
		t.Error("missing method Iterator::next")
	}
	if findSymbol(r, "Iterator::size_hint") == nil {
		t.Error("missing method Iterator::size_hint")
	}
}

func TestStructFieldPrimitivesFiltered(t *testing.T) {
	r := parse(t, `
struct Metric {
    name: String,
    value: f64,
    count: u32,
}
`)
	if findSymbol(r, "Metric") == nil {
		t.Fatal("missing symbol Metric")
	}
	// No composes edges for primitive/std types
	for _, e := range r.edges {
		if string(e.Kind) == "composes" {
			t.Errorf("unexpected composes edge to %q from primitive/std type", e.TargetQualified)
		}
	}
}

func TestConstWithVisibility(t *testing.T) {
	r := parse(t, `
pub const MAX_SIZE: usize = 1024;
const MIN_SIZE: usize = 64;
`)
	s := findSymbol(r, "MAX_SIZE")
	if s == nil {
		t.Fatal("missing symbol MAX_SIZE")
	}
	if s.Visibility != "public" {
		t.Errorf("MAX_SIZE.Visibility = %q, want public", s.Visibility)
	}
	s2 := findSymbol(r, "MIN_SIZE")
	if s2 == nil {
		t.Fatal("missing symbol MIN_SIZE")
	}
	if s2.Visibility != "private" {
		t.Errorf("MIN_SIZE.Visibility = %q, want private", s2.Visibility)
	}
}

func TestGenericImplWithTraitBound(t *testing.T) {
	r := parse(t, `
struct Container<T> {
    item: T,
}

impl<T: Clone> Container<T> {
    fn get(&self) -> T {
        self.item.clone()
    }
}
`)
	if findSymbol(r, "Container::get") == nil {
		t.Error("missing symbol Container::get from generic impl")
	}
}

func TestModuleExternalDeclarationNested(t *testing.T) {
	r := parse(t, `
mod outer {
    mod inner;
}
`)
	if findSymbol(r, "outer") == nil {
		t.Error("missing module symbol outer")
	}
	// inner has no body — should still emit as a symbol
	if findSymbol(r, "outer::inner") == nil {
		t.Error("missing module symbol outer::inner")
	}
}

func TestDeriveWithPath(t *testing.T) {
	r := parse(t, `
#[derive(Debug, Clone, serde::Serialize)]
struct Config {
    name: String,
}
`)
	if findEdge(r, "Config", "Debug", "inherits") == nil {
		t.Error("missing inherits edge Config -> Debug")
	}
	if findEdge(r, "Config", "Clone", "inherits") == nil {
		t.Error("missing inherits edge Config -> Clone")
	}
}

func TestFreeStandingFunctionWithNestedCalls(t *testing.T) {
	r := parse(t, `
fn process() {
    validate();
    transform();
    commit();
}
`)
	if findEdge(r, "process", "validate", "calls") == nil {
		t.Error("missing calls edge process -> validate")
	}
	if findEdge(r, "process", "transform", "calls") == nil {
		t.Error("missing calls edge process -> transform")
	}
	if findEdge(r, "process", "commit", "calls") == nil {
		t.Error("missing calls edge process -> commit")
	}
}

func TestStructFieldResultWrapperWithUserType(t *testing.T) {
	r := parse(t, `
struct AppError;
struct Config;

struct App {
    config: Result<Config, AppError>,
}
`)
	if findEdge(r, "App", "Config", "composes") == nil {
		t.Error("missing composes edge App -> Config from Result wrapper")
	}
	if findEdge(r, "App", "AppError", "composes") == nil {
		t.Error("missing composes edge App -> AppError from Result wrapper")
	}
}

func TestStructFieldTupleType(t *testing.T) {
	r := parse(t, `
struct Point;
struct Color;

struct Object {
    data: (Point, Color),
}
`)
	if findEdge(r, "Object", "Point", "composes") == nil {
		t.Error("missing composes edge Object -> Point from tuple field")
	}
	if findEdge(r, "Object", "Color", "composes") == nil {
		t.Error("missing composes edge Object -> Color from tuple field")
	}
}

func TestImplSelfInherentMethodCall(t *testing.T) {
	r := parse(t, `
struct Engine;

impl Engine {
    fn start(&self) {
        self.initialize();
        self.warm_up();
    }

    fn initialize(&self) {}
    fn warm_up(&self) {}
}
`)
	if findEdge(r, "Engine::start", "Engine::initialize", "calls") == nil {
		t.Error("missing calls edge Engine::start -> Engine::initialize from self call")
	}
	if findEdge(r, "Engine::start", "Engine::warm_up", "calls") == nil {
		t.Error("missing calls edge Engine::start -> Engine::warm_up from self call")
	}
}

func TestCollectDeriveTraitsNoTraits(t *testing.T) {
	r := parse(t, `
struct Empty;
`)
	if findSymbol(r, "Empty") == nil {
		t.Fatal("missing symbol Empty")
	}
	// No derive attributes -> no inherits edges
	for _, e := range r.edges {
		if string(e.Kind) == "inherits" && e.SourceQualified == "Empty" {
			t.Errorf("unexpected inherits edge from Empty to %q", e.TargetQualified)
		}
	}
}

// --- additional error propagation tests (reuses failAfterN from rust_test.go) ---

func TestConstSymbolErrorCoverage(t *testing.T) {
	err := parseWithEmitter(t, `const X: u32 = 1;`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on const symbol emit")
	}
}

func TestImplMethodCallErrorCoverage(t *testing.T) {
	err := parseWithEmitter(t, `
struct S;
impl S {
    fn f(&self) { helper(); }
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on impl method call edge emit")
	}
}

func TestTraitMethodWithDefaultImpl(t *testing.T) {
	r := parse(t, `
pub trait Validator {
    fn validate(&self) -> bool {
        true
    }
    fn name(&self) -> &str;
}
`)
	if findSymbol(r, "Validator::validate") == nil {
		t.Error("missing method Validator::validate from default impl")
	}
	if findSymbol(r, "Validator::name") == nil {
		t.Error("missing method Validator::name from signature")
	}
}

func TestTraitEmptyBody(t *testing.T) {
	r := parse(t, `
trait Marker {}
`)
	if findSymbol(r, "Marker") == nil {
		t.Error("missing symbol Marker")
	}
	// No methods in body -> no method symbols
	for _, s := range r.symbols {
		if s.ParentQualified == "Marker" {
			t.Errorf("unexpected method %q in empty trait", s.Qualified)
		}
	}
}

func TestEnumVariantTupleComposition(t *testing.T) {
	r := parse(t, `
struct Payload;

enum Message {
    Data(Payload),
    Empty,
}
`)
	if findEdge(r, "Message", "Payload", "composes") == nil {
		t.Error("missing composes edge Message -> Payload from tuple variant")
	}
}

func TestEmptyEnumBody(t *testing.T) {
	r := parse(t, `
enum Empty {}
`)
	if findSymbol(r, "Empty") == nil {
		t.Fatal("missing symbol Empty")
	}
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Empty" {
			t.Errorf("unexpected composes edge from empty enum")
		}
	}
}

func TestImplWithMultipleMethodsCoverage(t *testing.T) {
	r := parse(t, `
struct Service;

impl Service {
    pub fn new() -> Self { Service }
    fn start(&self) {
        self.initialize();
    }
    fn stop(&self) {}
}
`)
	if findSymbol(r, "Service::new") == nil {
		t.Error("missing method Service::new")
	}
	if findSymbol(r, "Service::start") == nil {
		t.Error("missing method Service::start")
	}
	if findSymbol(r, "Service::stop") == nil {
		t.Error("missing method Service::stop")
	}
}

func TestModuleWithNestedItems(t *testing.T) {
	r := parse(t, `
pub mod api {
    pub struct Handler;

    pub fn serve() {
        process();
    }
}
`)
	if findSymbol(r, "api") == nil {
		t.Error("missing module symbol api")
	}
	if findSymbol(r, "api::Handler") == nil {
		t.Error("missing symbol api::Handler")
	}
	if findSymbol(r, "api::serve") == nil {
		t.Error("missing function symbol api::serve")
	}
}

func TestStructFieldOptionWrapperCoverage(t *testing.T) {
	r := parse(t, `
struct Config;

struct App {
    config: Option<Config>,
}
`)
	if findEdge(r, "App", "Config", "composes") == nil {
		t.Error("missing composes edge App -> Config from Option wrapper")
	}
}

func TestStructFieldVecWrapperCoverage(t *testing.T) {
	r := parse(t, `
struct Item;

struct Order {
    items: Vec<Item>,
}
`)
	if findEdge(r, "Order", "Item", "composes") == nil {
		t.Error("missing composes edge Order -> Item from Vec wrapper")
	}
}

func TestFreeStandingFunctionEmptyBody(t *testing.T) {
	r := parse(t, `
fn noop() {}
`)
	if findSymbol(r, "noop") == nil {
		t.Fatal("missing symbol noop")
	}
	// Empty body -> no calls edges
	for _, e := range r.edges {
		if e.SourceQualified == "noop" && string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge from empty function body")
		}
	}
}

func TestPreCollectTraitAndImplResolution(t *testing.T) {
	r := parse(t, `
trait Processor {
    fn process(&self);
}

struct Worker;

impl Processor for Worker {
    fn process(&self) {
        execute();
    }
}
`)
	// Worker should have an inherits edge to Processor
	if findEdge(r, "Worker", "Processor", "inherits") == nil {
		t.Error("missing inherits edge Worker -> Processor")
	}
	// process should be qualified as Worker::process
	if findSymbol(r, "Worker::process") == nil {
		t.Error("missing method Worker::process from impl Processor for Worker")
	}
}

// --- more error propagation tests to cover return-err branches ---

func TestTypeDefSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `struct Foo;`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on struct symbol emit")
	}
}

func TestTraitMethodSymbolErrorBoost(t *testing.T) {
	err := parseWithEmitter(t, `
trait Foo {
    fn bar(&self);
}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on trait method symbol emit")
	}
}

func TestDeriveEdgeErrorBoost(t *testing.T) {
	err := parseWithEmitter(t, `
#[derive(Clone)]
struct Foo;
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on derive edge emit")
	}
}

func TestFieldCompositionEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `
struct Bar;
struct Foo {
    bar: Bar,
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on field composition edge emit")
	}
}

func TestEnumVariantCompositionEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `
struct Payload;
enum Msg {
    Data(Payload),
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on enum variant composition edge emit")
	}
}

func TestFunctionSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `fn process() {}`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on function symbol emit")
	}
}

func TestFunctionBodyCallError(t *testing.T) {
	err := parseWithEmitter(t, `fn process() { helper(); }`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on function body call edge emit")
	}
}

func TestImplInheritsEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `
trait Display {}
struct Point;
impl Display for Point {}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on impl inherits edge emit")
	}
}

func TestImplMethodSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `
struct S;
impl S {
    fn method(&self) {}
}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on impl method symbol emit")
	}
}

func TestModSymbolErrorBoost(t *testing.T) {
	err := parseWithEmitter(t, `mod foo {}`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on mod symbol emit")
	}
}

func TestModChildError(t *testing.T) {
	err := parseWithEmitter(t, `
mod foo {
    struct Bar;
}
`, &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on mod child walk")
	}
}

func TestImplBodyCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `
struct S;
impl S {
    fn f(&self) { g(); }
    fn h(&self) {}
}
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on impl method call edge emit")
	}
}

func TestEnumStructFieldEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `
struct Config;
enum Ev { A { c: Config } }
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on enum struct variant field edge emit")
	}
}
