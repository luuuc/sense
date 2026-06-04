package langspec

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

// testEmitter collects emitted symbols and edges for assertion.
type testEmitter struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (e *testEmitter) Symbol(s extract.EmittedSymbol) error {
	e.symbols = append(e.symbols, s)
	return nil
}

func (e *testEmitter) Edge(ed extract.EmittedEdge) error {
	e.edges = append(e.edges, ed)
	return nil
}

func parse(t *testing.T, lang *sitter.Language, src string) *sitter.Tree {
	t.Helper()
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(lang); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("parse returned nil")
	}
	t.Cleanup(tree.Close)
	return tree
}

func TestSyntheticSpec_ClassesAndMethods(t *testing.T) {
	// Use the Python grammar with a synthetic spec to prove the walker
	// can extract classes, methods, and nesting from config alone.
	spec := langSpec{
		Name:      "test-python",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:     []string{"function_definition"},
		ClassTypes:    []string{"class_definition"},
		CallTypes:     []string{"call"},
		ImportTypes:   []string{"import_from_statement"},
		InheritFields: []string{"superclasses"},
		NameField:     "name",
	}

	src := `
class Animal:
    def speak(self):
        print("hello")

class Dog(Animal):
    def speak(self):
        self.wag()
        print("woof")

def main():
    d = Dog()
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Verify symbols
	wantSymbols := []struct {
		name      string
		qualified string
		kind      model.SymbolKind
		parent    string
	}{
		{"Animal", "Animal", model.KindClass, ""},
		{"speak", "Animal.speak", model.KindMethod, "Animal"},
		{"Dog", "Dog", model.KindClass, ""},
		{"speak", "Dog.speak", model.KindMethod, "Dog"},
		{"main", "main", model.KindFunction, ""},
	}

	if len(em.symbols) != len(wantSymbols) {
		t.Fatalf("got %d symbols, want %d:\n%v", len(em.symbols), len(wantSymbols), em.symbols)
	}

	for i, want := range wantSymbols {
		got := em.symbols[i]
		if got.Name != want.name || got.Qualified != want.qualified || got.Kind != want.kind || got.ParentQualified != want.parent {
			t.Errorf("symbol[%d]: got {%q, %q, %q, parent=%q}, want {%q, %q, %q, parent=%q}",
				i, got.Name, got.Qualified, got.Kind, got.ParentQualified,
				want.name, want.qualified, want.kind, want.parent)
		}
	}

	// Verify edges exist
	if len(em.edges) == 0 {
		t.Error("expected at least one edge (calls or inherits)")
	}

	// Check inheritance edge: Dog → Animal
	foundInherit := false
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits && e.SourceQualified == "Dog" && e.TargetQualified == "Animal" {
			foundInherit = true
		}
	}
	if !foundInherit {
		t.Error("missing inherits edge: Dog → Animal")
	}

	// Check calls edges exist from methods
	foundCall := false
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			foundCall = true
			break
		}
	}
	if !foundCall {
		t.Error("expected at least one calls edge")
	}
}

func TestSyntheticSpec_EmptySlices(t *testing.T) {
	// A spec with no call types should not emit call edges.
	spec := langSpec{
		Name:      "test-minimal",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierBasic,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_definition"},
		NameField:  "name",
	}

	src := `
def hello():
    print("hi")
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(em.symbols) != 1 {
		t.Fatalf("got %d symbols, want 1", len(em.symbols))
	}
	if em.symbols[0].Qualified != "hello" {
		t.Errorf("got qualified %q, want %q", em.symbols[0].Qualified, "hello")
	}
	if len(em.edges) != 0 {
		t.Errorf("got %d edges with empty call/import types, want 0", len(em.edges))
	}
}

func TestSyntheticSpec_ScopeStacking(t *testing.T) {
	// Nested classes produce properly qualified names.
	spec := langSpec{
		Name:      "test-nested",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_definition"},
		NameField:  "name",
	}

	src := `
class Outer:
    class Inner:
        def method(self):
            pass
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	wantQualified := []string{"Outer", "Outer.Inner", "Outer.Inner.method"}
	if len(em.symbols) != len(wantQualified) {
		t.Fatalf("got %d symbols, want %d", len(em.symbols), len(wantQualified))
	}
	for i, want := range wantQualified {
		if em.symbols[i].Qualified != want {
			t.Errorf("symbol[%d].Qualified = %q, want %q", i, em.symbols[i].Qualified, want)
		}
	}
}

func TestSyntheticSpec_Imports(t *testing.T) {
	spec := langSpec{
		Name:      "test-imports",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_definition"},
		ClassTypes:  []string{"class_definition"},
		ImportTypes: []string{"import_from_statement"},
		NameField:   "name",
	}

	src := `
from os.path import join
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(em.edges) == 0 {
		t.Fatal("expected at least one import edge")
	}

	foundImport := false
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			foundImport = true
		}
	}
	if !foundImport {
		t.Error("no import edge found")
	}
}

func TestInheritTargets_KindBasedWithNoise(t *testing.T) {
	// Exercises the recursive inheritTargets path through InheritKinds,
	// verifying that isInheritNoise filters out access specifiers and
	// arguments while still collecting type identifiers nested inside
	// wrapper nodes (e.g., C++ base_class_clause, Java super_interfaces).

	// C++ fixture: `class Circle : public Shape` produces a
	// base_class_clause with children [access_specifier, type_identifier].
	spec := langSpec{
		Name:      "test-cpp-inherit",
		Exts:      []string{".cpp"},
		Grammar:   grammars.Cpp(),
		Tier:      extract.TierStandard,
		Separator: "::",

		FuncTypes:    []string{"function_definition"},
		ClassTypes:   []string{"class_specifier"},
		InheritKinds: []string{"base_class_clause"},
		NameField:    "name",
	}

	src := `class Circle : public Shape {};`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.cpp", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should have Circle and Shape (zero-body class)
	if len(em.symbols) == 0 {
		t.Fatal("expected at least one symbol")
	}

	var inheritEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			inheritEdges = append(inheritEdges, e)
		}
	}

	if len(inheritEdges) != 1 {
		t.Fatalf("got %d inherits edges, want 1: %v", len(inheritEdges), inheritEdges)
	}
	if inheritEdges[0].TargetQualified != "Shape" {
		t.Errorf("inherits target = %q, want %q", inheritEdges[0].TargetQualified, "Shape")
	}
}

func TestSyntheticSpec_NilTree(t *testing.T) {
	spec := langSpec{
		Name:    "test-nil",
		Exts:    []string{".py"},
		Grammar: grammars.Python(),
	}
	em := &testEmitter{}
	ex := New(spec)
	// nil tree should not panic
	if err := ex.Extract(nil, nil, "test.py", em); err != nil {
		t.Fatalf("Extract with nil tree: %v", err)
	}
	if len(em.symbols) != 0 {
		t.Errorf("got %d symbols from nil tree", len(em.symbols))
	}
}

func TestGrammarAndTier(t *testing.T) {
	spec := langSpec{
		Name:    "test-accessors",
		Exts:    []string{".py"},
		Grammar: grammars.Python(),
		Tier:    extract.TierStandard,
	}
	ex := New(spec)
	if ex.Grammar() == nil {
		t.Error("Grammar() returned nil")
	}
	if ex.Tier() != extract.TierStandard {
		t.Errorf("Tier() = %v, want TierStandard", ex.Tier())
	}
	if ex.Language() != "test-accessors" {
		t.Errorf("Language() = %q, want test-accessors", ex.Language())
	}
	exts := ex.Extensions()
	if len(exts) != 1 || exts[0] != ".py" {
		t.Errorf("Extensions() = %v, want [.py]", exts)
	}
}

func TestSyntheticSpec_CallTarget_MethodField(t *testing.T) {
	// Java's method_invocation uses "name" as the method field, but
	// also has an "object" field for the receiver. Exercises the
	// callTarget path for "name" + "object" fields.
	spec := langSpec{
		Name:      "test-java-call",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"method_declaration"},
		ClassTypes: []string{"class_declaration"},
		CallTypes:  []string{"method_invocation", "object_creation_expression"},
		NameField:  "name",
	}

	src := `class App {
    void run() {
        service.process();
        new Widget();
    }
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.java", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var callTargets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			callTargets = append(callTargets, e.TargetQualified)
		}
	}
	if len(callTargets) == 0 {
		t.Fatal("expected call edges from method invocations")
	}
	// service.process() should have receiver "service" joined with method "process"
	foundServiceProcess := false
	for _, t2 := range callTargets {
		if t2 == "service.process" {
			foundServiceProcess = true
		}
	}
	if !foundServiceProcess {
		t.Errorf("expected call to service.process, got %v", callTargets)
	}
}

func TestSyntheticSpec_ClassWithNoName(t *testing.T) {
	// C++ anonymous struct should not crash; walker should descend into it.
	spec := langSpec{
		Name:      "test-cpp",
		Exts:      []string{".cpp"},
		Grammar:   grammars.Cpp(),
		Tier:      extract.TierStandard,
		Separator: "::",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_specifier", "struct_specifier"},
		NameField:  "name",
	}

	src := `struct {
    int x;
} anon;

void helper() {}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.cpp", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should at least find the helper function
	found := false
	for _, s := range em.symbols {
		if s.Name == "helper" {
			found = true
		}
	}
	if !found {
		t.Error("expected helper function to be extracted through anonymous struct")
	}
}

func TestC_FunctionDeclarator(t *testing.T) {
	// C function returning a pointer uses pointer_declarator wrapping
	// function_declarator. Exercises extractDeclaratorName recursion.
	spec := langSpec{
		Name:      "test-c-ptr",
		Exts:      []string{".c"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier"},
		NameField:  "name",
	}

	src := `char *strdup(const char *s) { return 0; }
int **matrix_alloc(int n) { return 0; }
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.c", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	names := map[string]bool{}
	for _, s := range em.symbols {
		names[s.Name] = true
	}
	if !names["strdup"] {
		t.Error("missing symbol strdup (pointer declarator)")
	}
	if !names["matrix_alloc"] {
		t.Error("missing symbol matrix_alloc (double pointer declarator)")
	}
}

func TestSyntheticSpec_ImportWithModuleNameField(t *testing.T) {
	// Python's import_statement and import_from_statement have a
	// "module_name" field. This test exercises that importTarget path.
	spec := langSpec{
		Name:      "test-py-import",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_definition"},
		ClassTypes:  []string{"class_definition"},
		ImportTypes: []string{"import_from_statement", "import_statement"},
		NameField:   "name",
	}

	src := `from os.path import join
import sys
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var importTargets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			importTargets = append(importTargets, e.TargetQualified)
		}
	}
	if len(importTargets) == 0 {
		t.Error("expected import edges")
	}
}

func TestSyntheticSpec_CleanTypeName(t *testing.T) {
	// Java class with generics: `class Foo<T> extends Bar<T>` should clean
	// the type name to "Bar" (stripping the generic parameter).
	spec := langSpec{
		Name:      "test-java-inherit-generic",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:     []string{"method_declaration"},
		ClassTypes:    []string{"class_declaration", "interface_declaration"},
		InheritFields: []string{"superclass", "interfaces"},
		NameField:     "name",
	}

	src := `class MyList extends ArrayList<String> implements Iterable<String> {
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.java", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var inheritTargets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			inheritTargets = append(inheritTargets, e.TargetQualified)
		}
	}
	if len(inheritTargets) == 0 {
		t.Fatal("expected inherit edges for extends/implements")
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

func TestJavaClassSymbolError(t *testing.T) {
	spec := javaSpec()
	tree := parse(t, spec.Grammar, `public class Foo { }`)
	ex := New(spec)
	err := ex.Extract(tree, []byte(`public class Foo { }`), "Foo.java", &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on class symbol emit")
	}
}

func TestJavaMethodSymbolError(t *testing.T) {
	spec := javaSpec()
	src := `public class Foo {
    public void bar() {}
}`
	tree := parse(t, spec.Grammar, src)
	ex := New(spec)
	// Class symbol succeeds, method fails
	err := ex.Extract(tree, []byte(src), "Foo.java", &failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on method symbol emit")
	}
}

func TestJavaInheritanceEdgeError(t *testing.T) {
	spec := javaSpec()
	src := `public class Child extends Parent { }`
	tree := parse(t, spec.Grammar, src)
	ex := New(spec)
	err := ex.Extract(tree, []byte(src), "Child.java", &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on inheritance edge emit")
	}
}

func TestJavaCallEdgeError(t *testing.T) {
	spec := javaSpec()
	src := `public class Foo {
    public void bar() {
        helper();
    }
}`
	tree := parse(t, spec.Grammar, src)
	ex := New(spec)
	err := ex.Extract(tree, []byte(src), "Foo.java", &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on call edge emit")
	}
}

func TestJavaImportEdgeError(t *testing.T) {
	spec := javaSpec()
	src := `import java.util.List;
public class Foo { }`
	tree := parse(t, spec.Grammar, src)
	ex := New(spec)
	err := ex.Extract(tree, []byte(src), "Foo.java", &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on import edge emit")
	}
}

// TestKindBasedInheritanceEdgeError covers the error branch of the kind-based
// inheritance walk (emitInheritance → emitInheritTargets) using C#'s base_list:
// a failing edge emitter must surface the error from the kind-based path, which
// the field-based Java tests never reach.
func TestKindBasedInheritanceEdgeError(t *testing.T) {
	spec := langSpec{
		Name:      "csharp-kind-inherit-err",
		Exts:      []string{".cs"},
		Grammar:   grammars.CSharp(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:    []string{"method_declaration"},
		ClassTypes:   []string{"class_declaration"},
		InheritKinds: []string{"base_list"},
		NameField:    "name",
	}
	src := `class Child : Parent { }`
	tree := parse(t, spec.Grammar, src)
	ex := New(spec)
	err := ex.Extract(tree, []byte(src), "Child.cs", &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error on kind-based inheritance edge emit")
	}
}

func javaSpec() langSpec {
	return langSpec{
		Name:    "java",
		Exts:    []string{".java"},
		Grammar: grammars.Java(),
		Tier:    extract.TierStandard,

		Separator: ".",

		FuncTypes:     []string{"method_declaration", "constructor_declaration"},
		ClassTypes:    []string{"class_declaration", "interface_declaration", "enum_declaration"},
		CallTypes:     []string{"method_invocation"},
		ImportTypes:   []string{"import_declaration"},
		InheritFields: []string{"superclass", "interfaces"},
	}
}
