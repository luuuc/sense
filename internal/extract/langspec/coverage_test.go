package langspec

import (
	"testing"

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

		FuncTypes:  []string{"method_declaration", "constructor_declaration"},
		ClassTypes: []string{"class_declaration", "interface_declaration"},
		CallTypes:  []string{"method_invocation", "object_creation_expression"},
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

		FuncTypes:  []string{"function_declaration"},
		ClassTypes: []string{"class_declaration", "object_declaration"},
		CallTypes:  []string{"call_expression"},
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

		FuncTypes:  []string{"method_declaration", "constructor_declaration"},
		ClassTypes: []string{"class_declaration", "interface_declaration", "struct_declaration"},
		CallTypes:  []string{"invocation_expression"},
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

	var importEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			importEdges = append(importEdges, e)
		}
	}
	if len(importEdges) == 0 {
		t.Error("expected import edges for using directives")
	}
}

func TestScala_Imports(t *testing.T) {
	spec := langSpec{
		Name:      "test-scala",
		Exts:      []string{".scala"},
		Grammar:   grammars.Scala(),
		Tier:      extract.TierStandard,
		Separator: ".",

		FuncTypes:  []string{"function_definition"},
		ClassTypes: []string{"class_definition", "object_definition", "trait_definition"},
		CallTypes:  []string{"call_expression"},
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

	var importEdges []extract.EmittedEdge
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			importEdges = append(importEdges, e)
		}
	}
	if len(importEdges) == 0 {
		t.Error("expected import edge for scala.collection.mutable")
	}
}

func TestPHP_Calls(t *testing.T) {
	spec := langSpec{
		Name:      "test-php",
		Exts:      []string{".php"},
		Grammar:   grammars.PHP(),
		Tier:      extract.TierStandard,
		Separator: "\\",

		FuncTypes:  []string{"function_definition", "method_declaration"},
		ClassTypes: []string{"class_declaration", "interface_declaration", "trait_declaration"},
		CallTypes:  []string{"function_call_expression", "member_call_expression", "scoped_call_expression"},
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

