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

		FuncTypes:    []string{"function_definition"},
		ClassTypes:   []string{"class_definition"},
		CallTypes:    []string{"call"},
		ImportTypes:  []string{"import_from_statement"},
		InheritFields: []string{"superclasses"},
		NameField:    "name",

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
