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

