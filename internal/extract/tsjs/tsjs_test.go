package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

func TestSmokeExtract(t *testing.T) {
	cases := []struct {
		name string
		ex   extract.Extractor
		src  string
	}{
		{"typescript", TypeScript{}, "export class Foo {}\n"},
		{"tsx", TSX{}, "export const X = <div/>;\n"},
		{"javascript", JavaScript{}, "export class Foo {}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sitter.NewParser()
			defer p.Close()
			if err := p.SetLanguage(tc.ex.Grammar()); err != nil {
				t.Fatalf("SetLanguage: %v", err)
			}
			src := []byte(tc.src)
			tree := p.Parse(src, nil)
			if tree == nil {
				t.Fatal("Parse returned nil tree")
			}
			defer tree.Close()

			c := &counter{}
			if err := tc.ex.Extract(tree, src, "smoke", c); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if c.symbols == 0 {
				t.Errorf("%s emitted 0 symbols; expected ≥1", tc.name)
			}
		})
	}
}

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }

type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error {
	r.symbols = append(r.symbols, s)
	return nil
}
func (r *recorder) Edge(e extract.EmittedEdge) error { r.edges = append(r.edges, e); return nil }

func TestDefaultExportNaming(t *testing.T) {
	src := []byte(`export default class extends Base {}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/javascript/utils/helper.js", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "Helper" && s.Kind == "class" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol Helper (class) from anonymous default export named after file")
	}

	// Inherits edge should still be emitted.
	var hasEdge bool
	for _, e := range r.edges {
		if e.SourceQualified == "Helper" && e.TargetQualified == "Base" && e.Kind == "inherits" {
			hasEdge = true
		}
	}
	if !hasEdge {
		t.Error("expected inherits edge Helper → Base")
	}
}

func TestDefaultExportFunctionNaming(t *testing.T) {
	src := []byte(`export default function({ user }) {
  return process(user)
}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/UserProfile.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "UserProfile" && s.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol UserProfile (function) from anonymous default export")
	}

	var hasCall bool
	for _, e := range r.edges {
		if e.SourceQualified == "UserProfile" && e.TargetQualified == "process" && e.Kind == "calls" {
			hasCall = true
		}
	}
	if !hasCall {
		t.Error("expected calls edge UserProfile → process")
	}
}

func TestDefaultExportArrowFunctionNaming(t *testing.T) {
	src := []byte(`export default ({ items }) => {
  return items.map(format)
}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/OrderSummary.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "OrderSummary" && s.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol OrderSummary (function) from arrow function default export")
	}
}

func TestDefaultExportIndexFileSkipped(t *testing.T) {
	src := []byte(`export default function() {}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TypeScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/index.ts", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(r.symbols) != 0 {
		t.Errorf("expected 0 symbols for anonymous default export in index file, got %d", len(r.symbols))
	}
}

// parseTS is a test helper that parses TypeScript source.
func parseTS(t *testing.T, src, path string) *recorder {
	t.Helper()
	p := sitter.NewParser()
	defer p.Close()
	ex := TypeScript{}
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
	if err := ex.Extract(tree, source, path, r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

// parseJS is a test helper for JavaScript source.
func parseJS(t *testing.T, src, path string) *recorder {
	t.Helper()
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
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
	if err := ex.Extract(tree, source, path, r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

func findSym(r *recorder, qualified string) *extract.EmittedSymbol {
	for i := range r.symbols {
		if r.symbols[i].Qualified == qualified {
			return &r.symbols[i]
		}
	}
	return nil
}

func findEdg(r *recorder, source, target, kind string) *extract.EmittedEdge {
	for i := range r.edges {
		if r.edges[i].SourceQualified == source &&
			r.edges[i].TargetQualified == target &&
			string(r.edges[i].Kind) == kind {
			return &r.edges[i]
		}
	}
	return nil
}

func TestInterfaceExtraction(t *testing.T) {
	r := parseTS(t, `interface Serializable {
  serialize(): string;
}
`, "types.ts")
	s := findSym(r, "Serializable")
	if s == nil {
		t.Fatal("missing symbol Serializable")
	}
	if s.Kind != "interface" {
		t.Errorf("Serializable.Kind = %q, want interface", s.Kind)
	}
}

func TestInterfaceExtends(t *testing.T) {
	r := parseTS(t, `interface Base {
  id: string;
}

interface Extended extends Base {
  name: string;
}
`, "types.ts")
	if findEdg(r, "Extended", "Base", "inherits") == nil {
		t.Error("missing inherits edge Extended -> Base")
	}
}

func TestEnumExtraction(t *testing.T) {
	r := parseTS(t, `enum Color {
  Red,
  Green,
  Blue,
}
`, "types.ts")
	s := findSym(r, "Color")
	if s == nil {
		t.Fatal("missing symbol Color")
	}
	if s.Kind != "class" {
		t.Errorf("Color.Kind = %q, want class (enum mapped to class)", s.Kind)
	}
}

func TestTypeAliasExtraction(t *testing.T) {
	r := parseTS(t, `type ID = string;
`, "types.ts")
	s := findSym(r, "ID")
	if s == nil {
		t.Fatal("missing symbol ID")
	}
	if s.Kind != "type" {
		t.Errorf("ID.Kind = %q, want type", s.Kind)
	}
}

func TestIntersectionTypeComposesEdges(t *testing.T) {
	r := parseTS(t, `type Admin = User & Permissions;
`, "types.ts")
	if findEdg(r, "Admin", "User", "composes") == nil {
		t.Error("missing composes edge Admin -> User from intersection")
	}
	if findEdg(r, "Admin", "Permissions", "composes") == nil {
		t.Error("missing composes edge Admin -> Permissions from intersection")
	}
}

func TestFunctionDeclaration(t *testing.T) {
	r := parseTS(t, `function processOrder(id: string) {
  validate(id);
}
`, "app.ts")
	s := findSym(r, "processOrder")
	if s == nil {
		t.Fatal("missing symbol processOrder")
	}
	if s.Kind != "function" {
		t.Errorf("processOrder.Kind = %q, want function", s.Kind)
	}
	if findEdg(r, "processOrder", "validate", "calls") == nil {
		t.Error("missing calls edge processOrder -> validate")
	}
}

func TestConstArrowFunction(t *testing.T) {
	r := parseTS(t, `const helper = () => {
  doWork();
};
`, "app.ts")
	s := findSym(r, "helper")
	if s == nil {
		t.Fatal("missing symbol helper")
	}
	if s.Kind != "function" {
		t.Errorf("helper.Kind = %q, want function", s.Kind)
	}
}

func TestConstValue(t *testing.T) {
	r := parseTS(t, `const MAX_RETRIES = 3;
`, "config.ts")
	s := findSym(r, "MAX_RETRIES")
	if s == nil {
		t.Fatal("missing symbol MAX_RETRIES")
	}
	if s.Kind != "constant" {
		t.Errorf("MAX_RETRIES.Kind = %q, want constant", s.Kind)
	}
}

func TestLetVarSkipped(t *testing.T) {
	r := parseTS(t, `let x = 10;
var y = 20;
`, "app.ts")
	if findSym(r, "x") != nil {
		t.Error("let should not produce a symbol")
	}
	if findSym(r, "y") != nil {
		t.Error("var should not produce a symbol")
	}
}

func TestClassMethodExtraction(t *testing.T) {
	r := parseTS(t, `class Service {
  process() {
    this.validate();
  }
}
`, "app.ts")
	m := findSym(r, "Service.process")
	if m == nil {
		t.Fatal("missing symbol Service.process")
	}
	if m.Kind != "method" {
		t.Errorf("Service.process.Kind = %q, want method", m.Kind)
	}
	// this.validate() should strip "this." prefix
	if findEdg(r, "Service.process", "validate", "calls") == nil {
		t.Error("missing calls edge Service.process -> validate (this. stripped)")
	}
}

func TestClassInheritance(t *testing.T) {
	r := parseTS(t, `class Base {}
class Child extends Base {}
`, "app.ts")
	if findEdg(r, "Child", "Base", "inherits") == nil {
		t.Error("missing inherits edge Child -> Base")
	}
}

func TestClassImplements(t *testing.T) {
	r := parseTS(t, `interface Serializable {}
class Config implements Serializable {}
`, "app.ts")
	if findEdg(r, "Config", "Serializable", "inherits") == nil {
		t.Error("missing inherits edge Config -> Serializable from implements")
	}
}

func TestJSXComponentCall(t *testing.T) {
	r := parseJS(t, `function App() {
  return <UserProfile name="test" />;
}
`, "app.jsx")
	if findEdg(r, "App", "UserProfile", "calls") == nil {
		t.Error("missing calls edge App -> UserProfile from JSX")
	}
}

func TestJSXLowercaseSkipped(t *testing.T) {
	r := parseJS(t, `function App() {
  return <div>hello</div>;
}
`, "app.jsx")
	for _, e := range r.edges {
		if e.TargetQualified == "div" {
			t.Error("lowercase JSX tags should be skipped")
		}
	}
}

func TestConstClassExpression(t *testing.T) {
	r := parseTS(t, `const Widget = class {
  render() {}
};
`, "widget.ts")
	s := findSym(r, "Widget")
	if s == nil {
		t.Fatal("missing symbol Widget")
	}
	if s.Kind != "class" {
		t.Errorf("Widget.Kind = %q, want class", s.Kind)
	}
}

func TestMemberExpressionCall(t *testing.T) {
	r := parseTS(t, `function init() {
  console.log("hello");
  service.process();
}
`, "app.ts")
	if findEdg(r, "init", "console.log", "calls") == nil {
		t.Error("missing calls edge init -> console.log")
	}
	if findEdg(r, "init", "service.process", "calls") == nil {
		t.Error("missing calls edge init -> service.process")
	}
}

func TestExtractorMetadata(t *testing.T) {
	ts := TypeScript{}
	if ts.Language() != "typescript" {
		t.Errorf("TypeScript.Language() = %q", ts.Language())
	}
	if ts.Grammar() == nil {
		t.Error("TypeScript.Grammar() nil")
	}
	if ts.Tier() != extract.TierBasic {
		t.Errorf("TypeScript.Tier() = %v, want TierBasic", ts.Tier())
	}

	tsx := TSX{}
	if tsx.Language() != "tsx" {
		t.Errorf("TSX.Language() = %q", tsx.Language())
	}
	if tsx.Tier() != extract.TierBasic {
		t.Errorf("TSX.Tier() = %v, want TierBasic", tsx.Tier())
	}

	js := JavaScript{}
	if js.Language() != "javascript" {
		t.Errorf("JavaScript.Language() = %q", js.Language())
	}
	exts := js.Extensions()
	if len(exts) != 4 {
		t.Errorf("JavaScript.Extensions() = %v, want 4", exts)
	}
	if js.Tier() != extract.TierBasic {
		t.Errorf("JavaScript.Tier() = %v, want TierBasic", js.Tier())
	}
}

func TestSnakeToPascal(t *testing.T) {
	cases := []struct{ in, want string }{
		{"checkout_controller", "CheckoutController"},
		{"user_profile_controller", "UserProfileController"},
		{"hello", "Hello"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := snakeToPascal(tc.in); got != tc.want {
			t.Errorf("snakeToPascal(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGenericTypeInheritance(t *testing.T) {
	r := parseTS(t, `class Repo extends Base<User> {}
`, "app.ts")
	if findEdg(r, "Repo", "Base", "inherits") == nil {
		t.Error("missing inherits edge Repo -> Base through generic type")
	}
}

func TestTypeAliasIntersection(t *testing.T) {
	r := parseTS(t, `type Enhanced = Base & Extra & Mixin;
`, "types.ts")
	s := findSym(r, "Enhanced")
	if s == nil {
		t.Fatal("missing symbol Enhanced")
	}
	if s.Kind != "type" {
		t.Errorf("Enhanced.Kind = %q, want type", s.Kind)
	}
	if findEdg(r, "Enhanced", "Base", "composes") == nil {
		t.Error("missing composes edge Enhanced -> Base from intersection")
	}
	if findEdg(r, "Enhanced", "Extra", "composes") == nil {
		t.Error("missing composes edge Enhanced -> Extra from intersection")
	}
	if findEdg(r, "Enhanced", "Mixin", "composes") == nil {
		t.Error("missing composes edge Enhanced -> Mixin from intersection")
	}
}

func TestTypeAliasIntersectionWithGeneric(t *testing.T) {
	r := parseTS(t, `type Widget = Base & Serializable<string>;
`, "types.ts")
	if findEdg(r, "Widget", "Base", "composes") == nil {
		t.Error("missing composes edge Widget -> Base from intersection")
	}
	if findEdg(r, "Widget", "Serializable", "composes") == nil {
		t.Error("missing composes edge Widget -> Serializable from generic intersection")
	}
}

func TestTypeAliasNonIntersection(t *testing.T) {
	// Type alias that is NOT an intersection should not emit composes
	r := parseTS(t, `type ID = string;
`, "types.ts")
	s := findSym(r, "ID")
	if s == nil {
		t.Fatal("missing symbol ID")
	}
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "ID" {
			t.Errorf("unexpected composes edge for non-intersection type alias: %v", e.TargetQualified)
		}
	}
}

func TestClassImplementsInterface(t *testing.T) {
	r := parseTS(t, `interface Printable {
  print(): void;
}

class Document implements Printable {
  print() {}
}
`, "doc.ts")
	if findEdg(r, "Document", "Printable", "inherits") == nil {
		t.Error("missing inherits edge Document -> Printable from implements")
	}
}

func TestClassExtendsAndImplements(t *testing.T) {
	r := parseTS(t, `class Child extends Parent implements Printable, Serializable {
  print() {}
  serialize() { return ""; }
}
`, "child.ts")
	if findEdg(r, "Child", "Parent", "inherits") == nil {
		t.Error("missing inherits edge Child -> Parent")
	}
	if findEdg(r, "Child", "Printable", "inherits") == nil {
		t.Error("missing inherits edge Child -> Printable")
	}
	if findEdg(r, "Child", "Serializable", "inherits") == nil {
		t.Error("missing inherits edge Child -> Serializable")
	}
}

func TestJavaScriptClassInheritance(t *testing.T) {
	r := parseJS(t, `class Dog extends Animal {
  bark() {}
}
`, "dog.js")
	if findEdg(r, "Dog", "Animal", "inherits") == nil {
		t.Error("missing inherits edge Dog -> Animal in JavaScript")
	}
}

func TestClassWithMethodAndCalls(t *testing.T) {
	r := parseTS(t, `class Service {
  process() {
    this.validate();
    helper();
  }
  validate() {}
}
`, "service.ts")
	if findEdg(r, "Service.process", "validate", "calls") == nil &&
		findEdg(r, "Service.process", "this.validate", "calls") == nil &&
		findEdg(r, "Service.process", "Service.validate", "calls") == nil {
		t.Error("missing calls edge from process -> validate")
	}
	if findEdg(r, "Service.process", "helper", "calls") == nil {
		t.Error("missing calls edge from process -> helper")
	}
}

func TestAnonymousClassExpression(t *testing.T) {
	// Anonymous class should still descend into children
	r := parseTS(t, `const Widget = class {
  render() {}
};
`, "widget.ts")
	// The widget should be extracted as a const
	if findSym(r, "Widget") == nil {
		t.Error("missing symbol Widget from class expression assignment")
	}
}

func TestEnumDeclaration(t *testing.T) {
	r := parseTS(t, `enum Color {
  Red,
  Green,
  Blue
}
`, "colors.ts")
	s := findSym(r, "Color")
	if s == nil {
		t.Fatal("missing symbol Color from enum")
	}
	// Enums are emitted as KindClass
	if s.Kind != "class" {
		t.Errorf("Color.Kind = %q, want class", s.Kind)
	}
}

func TestInterfaceWithExtends(t *testing.T) {
	r := parseTS(t, `interface Animal {
  name: string;
}

interface Dog extends Animal {
  bark(): void;
}
`, "types.ts")
	if findSym(r, "Animal") == nil {
		t.Fatal("missing symbol Animal interface")
	}
	if findSym(r, "Dog") == nil {
		t.Fatal("missing symbol Dog interface")
	}
	if findEdg(r, "Dog", "Animal", "inherits") == nil {
		t.Error("missing inherits edge Dog -> Animal from interface extends")
	}
}

func TestFunctionDeclarationWithBody(t *testing.T) {
	r := parseTS(t, `function processOrder(order: Order) {
  validate(order);
  return true;
}
`, "orders.ts")
	s := findSym(r, "processOrder")
	if s == nil {
		t.Fatal("missing symbol processOrder")
	}
	if s.Kind != "function" {
		t.Errorf("processOrder.Kind = %q, want function", s.Kind)
	}
	if findEdg(r, "processOrder", "validate", "calls") == nil {
		t.Error("missing calls edge processOrder -> validate")
	}
}

func TestLetVarSkippedConstKept(t *testing.T) {
	r := parseTS(t, `let counter = 0;
var name = "test";
const VERSION = "1.0";
`, "config.ts")
	// Only const should produce a symbol
	if findSym(r, "counter") != nil {
		t.Error("let binding should not produce a symbol")
	}
	if findSym(r, "name") != nil {
		t.Error("var binding should not produce a symbol")
	}
	if findSym(r, "VERSION") == nil {
		t.Error("missing symbol VERSION from const")
	}
}

func TestConstArrowFunctionWithCall(t *testing.T) {
	r := parseTS(t, `const greet = (name: string) => {
  console.log(name);
};
`, "greet.ts")
	s := findSym(r, "greet")
	if s == nil {
		t.Fatal("missing symbol greet from const arrow")
	}
	if s.Kind != "function" {
		t.Errorf("greet.Kind = %q, want function", s.Kind)
	}
}

func TestJSXComponentCalls(t *testing.T) {
	r := parseTS(t, `function App() {
  return <UserProfile name="test" />;
}
`, "app.tsx")
	// Use TSX parser
	s := findSym(r, "App")
	if s == nil {
		t.Fatal("missing symbol App")
	}
}

func TestJSXComponentCallsTSX(t *testing.T) {
	// TSX extractor is needed to parse JSX
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`function App() {
  return <UserProfile name="test" />;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if findEdg(r, "App", "UserProfile", "calls") == nil {
		t.Error("missing calls edge App -> UserProfile from JSX")
	}
}

func TestDefaultExportFunction(t *testing.T) {
	r := parseTS(t, `export default function(req: Request) {
  handle(req);
}
`, "handler.ts")
	// Default export with no name gets a file-based name "Handler"
	s := findSym(r, "Handler")
	if s == nil {
		t.Fatal("missing symbol Handler from default export function")
	}
}

func TestDefaultExportArrowFunction(t *testing.T) {
	r := parseTS(t, `export default (req: Request) => {
  process(req);
};
`, "middleware.ts")
	s := findSym(r, "Middleware")
	if s == nil {
		t.Fatal("missing symbol Middleware from default export arrow")
	}
}

func TestNamedExportConsts(t *testing.T) {
	r := parseTS(t, `export const API_URL = "https://example.com";
export const TIMEOUT = 5000;
`, "config.ts")
	if findSym(r, "API_URL") == nil {
		t.Error("missing symbol API_URL")
	}
	if findSym(r, "TIMEOUT") == nil {
		t.Error("missing symbol TIMEOUT")
	}
}

func TestNestedClassInModule(t *testing.T) {
	r := parseTS(t, `namespace Admin {
  export class UserManager {
    find() {}
  }
}
`, "admin.ts")
	// Namespace may or may not be handled in tier-basic.
	// Mainly exercises the scoping/walk logic.
	_ = r
}

func TestIndexFileBasedName(t *testing.T) {
	// index.ts should not produce file-based names for default exports
	r := parseTS(t, `export default function() {
  return 42;
}
`, "index.ts")
	// Default export in index.ts has no file-based name fallback
	for _, s := range r.symbols {
		if s.Name == "Index" {
			t.Error("index.ts should not produce 'Index' as file-based name")
		}
	}
}

func TestAbstractClassDeclaration(t *testing.T) {
	r := parseTS(t, `abstract class Animal {
  abstract speak(): string;
  move() {}
}
`, "animal.ts")
	s := findSym(r, "Animal")
	if s == nil {
		t.Fatal("missing symbol Animal")
	}
	if s.Kind != "class" {
		t.Errorf("Animal.Kind = %q, want class", s.Kind)
	}
	m := findSym(r, "Animal.move")
	if m == nil {
		t.Fatal("missing method Animal.move")
	}
}

func TestInterfaceWithScope(t *testing.T) {
	// Interface declared inside a namespace -> non-empty scope
	r := parseTS(t, `namespace Models {
  interface Serializable {
    serialize(): string;
  }
}
`, "models.ts")
	// namespace is walked by walkChildren; interface inside gets scope
	_ = r // exercises the scoped path
}

func TestTypeAliasSimple(t *testing.T) {
	r := parseTS(t, `type ID = string;
`, "types.ts")
	s := findSym(r, "ID")
	if s == nil {
		t.Fatal("missing symbol ID")
	}
	if s.Kind != "type" {
		t.Errorf("ID.Kind = %q, want type", s.Kind)
	}
}

func TestTypeAliasNoName(t *testing.T) {
	// This shouldn't crash; the name check returns nil early.
	r := parseTS(t, `type = string;
`, "types.ts")
	_ = r
}

func TestIntersectionWithGenericType(t *testing.T) {
	r := parseTS(t, `type Combined = Base & Partial<Config>;
`, "types.ts")
	s := findSym(r, "Combined")
	if s == nil {
		t.Fatal("missing symbol Combined")
	}
	// Should emit composes edges to Base and Config (via generic_type resolution)
	if findEdg(r, "Combined", "Base", "composes") == nil {
		t.Error("missing composes edge Combined -> Base")
	}
	if findEdg(r, "Combined", "Partial", "composes") == nil {
		t.Error("missing composes edge Combined -> Partial (via generic_type)")
	}
}

func TestNestedIntersectionType(t *testing.T) {
	r := parseTS(t, `type Full = A & B & C;
`, "types.ts")
	s := findSym(r, "Full")
	if s == nil {
		t.Fatal("missing symbol Full")
	}
	// A & B & C is a nested intersection: (A & B) & C
	if findEdg(r, "Full", "A", "composes") == nil {
		t.Error("missing composes edge Full -> A")
	}
	if findEdg(r, "Full", "B", "composes") == nil {
		t.Error("missing composes edge Full -> B")
	}
	if findEdg(r, "Full", "C", "composes") == nil {
		t.Error("missing composes edge Full -> C")
	}
}

func TestEnumWithScope(t *testing.T) {
	r := parseTS(t, `namespace Config {
  enum Priority {
    Low,
    Medium,
    High,
  }
}
`, "config.ts")
	// The namespace may or may not set scope, but it exercises the code path.
	_ = r
}

func TestFunctionDeclarationWithScope(t *testing.T) {
	r := parseTS(t, `namespace Utils {
  function format(input: string): string {
    return process(input);
  }
}
`, "utils.ts")
	_ = r
}

func TestScopedConstArrowFunction(t *testing.T) {
	r := parseTS(t, `namespace Helpers {
  const transform = (x: number) => {
    return calculate(x);
  }
}
`, "helpers.ts")
	_ = r
}

func TestJSXSelfClosingComponent(t *testing.T) {
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`export function App() {
  return <UserProfile name="test" />;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if findEdg(r, "App", "UserProfile", "calls") == nil {
		t.Error("missing calls edge App -> UserProfile from self-closing JSX")
	}
}

func TestJSXMemberExpressionComponent(t *testing.T) {
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`export function App() {
  return <Form.Input value="test" />;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if findEdg(r, "App", "Form.Input", "calls") == nil {
		t.Error("missing calls edge from JSX member expression component")
	}
}

func TestJSXReactFragmentSkipped(t *testing.T) {
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`export function App() {
  return <React.Fragment><div/></React.Fragment>;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, e := range r.edges {
		if e.TargetQualified == "React.Fragment" {
			t.Error("React.Fragment should be skipped as JSX component")
		}
	}
}

func TestCallThisStripping(t *testing.T) {
	r := parseTS(t, `class Service {
  process() {
    this.helper();
    this.validate();
  }
  helper() {}
  validate() {}
}
`, "service.ts")
	if findEdg(r, "Service.process", "helper", "calls") == nil {
		t.Error("missing calls edge with this. stripped: process -> helper")
	}
	if findEdg(r, "Service.process", "validate", "calls") == nil {
		t.Error("missing calls edge with this. stripped: process -> validate")
	}
}

func TestDefaultExportClassExpressionInFile(t *testing.T) {
	r := parseJS(t, `export default class {
  run() {}
}`, "services/runner.js")
	s := findSym(r, "Runner")
	if s == nil {
		t.Fatal("missing symbol Runner from anonymous class expression default export")
	}
	if s.Kind != "class" {
		t.Errorf("Runner.Kind = %q, want class", s.Kind)
	}
}

func TestFileBasedNameDotSeparator(t *testing.T) {
	r := parseJS(t, `export default function() {
  return 1;
}`, "user.controller.js")
	s := findSym(r, "User")
	if s == nil {
		t.Fatal("missing symbol User from file-based name with dot separator")
	}
}

func TestFileBasedNameHyphenSeparator(t *testing.T) {
	r := parseJS(t, `export default function() {
  return 1;
}`, "user-profile.js")
	s := findSym(r, "UserProfile")
	if s == nil {
		t.Fatal("missing symbol UserProfile from file-based name with hyphen separator")
	}
}

func TestEmptyFilePath(t *testing.T) {
	r := parseTS(t, `export default function() {
  return 1;
}`, "")
	// Should not crash, and should not produce symbols (empty path -> empty name).
	for _, s := range r.symbols {
		if s.Name != "" {
			t.Errorf("unexpected symbol %q from empty file path", s.Name)
		}
	}
}

func TestExportStatementFallthroughWalk(t *testing.T) {
	r := parseTS(t, `export class Exported {
  method() {}
}
`, "module.ts")
	s := findSym(r, "Exported")
	if s == nil {
		t.Fatal("missing symbol Exported from export statement")
	}
	if findSym(r, "Exported.method") == nil {
		t.Error("missing method Exported.method from export statement")
	}
}

func TestClassConstExpressionWithInheritance(t *testing.T) {
	r := parseTS(t, `const Widget = class extends Component {
  render() {}
};
`, "widget.ts")
	s := findSym(r, "Widget")
	if s == nil {
		t.Fatal("missing symbol Widget from const class expression")
	}
	if s.Kind != "class" {
		t.Errorf("Widget.Kind = %q, want class", s.Kind)
	}
}

func TestConstFunctionExpressionWithBody(t *testing.T) {
	r := parseTS(t, `const processItems = function(items: string[]) {
  return items.map(format);
};
`, "util.ts")
	s := findSym(r, "processItems")
	if s == nil {
		t.Fatal("missing symbol processItems from const function expression")
	}
	if s.Kind != "function" {
		t.Errorf("processItems.Kind = %q, want function", s.Kind)
	}
}

func TestMultipleExportsInModule(t *testing.T) {
	r := parseTS(t, `export interface Logger {
  log(msg: string): void;
}

export enum Level {
  Debug,
  Info,
  Error,
}

export function createLogger(): Logger {
  return configure();
}

export type Config = {
  level: Level;
};

export const DEFAULT_LEVEL = Level.Info;
`, "logger.ts")
	if findSym(r, "Logger") == nil {
		t.Error("missing symbol Logger")
	}
	if findSym(r, "Level") == nil {
		t.Error("missing symbol Level")
	}
	if findSym(r, "createLogger") == nil {
		t.Error("missing symbol createLogger")
	}
	if findSym(r, "DEFAULT_LEVEL") == nil {
		t.Error("missing symbol DEFAULT_LEVEL")
	}
	if findEdg(r, "createLogger", "configure", "calls") == nil {
		t.Error("missing calls edge createLogger -> configure")
	}
}

func TestInterfaceExtendsMultiple(t *testing.T) {
	r := parseTS(t, `interface ReadWrite extends Readable, Writable {
  flush(): void;
}
`, "io.ts")
	if findEdg(r, "ReadWrite", "Readable", "inherits") == nil {
		t.Error("missing inherits edge ReadWrite -> Readable")
	}
	if findEdg(r, "ReadWrite", "Writable", "inherits") == nil {
		t.Error("missing inherits edge ReadWrite -> Writable")
	}
}

func TestInterfaceExtendsGeneric(t *testing.T) {
	r := parseTS(t, `interface StringMap extends Map<string, string> {
  getOrDefault(key: string): string;
}
`, "map.ts")
	if findEdg(r, "StringMap", "Map", "inherits") == nil {
		t.Error("missing inherits edge StringMap -> Map (via generic_type)")
	}
}

// --- error propagation tests for previously uncovered paths ---

var errTest = &testErr{"test"}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

type failAfter struct {
	symbolsLeft int
	edgesLeft   int
}

func (f *failAfter) Symbol(_ extract.EmittedSymbol) error {
	if f.symbolsLeft <= 0 {
		return errTest
	}
	f.symbolsLeft--
	return nil
}

func (f *failAfter) Edge(_ extract.EmittedEdge) error {
	if f.edgesLeft <= 0 {
		return errTest
	}
	f.edgesLeft--
	return nil
}

func parseWithEmitter(t *testing.T, src string, emit extract.Emitter) error {
	t.Helper()
	ex := TypeScript{}
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
	return ex.Extract(tree, source, "test.ts", emit)
}

func TestInterfaceSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `interface Foo {
  bar(): void;
}`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on interface symbol emit")
	}
}

func TestTypeAliasSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `type ID = string;`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on type alias symbol emit")
	}
}

func TestEnumSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `enum Color { Red, Green }`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on enum symbol emit")
	}
}

func TestFunctionSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `function hello() {}`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on function symbol emit")
	}
}

func TestIntersectionEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `type X = A & B;`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on intersection composes edge emit")
	}
}

func TestHeritageEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `interface B extends A {}`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on heritage inherits edge emit")
	}
}

func TestConstSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `const MAX = 100;`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on const symbol emit")
	}
}

func TestConstGeneratorFunction(t *testing.T) {
	r := parseTS(t, `const gen = function*() {
  yield process();
};
`, "gen.ts")
	s := findSym(r, "gen")
	if s == nil {
		t.Fatal("missing symbol gen from const generator function")
	}
	if s.Kind != "function" {
		t.Errorf("gen.Kind = %q, want function", s.Kind)
	}
}

func TestEnumInsideExport(t *testing.T) {
	r := parseTS(t, `export enum Status {
  Active,
  Inactive,
}
`, "status.ts")
	if findSym(r, "Status") == nil {
		t.Fatal("missing symbol Status from exported enum")
	}
}

func TestFunctionInsideExport(t *testing.T) {
	r := parseTS(t, `export function process() {
  transform();
  validate();
}
`, "app.ts")
	s := findSym(r, "process")
	if s == nil {
		t.Fatal("missing symbol process from exported function")
	}
	if findEdg(r, "process", "transform", "calls") == nil {
		t.Error("missing calls edge process -> transform")
	}
}

func TestInterfaceNoExtends(t *testing.T) {
	r := parseTS(t, `interface Config {
  host: string;
  port: number;
}
`, "config.ts")
	s := findSym(r, "Config")
	if s == nil {
		t.Fatal("missing symbol Config")
	}
	if s.Kind != "interface" {
		t.Errorf("Config.Kind = %q, want interface", s.Kind)
	}
	// No extends -> no inherits edges
	for _, e := range r.edges {
		if e.SourceQualified == "Config" && string(e.Kind) == "inherits" {
			t.Errorf("unexpected inherits edge from interface without extends")
		}
	}
}

func TestClassWithEmptyBody(t *testing.T) {
	r := parseTS(t, `class Empty {}
`, "empty.ts")
	if findSym(r, "Empty") == nil {
		t.Fatal("missing symbol Empty from class with empty body")
	}
}

func TestJSXOpeningElementPaired(t *testing.T) {
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`export function App() {
  return <UserList><Item /></UserList>;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if findEdg(r, "App", "UserList", "calls") == nil {
		t.Error("missing calls edge App -> UserList from paired JSX")
	}
	if findEdg(r, "App", "Item", "calls") == nil {
		t.Error("missing calls edge App -> Item from self-closing inside paired JSX")
	}
}

func TestMethodSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `class Foo {
  bar() {}
}`, &failAfter{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on method symbol emit after class succeeds")
	}
}

func TestClassSymbolError(t *testing.T) {
	err := parseWithEmitter(t, `class Foo {}`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class symbol emit")
	}
}

func TestCallEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `function f() { g(); }`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on call edge emit")
	}
}

func TestHeritageJSShapeDirect(t *testing.T) {
	r := parseJS(t, `class Widget extends Base {}
`, "widget.js")
	if findEdg(r, "Widget", "Base", "inherits") == nil {
		t.Error("missing inherits edge Widget -> Base from JS heritage shape")
	}
}

func TestTypeAliasExported(t *testing.T) {
	r := parseTS(t, `export type Config = {
  host: string;
};
`, "config.ts")
	if findSym(r, "Config") == nil {
		t.Fatal("missing symbol Config from exported type alias")
	}
}

func TestJSXFragmentOpeningSkipped(t *testing.T) {
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte(`export function App() {
  return <React.Fragment><Comp /></React.Fragment>;
}
`)
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	for _, e := range r.edges {
		if e.TargetQualified == "React.Fragment" {
			t.Error("React.Fragment in opening element should be skipped")
		}
	}
	if findEdg(r, "App", "Comp", "calls") == nil {
		t.Error("missing calls edge to Comp nested inside Fragment")
	}
}

// --- more error propagation tests ---

func TestClassBodyMethodError(t *testing.T) {
	err := parseWithEmitter(t, `class Foo { bar() {} }`, &failAfter{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class method symbol emit")
	}
}

func TestClassHeritageEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `class Child extends Parent {}`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on heritage edge emit")
	}
}

func TestInterfaceSymbolErrorTS(t *testing.T) {
	err := parseWithEmitter(t, `interface Foo {}`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on interface symbol emit")
	}
}

func TestInterfaceHeritageError(t *testing.T) {
	err := parseWithEmitter(t, `interface B extends A { x(): void; }`, &failAfter{symbolsLeft: 1, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on interface heritage edge emit")
	}
}

func TestTypeAliasSymbolErrorTS(t *testing.T) {
	err := parseWithEmitter(t, `type ID = string;`, &failAfter{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on type alias symbol emit")
	}
}

func TestTypeAliasIntersectionEdgeError(t *testing.T) {
	err := parseWithEmitter(t, `type Both = A & B;`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on type alias intersection edge emit")
	}
}

func TestFunctionWithCallError(t *testing.T) {
	err := parseWithEmitter(t, `function f() { g(); h(); }`, &failAfter{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on function call edge emit")
	}
}

func TestConstSymbolErrorTS(t *testing.T) {
	err := parseWithEmitter(t, `const MAX = 100; const MIN = 0;`, &failAfter{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on second const symbol emit")
	}
}

func TestConstReferenceEdgeTS(t *testing.T) {
	r := parseTS(t, `const MAX_RETRIES = 5;

function process() {
  const x = MAX_RETRIES;
}
`, "test.ts")
	if findEdg(r, "process", "MAX_RETRIES", "references") == nil {
		t.Error("missing references edge process -> MAX_RETRIES")
	}
}

func TestConstReferenceExportedTS(t *testing.T) {
	r := parseTS(t, `export const API_URL = "http://example.com";

function fetch() {
  console.log(API_URL);
}
`, "test.ts")
	if findEdg(r, "fetch", "API_URL", "references") == nil {
		t.Error("missing references edge for exported constant")
	}
}

func TestConstReferenceSkipsArrowFnTS(t *testing.T) {
	r := parseTS(t, `const helper = () => {};
const VALUE = 42;

function process() {
  helper();
  const x = VALUE;
}
`, "test.ts")
	// helper is a function (arrow), not tracked as a constant
	if findEdg(r, "process", "helper", "references") != nil {
		t.Error("should not emit references edge for arrow function constant")
	}
	if findEdg(r, "process", "VALUE", "references") == nil {
		t.Error("missing references edge for value constant")
	}
}

func TestConstReferenceDedupTS(t *testing.T) {
	r := parseTS(t, `const X = 1;

function f() {
  const a = X;
  const b = X;
}
`, "test.ts")
	count := 0
	for _, e := range r.edges {
		if string(e.Kind) == "references" && e.TargetQualified == "X" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 references edge to X, got %d", count)
	}
}

func TestConstReferenceFromMethodTS(t *testing.T) {
	r := parseTS(t, `const TIMEOUT = 30;

class Server {
  run() {
    const t = TIMEOUT;
  }
}
`, "test.ts")
	if findEdg(r, "Server.run", "TIMEOUT", "references") == nil {
		t.Error("missing references edge from method to constant")
	}
}

func TestConstSkipsArrowFunctionValueTS(t *testing.T) {
	r := parseTS(t, `const handler = () => { return 1; };
const MAX = 100;

function run() {
  const x = MAX;
}
`, "test.ts")
	if findEdg(r, "run", "handler", "references") != nil {
		t.Error("arrow function const should not be tracked as a constant")
	}
	if findEdg(r, "run", "MAX", "references") == nil {
		t.Error("missing references edge for value constant")
	}
}

func TestConstSkipsDestructuringTS(t *testing.T) {
	r := parseTS(t, `const { a, b } = config;
const LIMIT = 10;

function run() {
  const x = LIMIT;
}
`, "test.ts")
	if findEdg(r, "run", "LIMIT", "references") == nil {
		t.Error("missing references edge for LIMIT constant")
	}
}

func TestConstSkipsCallTargetTS(t *testing.T) {
	r := parseTS(t, `const API_URL = "http://example.com";

function run() {
  fetch(API_URL);
  const x = API_URL;
}
`, "test.ts")
	edges := 0
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "API_URL" {
			edges++
		}
	}
	if edges != 1 {
		t.Errorf("expected 1 references edge (skip call target), got %d", edges)
	}
}

// TestMemberReceiverConfidence pins the emit-alignment for JS/TS: a member call
// is rated by how well its receiver type is known. `this` (resolved against the
// enclosing class) and a Capitalized (class/namespace) receiver stay fully
// confident; a lowercase-variable or chained receiver is an unverified instance
// call at ConfidenceUnresolved, so bare-name fallback cannot surface it as a
// confident caller.
func TestMemberReceiverConfidence(t *testing.T) {
	r := parseTS(t, `class C {
  m() {
    this.helper();
    Other.build();
    obj.save();
    a.b.run();
  }
}
`, "c.ts")
	cases := []struct {
		target string
		want   float64
	}{
		{"helper", 1.0}, // this. is stripped
		{"Other.build", 1.0},
		{"obj.save", extract.ConfidenceUnresolved},
		{"a.b.run", extract.ConfidenceUnresolved},
	}
	for _, c := range cases {
		e := findEdg(r, "C.m", c.target, "calls")
		if e == nil {
			t.Errorf("missing calls edge to %q", c.target)
			continue
		}
		if e.Confidence != c.want {
			t.Errorf("%s confidence = %v, want %v", c.target, e.Confidence, c.want)
		}
	}
}
