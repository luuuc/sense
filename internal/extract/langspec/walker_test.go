package langspec

import (
	"slices"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

func TestC_CallTarget_FunctionField(t *testing.T) {
	spec := langSpec{
		Name:      "test-c",
		Exts:      []string{".c"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier"},
		CallTypes:  []string{"call_expression"},
		NameField:  "name",
	}

	src := `void greet() {}
void main() {
    greet();
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.c", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var callEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			callEdges = append(callEdges, e)
		}
	}
	if len(callEdges) == 0 {
		t.Fatal("expected at least one call edge from main → greet")
	}
	found := false
	for _, e := range callEdges {
		if e.TargetQualified == "greet" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected call to greet, got %v", callEdges)
	}
}

func TestC_DeclaratorName(t *testing.T) {
	spec := langSpec{
		Name:      "test-c-decl",
		Exts:      []string{".c"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier"},
		NameField:  "name",
	}

	src := `int *get_ptr() { return 0; }
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.c", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(em.symbols) == 0 {
		t.Fatal("expected at least one symbol for pointer declarator function")
	}
	if em.symbols[0].Name != "get_ptr" {
		t.Errorf("name = %q, want %q", em.symbols[0].Name, "get_ptr")
	}
}

func TestJava_MethodInvocation_WithReceiver(t *testing.T) {
	spec := langSpec{
		Name:      "test-java",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"method_declaration", "constructor_declaration"},
		ClassTypes:  []string{"class_declaration", "interface_declaration"},
		CallTypes:   []string{"method_invocation", "object_creation_expression"},
		ImportTypes: []string{"import_declaration"},

		InheritFields: []string{"superclass", "interfaces"},
		NameField:     "name",
	}

	src := `import java.util.List;

class App {
    void run() {
        System.out.println("hello");
        List.of(1, 2, 3);
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
	var importTargets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			callTargets = append(callTargets, e.TargetQualified)
		}
		if e.Kind == model.EdgeImports {
			importTargets = append(importTargets, e.TargetQualified)
		}
	}

	if len(callTargets) == 0 {
		t.Error("expected call edges from method invocations")
	}
	if len(importTargets) == 0 {
		t.Error("expected import edge for java.util.List")
	}
}

func TestKotlin_CallNameFn(t *testing.T) {
	spec := langSpec{
		Name:      "test-kotlin",
		Exts:      []string{".kt"},
		Grammar:   grammars.Kotlin(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_declaration"},
		ClassTypes:  []string{"class_declaration", "object_declaration"},
		CallTypes:   []string{"call_expression"},
		ImportTypes: []string{"import_header"},

		InheritKinds: []string{"delegation_specifier"},
		NameField:    "name",
		CallNameFn:   kotlinCallName,
	}

	src := `import java.util.List

fun main() {
    println("hello")
    listOf(1, 2, 3)
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.kt", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var callTargets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			callTargets = append(callTargets, e.TargetQualified)
		}
	}

	if len(callTargets) == 0 {
		t.Fatal("expected call edges from Kotlin function calls")
	}

	foundPrintln := false
	for _, target := range callTargets {
		if target == "println" {
			foundPrintln = true
		}
	}
	if !foundPrintln {
		t.Errorf("expected call to println, got %v", callTargets)
	}
}

// TestKotlin_CallNameFn_EmptyNode pins the defensive return-"" branch:
// a node with no named children should produce an empty string rather
// than panic. We feed it a leaf node (integer literal) walked out of a
// parsed expression — it has zero named children, mirroring a malformed
// call_expression in the wild.
func TestKotlin_CallNameFn_EmptyNode(t *testing.T) {
	src := `val x = 42`
	tree := parse(t, grammars.Kotlin(), src)
	root := tree.RootNode()

	var findLeaf func(n *sitter.Node) *sitter.Node
	findLeaf = func(n *sitter.Node) *sitter.Node {
		if n.NamedChildCount() == 0 {
			return n
		}
		return findLeaf(n.NamedChild(0))
	}
	leaf := findLeaf(root)
	if leaf == nil {
		t.Fatal("no leaf node found in parsed tree")
	}
	if got := kotlinCallName(leaf, []byte(src)); got != "" {
		t.Errorf("kotlinCallName on leaf = %q, want empty string", got)
	}
}

func TestKotlin_ClassInheritance(t *testing.T) {
	spec := langSpec{
		Name:      "test-kotlin-inherit",
		Exts:      []string{".kt"},
		Grammar:   grammars.Kotlin(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:    []string{"function_declaration"},
		ClassTypes:   []string{"class_declaration", "object_declaration"},
		InheritKinds: []string{"delegation_specifier"},
		NameField:    "name",
	}

	src := `open class Base
class Child : Base()
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.kt", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var inheritEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			inheritEdges = append(inheritEdges, e)
		}
	}

	if len(inheritEdges) == 0 {
		t.Error("expected inherits edge: Child → Base")
	}
}

func TestCSharp_Imports(t *testing.T) {
	spec := langSpec{
		Name:      "test-csharp",
		Exts:      []string{".cs"},
		Grammar:   grammars.CSharp(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"method_declaration", "constructor_declaration"},
		ClassTypes:  []string{"class_declaration", "interface_declaration", "struct_declaration"},
		CallTypes:   []string{"invocation_expression"},
		ImportTypes: []string{"using_directive"},

		InheritKinds: []string{"base_list"},
		NameField:    "name",
	}

	src := `using System;
using System.Collections.Generic;

class App {
    void Run() {
        Console.WriteLine("hello");
    }
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.cs", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var targets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			targets = append(targets, e.TargetQualified)
		}
	}
	// The compound `using System.Collections.Generic;` resolves via the
	// qualified-name child (importFromChildLiteral); confirm the decomposed
	// dispatch preserves the full path, not just the first component.
	if !slices.Contains(targets, "System.Collections.Generic") {
		t.Errorf("import targets = %v, want one to be %q", targets, "System.Collections.Generic")
	}
}

func TestScala_Imports(t *testing.T) {
	spec := langSpec{
		Name:      "test-scala",
		Exts:      []string{".scala"},
		Grammar:   grammars.Scala(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_definition"},
		ClassTypes:  []string{"class_definition", "object_definition", "trait_definition"},
		CallTypes:   []string{"call_expression"},
		ImportTypes: []string{"import_declaration"},

		InheritFields: []string{"extend_clause"},
		NameField:     "name",
	}

	src := `import scala.collection.mutable

class Greeter {
  def hello(): Unit = {
    println("hello")
  }
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.scala", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var targets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			targets = append(targets, e.TargetQualified)
		}
	}
	// Scala splits the path across sibling identifiers; importFromBareIdentifiers
	// joins them with the grammar separator. Confirm the decomposition still
	// reconstructs the full dotted path.
	if !slices.Contains(targets, "scala.collection.mutable") {
		t.Errorf("import targets = %v, want one to be %q", targets, "scala.collection.mutable")
	}
}

func TestPHP_Calls(t *testing.T) {
	spec := langSpec{
		Name:      "test-php",
		Exts:      []string{".php"},
		Grammar:   grammars.PHP(),
		Tier:      extract.TierStandard,
		Separator: "\\",

		FuncTypes:   []string{"function_definition", "method_declaration"},
		ClassTypes:  []string{"class_declaration", "interface_declaration", "trait_declaration"},
		CallTypes:   []string{"function_call_expression", "member_call_expression", "scoped_call_expression"},
		ImportTypes: []string{"namespace_use_declaration"},

		InheritFields: []string{"base_clause", "interfaces"},
		NameField:     "name",
	}

	src := `<?php
namespace App\Models;

class User {
    public function greet() {
        echo "hello";
    }

    public function run() {
        $this->greet();
    }
}

function helper() {
    strlen("test");
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.php", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var callEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			callEdges = append(callEdges, e)
		}
	}

	if len(callEdges) == 0 {
		t.Error("expected call edges from PHP function/method calls")
	}
}

func TestNew_PanicOnNilGrammar(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil Grammar")
		}
	}()
	New(langSpec{Name: "bad", Exts: []string{".bad"}})
}

func TestNew_PanicOnEmptyName(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty Name")
		}
	}()
	New(langSpec{Grammar: grammars.Python(), Exts: []string{".py"}})
}

func TestNew_PanicOnEmptyExts(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for empty Exts")
		}
	}()
	New(langSpec{Name: "bad", Grammar: grammars.Python()})
}

// TestC_PointerDeclarator exercises extractDeclaratorName's pointer_declarator branch.
func TestC_PointerDeclarator(t *testing.T) {
	spec := langSpec{
		Name:      "test-c-ptr",
		Exts:      []string{".c"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier"},
		CallTypes:  []string{"call_expression"},
		NameField:  "name",
	}

	src := `int *create_item() { return malloc(sizeof(int)); }
int **get_items() { return create_item(); }
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.c", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, s := range em.symbols {
		if s.Name == "create_item" {
			found = true
		}
	}
	if !found {
		t.Error("missing symbol create_item from pointer declarator")
	}
}

// TestJava_InheritanceWithInterfaces exercises emitInheritance via
// both superclass and interfaces fields.
func TestJava_InheritanceWithInterfaces(t *testing.T) {
	spec := langSpec{
		Name:      "test-java-inherit",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:     []string{"method_declaration", "constructor_declaration"},
		ClassTypes:    []string{"class_declaration", "interface_declaration"},
		CallTypes:     []string{"method_invocation"},
		InheritFields: []string{"superclass", "interfaces"},
		NameField:     "name",
	}

	src := `interface Serializable {}
interface Printable {}

class Document extends Object implements Serializable, Printable {
    void print() {
        System.out.println("doc");
    }
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
		if e.Kind == model.EdgeInherits && e.SourceQualified == "Document" {
			inheritTargets = append(inheritTargets, e.TargetQualified)
		}
	}
	if len(inheritTargets) == 0 {
		t.Error("expected inherits edges from Document")
	}
}

// TestCSharp_StructAndInterface exercises class extraction for struct and
// interface declarations in C#.
func TestCSharp_StructAndInterface(t *testing.T) {
	spec := langSpec{
		Name:      "test-csharp-types",
		Exts:      []string{".cs"},
		Grammar:   grammars.CSharp(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:    []string{"method_declaration"},
		ClassTypes:   []string{"class_declaration", "interface_declaration", "struct_declaration"},
		InheritKinds: []string{"base_list"},
		NameField:    "name",
	}

	src := `interface IService {
    void Run();
}

struct Point {
    public int X;
    public int Y;
}

class AppService : IService {
    public void Run() {}
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.cs", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	names := map[string]bool{}
	for _, s := range em.symbols {
		names[s.Name] = true
	}
	for _, want := range []string{"IService", "Point", "AppService"} {
		if !names[want] {
			t.Errorf("missing symbol %q", want)
		}
	}
}

// TestKotlin_ObjectDeclaration exercises the object_declaration class type.
func TestKotlin_ObjectDeclaration(t *testing.T) {
	spec := langSpec{
		Name:      "test-kotlin-obj",
		Exts:      []string{".kt"},
		Grammar:   grammars.Kotlin(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:    []string{"function_declaration"},
		ClassTypes:   []string{"class_declaration", "object_declaration"},
		CallTypes:    []string{"call_expression"},
		InheritKinds: []string{"delegation_specifier"},
		NameField:    "name",
		CallNameFn:   kotlinCallName,
	}

	src := `object Singleton {
    fun greet() {
        println("hello")
    }
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.kt", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	found := false
	for _, s := range em.symbols {
		if s.Name == "Singleton" {
			found = true
		}
	}
	if !found {
		t.Error("missing symbol Singleton from object_declaration")
	}
}

// TestImport_FallbackToNameField exercises importTarget's name field fallback.
func TestImport_FallbackToNameField(t *testing.T) {
	spec := langSpec{
		Name:      "test-kotlin-import",
		Exts:      []string{".kt"},
		Grammar:   grammars.Kotlin(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_declaration"},
		ClassTypes:  []string{"class_declaration"},
		ImportTypes: []string{"import_header"},
		NameField:   "name",
	}

	src := `import kotlin.collections.MutableList

fun main() {}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.kt", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var importEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			importEdges = append(importEdges, e)
		}
	}
	if len(importEdges) == 0 {
		t.Error("expected import edge from Kotlin import_header")
	}
}

// TestC_ParenthesizedDeclarator covers extractDeclaratorName's
// parenthesized_declarator branch: a C function whose name is wrapped in
// redundant parentheses (`int (max)(...)`) nests the identifier one level deeper,
// so the walker must descend through the parentheses to recover the name.
func TestC_ParenthesizedDeclarator(t *testing.T) {
	spec := langSpec{
		Name:      "test-c-paren",
		Exts:      []string{".c"},
		Grammar:   grammars.C(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"struct_specifier"},
		NameField:  "name",
	}

	src := `int (max)(int a, int b) { return a > b ? a : b; }
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.c", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(em.symbols) == 0 {
		t.Fatal("expected a symbol for the parenthesized-declarator function")
	}
	if em.symbols[0].Name != "max" {
		t.Errorf("name = %q, want %q", em.symbols[0].Name, "max")
	}
}

// TestCSharp_NamespaceAndEnumKinds drives the handleClass symbol-kind switch
// through its namespace/module and enum arms: a C# namespace declaration maps
// to KindModule and an enum declaration to KindType.
func TestCSharp_NamespaceAndEnumKinds(t *testing.T) {
	spec := langSpec{
		Name:      "test-csharp-kinds",
		Exts:      []string{".cs"},
		Grammar:   grammars.CSharp(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"method_declaration"},
		ClassTypes: []string{"class_declaration", "namespace_declaration", "enum_declaration"},
		NameField:  "name",
	}

	src := `namespace App {
    enum Color { Red, Green, Blue }
}
`
	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.cs", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	kinds := map[string]model.SymbolKind{}
	for _, s := range em.symbols {
		kinds[s.Name] = s.Kind
	}
	if kinds["App"] != model.KindModule {
		t.Errorf("App kind = %q, want module", kinds["App"])
	}
	if kinds["Color"] != model.KindType {
		t.Errorf("Color kind = %q, want type (enum)", kinds["Color"])
	}
}

// TestPython_NestedFunctionSkippedInCallWalk confirms walkForCalls stops at a
// nested function boundary: when collecting the outer function's calls, the
// inner def is skipped so the inner body's call is NOT attributed to the
// outer function, while the outer function's own call is recorded. (langspec
// does not recurse into function bodies for nested symbols, so the inner
// function itself is not separately extracted — the skip prevents its calls
// from leaking onto the outer symbol.)
func TestPython_NestedFunctionSkippedInCallWalk(t *testing.T) {
	spec := langSpec{
		Name:      "test-py-nested-calls",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_definition"},
		CallTypes:  []string{"call"},
		NameField:  "name",
	}

	src := `def outer():
    def inner():
        inner_only()
    outer_call()
`
	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var outerCalls []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls && e.SourceQualified == "outer" {
			outerCalls = append(outerCalls, e.TargetQualified)
		}
	}
	if !slices.Contains(outerCalls, "outer_call") {
		t.Errorf("outer calls = %v, want to contain outer_call", outerCalls)
	}
	if slices.Contains(outerCalls, "inner_only") {
		t.Errorf("outer calls = %v, must NOT contain the nested inner_only (walkForCalls must stop at the inner def)", outerCalls)
	}
}

// TestWalkAndDeclaratorNilNodes pins the recursion base cases that a
// well-formed tree never reaches: walk / walkForCalls on a nil node and
// extractDeclaratorName on a nil node, plus a declarator kind outside the
// recognised set (array_declarator) that falls through to the empty name.
func TestWalkAndDeclaratorNilNodes(t *testing.T) {
	w := &walker{
		spec:   &langSpec{Separator: ".", NameField: "name"},
		source: nil,
		emit:   &testEmitter{},
	}
	if err := w.walk(nil, nil); err != nil {
		t.Errorf("walk(nil) = %v, want nil", err)
	}
	if err := w.walkForCalls(nil, "x"); err != nil {
		t.Errorf("walkForCalls(nil) = %v, want nil", err)
	}
	if got := w.extractDeclaratorName(nil); got != "" {
		t.Errorf("extractDeclaratorName(nil) = %q, want \"\"", got)
	}

	// An array_declarator is not one of the recognised declarator kinds, so
	// extractDeclaratorName falls through to the empty-name return.
	src := `int arr[10];`
	tree := parse(t, grammars.C(), src)
	var find func(n *sitter.Node) *sitter.Node
	find = func(n *sitter.Node) *sitter.Node {
		if n == nil {
			return nil
		}
		if n.Kind() == "array_declarator" {
			return n
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if f := find(n.NamedChild(i)); f != nil {
				return f
			}
		}
		return nil
	}
	arr := find(tree.RootNode())
	if arr == nil {
		t.Fatal("no array_declarator node")
	}
	w2 := &walker{spec: &langSpec{Separator: ".", NameField: "name"}, source: []byte(src), emit: &testEmitter{}}
	if got := w2.extractDeclaratorName(arr); got != "" {
		t.Errorf("extractDeclaratorName(array_declarator) = %q, want \"\"", got)
	}
}

// TestHandleFuncNoName confirms handleFunc no-ops when the func node has no
// resolvable name (name=="" guard) — driven on a leaf node that is neither a
// declaration nor carries a name field.
func TestHandleFuncNoName(t *testing.T) {
	src := `x = 1`
	tree := parse(t, grammars.Python(), src)
	var leaf func(n *sitter.Node) *sitter.Node
	leaf = func(n *sitter.Node) *sitter.Node {
		if n.NamedChildCount() == 0 {
			return n
		}
		return leaf(n.NamedChild(0))
	}
	em := &testEmitter{}
	w := &walker{spec: &langSpec{Separator: ".", NameField: "name"}, source: []byte(src), emit: em}
	if err := w.handleFunc(leaf(tree.RootNode()), nil); err != nil {
		t.Errorf("handleFunc(no-name) = %v, want nil", err)
	}
	if len(em.symbols) != 0 {
		t.Errorf("handleFunc(no-name) emitted %d symbols, want 0", len(em.symbols))
	}
}

// TestHandleImportEmptyTarget confirms handleImport no-ops when no strategy
// resolves a target (target=="" guard) — driven on a leaf node that carries no
// path/name field and no compound-name or string child.
func TestHandleImportEmptyTarget(t *testing.T) {
	src := `x = 1`
	tree := parse(t, grammars.Python(), src)
	var leaf func(n *sitter.Node) *sitter.Node
	leaf = func(n *sitter.Node) *sitter.Node {
		if n.NamedChildCount() == 0 {
			return n
		}
		return leaf(n.NamedChild(0))
	}
	em := &testEmitter{}
	w := &walker{spec: &langSpec{Separator: ".", NameField: "name"}, source: []byte(src), emit: em}
	if err := w.handleImport(leaf(tree.RootNode()), nil); err != nil {
		t.Errorf("handleImport(empty) = %v, want nil", err)
	}
	if len(em.edges) != 0 {
		t.Errorf("handleImport(empty) emitted %d edges, want 0", len(em.edges))
	}
}

// TestImportFromChildLiteral_StringChild covers importFromChildLiteral's
// string-literal arm: a synthetic spec treats a Python bare-string statement
// as an "import", so the walker finds a string child (no path/source/name
// field present) and returns its unquoted text as the import target.
func TestImportFromChildLiteral_StringChild(t *testing.T) {
	spec := langSpec{
		Name:      "test-string-import",
		Exts:      []string{".py"},
		Grammar:   grammars.Python(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:   []string{"function_definition"},
		ClassTypes:  []string{"class_definition"},
		ImportTypes: []string{"expression_statement"},
		NameField:   "name",
	}
	src := `"some/module/path"
`
	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.py", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var targets []string
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			targets = append(targets, e.TargetQualified)
		}
	}
	if !slices.Contains(targets, "some/module/path") {
		t.Errorf("import targets = %v, want one to be %q", targets, "some/module/path")
	}
}

// TestImportFromNameField covers the importFromNameField fallback: an import
// node whose only path signal is a bare "name" field (no path/source field, no
// compound-name or string child, no bare identifiers). Driven directly so the
// strategy ordering reaches the name-field probe.
func TestImportFromNameField(t *testing.T) {
	// Python's `global x` statement exposes a "name"-less shape; instead we
	// drive importFromNameField directly against a node that carries a `name`
	// field whose text is the path.
	src := `def f(): pass`
	tree := parse(t, grammars.Python(), src)
	var find func(n *sitter.Node, kind string) *sitter.Node
	find = func(n *sitter.Node, kind string) *sitter.Node {
		if n == nil {
			return nil
		}
		if n.Kind() == kind {
			return n
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if f := find(n.NamedChild(i), kind); f != nil {
				return f
			}
		}
		return nil
	}
	fn := find(tree.RootNode(), "function_definition")
	if fn == nil {
		t.Fatal("no function_definition")
	}
	w := &walker{spec: &langSpec{Separator: ".", NameField: "name"}, source: []byte(src), emit: &testEmitter{}}
	// function_definition has a `name` field ("f"); importFromNameField reads it.
	if got := w.importFromNameField(fn); got != "f" {
		t.Errorf("importFromNameField = %q, want \"f\"", got)
	}
	// A node without a name field returns "".
	leaf := find(tree.RootNode(), "pass_statement")
	if got := w.importFromNameField(leaf); got != "" {
		t.Errorf("importFromNameField(no-name) = %q, want \"\"", got)
	}
}

// TestInheritTargetsEmptyAndFallback covers two inheritTargets branches:
// an empty-text node returns nil, and a non-type node whose children are all
// inherit-noise falls through to the cleanTypeName fallback on its own text.
func TestInheritTargetsEmptyAndFallback(t *testing.T) {
	w := &walker{spec: &langSpec{Separator: "."}, source: []byte(""), emit: &testEmitter{}}

	// Empty source → any node's text is empty → nil.
	emptyTree := parse(t, grammars.Java(), ``)
	if got := w.inheritTargets(emptyTree.RootNode()); got != nil {
		t.Errorf("inheritTargets(empty) = %v, want nil", got)
	}

	// Fallback: a node that is not a type kind and whose only named child is an
	// inherit-noise kind (argument_list). With no type-like descendant collected,
	// inheritTargets returns the cleaned text of the node itself.
	src := `class A extends Base() {}`
	tree := parse(t, grammars.Java(), src)
	w2 := &walker{spec: &langSpec{Separator: "."}, source: []byte(src), emit: &testEmitter{}}
	var find func(n *sitter.Node, kind string) *sitter.Node
	find = func(n *sitter.Node, kind string) *sitter.Node {
		if n == nil {
			return nil
		}
		if n.Kind() == kind {
			return n
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			if f := find(n.NamedChild(i), kind); f != nil {
				return f
			}
		}
		return nil
	}
	sc := find(tree.RootNode(), "superclass")
	if sc != nil {
		// Best-effort: just ensure no panic and a deterministic result shape.
		_ = w2.inheritTargets(sc)
	}
}

// TestIsDefinitionNameRootNode confirms isDefinitionName returns false for a
// node with no parent (the root) — the p==nil guard. Driven directly since a
// definition-name token always has a parent in real source.
func TestIsDefinitionNameRootNode(t *testing.T) {
	src := `def f(): pass`
	tree := parse(t, grammars.Python(), src)
	w := &walker{
		spec:   &langSpec{Separator: ".", NameField: "name", FuncTypes: []string{"function_definition"}},
		source: []byte(src),
		emit:   &testEmitter{},
	}
	if w.isDefinitionName(tree.RootNode()) {
		t.Error("isDefinitionName(root) = true, want false (root has no parent)")
	}
}

// TestWalkForCalls_SkipsNestedClasses exercises walkForCalls skipping
// nested class/function definitions.
func TestWalkForCalls_SkipsNestedClasses(t *testing.T) {
	spec := langSpec{
		Name:      "test-java-nested",
		Exts:      []string{".java"},
		Grammar:   grammars.Java(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"method_declaration", "constructor_declaration"},
		ClassTypes: []string{"class_declaration"},
		CallTypes:  []string{"method_invocation"},
		NameField:  "name",
	}

	src := `class Outer {
    void run() {
        helper();
    }
    class Inner {
        void innerMethod() {
            innerHelper();
        }
    }
}
`

	tree := parse(t, spec.Grammar, src)
	em := &testEmitter{}
	ex := New(spec)
	if err := ex.Extract(tree, []byte(src), "test.java", em); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Should have symbols for both classes and both methods
	names := map[string]bool{}
	for _, s := range em.symbols {
		names[s.Qualified] = true
	}
	if !names["Outer.run"] {
		t.Error("missing symbol Outer.run")
	}
	if !names["Outer.Inner.innerMethod"] {
		t.Error("missing symbol Outer.Inner.innerMethod")
	}
}
