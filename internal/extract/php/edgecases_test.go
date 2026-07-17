package php

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// TestExoticSourcesExtractWithoutError drives the grammar shapes the main
// tests don't: the global namespace block, group use, trait adaptations,
// untyped properties, dynamic scoped calls, promotion outside a class.
func TestExoticSourcesExtractWithoutError(t *testing.T) {
	srcs := map[string]string{
		"global namespace block": `<?php namespace { class G {} }`,
		"group use":              `<?php use App\{A, B};`,
		"single-segment use":     `<?php use Solo;`,
		"trait adaptation":       `<?php class X { public $legacy; public int $n; use B { b as protected; } }`,
		"interface extends many": `<?php interface I extends A, B {}`,
		"promotion in function":  `<?php function promo(Order $o, $untyped, int $n): void {}`,
		"dynamic dispatch soup": `<?php class C { public function m(): void {
			$a = 5;
			$obj->prop = new Side();
			$x = new $cls();
			$other->b->deepCall();
			$this->untyped->deeperCall();
			Foo::{$x}();
			$arr['k']();
		} }`,
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			mustRun(t, src)
		})
	}
}

func TestGroupUseEmitsNoHalfImports(t *testing.T) {
	em := mustRun(t, `<?php use App\{A, B};`)
	for _, e := range em.edges {
		if e.Kind == model.EdgeImports {
			t.Errorf("group use emitted an import edge: %+v (unsupported shape must stay silent)", e)
		}
	}
}

func TestSingleSegmentUseAliasesItself(t *testing.T) {
	em := mustRun(t, `<?php
use Solo;
class C extends Solo {}
`)
	em.edge(t, model.EdgeInherits, "C", "Solo")
}

func TestUntypedPropertyAndAccessGiveNoWitness(t *testing.T) {
	em := mustRun(t, `<?php
class C {
    public $legacy;
    public function m(): void {
        $this->legacy->refresh();
        $other->b->deepCall();
    }
}
`)
	for _, target := range []string{"refresh", "deepCall"} {
		e := em.edge(t, model.EdgeCalls, `C\m`, target)
		if e.Confidence != extract.ConfidenceNameCollision {
			t.Errorf("%s conf = %v, want the bare-name law", target, e.Confidence)
		}
	}
}

// TestDegenerateNodes calls the per-shape emitters with nodes of the wrong
// kind, covering the nil-field guards that valid trees never reach.
func TestDegenerateNodes(t *testing.T) {
	em := &rec{}
	w := &walker{
		source:    []byte("<?php $x = 1;"),
		emit:      em,
		uses:      map[string]string{},
		propTypes: map[string]map[string]string{},
		parents:   map[string]string{},
	}
	root := parse(t, "<?php $x = 1;").RootNode()

	if err := w.emitFunctionCall(root, "src"); err != nil {
		t.Errorf("emitFunctionCall on program node: %v", err)
	}
	if err := w.emitScopedCall(root, "src", ""); err != nil {
		t.Errorf("emitScopedCall on program node: %v", err)
	}
	if got := w.creationType(root, ""); got != "" {
		t.Errorf("creationType on program node = %q", got)
	}
	if target, _ := w.memberTarget(root, "m", nil, ""); target != "" {
		t.Errorf("memberTarget on program node = %q", target)
	}
	if target, _ := w.memberTarget(nil, "m", nil, ""); target != "" {
		t.Errorf("memberTarget on nil node = %q", target)
	}
	w.recordAssignment(root, map[string]string{})
	if env := w.paramTypes(root, ""); len(env) != 0 {
		t.Errorf("paramTypes on program node = %v", env)
	}
	if err := w.walkCalls(nil, "src", nil, ""); err != nil {
		t.Errorf("walkCalls(nil): %v", err)
	}
	if len(em.edges) != 0 {
		t.Errorf("degenerate nodes emitted edges: %v", em.edges)
	}
}

func TestFunctionAttributeErrorPropagates(t *testing.T) {
	em := &rec{failAnnotatedAt: 1}
	err := run(t, `<?php
#[Command]
function cli(): void {}
`, em)
	if err == nil {
		t.Fatal("want injected annotated error on function, got nil")
	}
}

func TestPromotedParamRecordsPropertyType(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
class C {
    public function __construct(private Gateway $gw) {}
    public function m(): void { $this->gw->charge(); }
}
`)
	e := em.edge(t, model.EdgeCalls, `App\C\m`, `App\Gateway\charge`)
	if e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("promoted-property receiver conf = %v", e.Confidence)
	}
}

// TestBrokenSourcesStayGraceful feeds parse-error shapes: extraction must
// neither panic nor invent symbols.
func TestBrokenSourcesStayGraceful(t *testing.T) {
	for name, src := range map[string]string{
		"nameless class":     `<?php class {}`,
		"bodyless class":     `<?php class X`,
		"nameless function":  `<?php function () {}`,
		"stray use":          `<?php use ;`,
		"nameless namespace": `<?php namespace;`,
	} {
		t.Run(name, func(t *testing.T) {
			mustRun(t, src)
		})
	}
}

func TestNestedDeclarationErrorPropagates(t *testing.T) {
	em := &rec{failSymbolAt: 1}
	err := run(t, `<?php
if (true) {
    class Guarded {}
}
`, em)
	if err == nil {
		t.Fatal("want injected symbol error through the statement recursion, got nil")
	}
}

// TestDegenerateDeclarationNodes drives the declaration handlers' own
// nil-field guards directly (a valid tree never reaches them).
func TestDegenerateDeclarationNodes(t *testing.T) {
	em := &rec{}
	w := &walker{
		source:    []byte("<?php $x = 1;"),
		emit:      em,
		uses:      map[string]string{},
		propTypes: map[string]map[string]string{},
		parents:   map[string]string{},
	}
	root := parse(t, "<?php $x = 1;").RootNode()

	if err := w.handleType(root); err != nil {
		t.Errorf("handleType: %v", err)
	}
	if err := w.handleMethod(root, "C", nil); err != nil {
		t.Errorf("handleMethod: %v", err)
	}
	if err := w.handleFunction(root); err != nil {
		t.Errorf("handleFunction: %v", err)
	}
	if err := w.walkBody(root, "C"); err != nil {
		t.Errorf("walkBody: %v", err)
	}
	if err := w.handleUseClause(root); err != nil {
		t.Errorf("handleUseClause: %v", err)
	}
	if err := w.emitInheritTargets(root, "C", true); err != nil {
		t.Errorf("emitInheritTargets: %v", err)
	}
	if err := w.handleNamespace(root); err != nil {
		t.Errorf("handleNamespace: %v", err)
	}
	if len(em.symbols) != 0 || len(em.edges) != 0 {
		t.Errorf("degenerate declarations emitted: %v %v", em.symbols, em.edges)
	}
}

func TestVariadicParameterIsNotAWitness(t *testing.T) {
	mustRun(t, `<?php function v(...$args) { $args->consume(); }`)
}
