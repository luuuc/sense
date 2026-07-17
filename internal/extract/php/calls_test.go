package php

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func TestThisAndSelfCallsResolveToClass(t *testing.T) {
	em := mustRun(t, `<?php
class Checkout {
    public function run(): void {
        $this->finalize();
        self::make();
        static::make();
    }
    private function finalize(): void {}
    public static function make(): void {}
}
`)
	if e := em.edge(t, model.EdgeCalls, `Checkout\run`, `Checkout\finalize`); e.Confidence != extract.ConfidenceStatic {
		t.Errorf("$this call conf = %v", e.Confidence)
	}
	found := 0
	for _, e := range em.edges {
		if e.TargetQualified == `Checkout\make` && e.Confidence == extract.ConfidenceStatic {
			found++
		}
	}
	if found != 2 {
		t.Errorf("self::/static:: calls = %d, want 2", found)
	}
}

func TestTypedReceiversResolveAtDynamicConfidence(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Models\Order;
class Checkout {
    private Logger $log;
    public function __construct(private Gateway $gateway) {}
    public function run(Order $order): void {
        $order->total();
        $this->log->info("x");
        $this->gateway->charge(1);
        $tax = new TaxCalculator();
        $tax->rate();
    }
}
`)
	cases := []string{
		`App\Models\Order\total`,
		`App\Logger\info`,
		`App\Gateway\charge`,
		`App\TaxCalculator\rate`,
	}
	for _, target := range cases {
		e := em.edge(t, model.EdgeCalls, `App\Checkout\run`, target)
		if e.Confidence != extract.ConfidenceDynamic {
			t.Errorf("%s conf = %v, want %v", target, e.Confidence, extract.ConfidenceDynamic)
		}
	}
	if e := em.edge(t, model.EdgeCalls, `App\Checkout\run`, `App\TaxCalculator`); e.Confidence != extract.ConfidenceStatic {
		t.Errorf("new TaxCalculator conf = %v", e.Confidence)
	}
}

func TestUnresolvedReceiverFollowsTheLaw(t *testing.T) {
	em := mustRun(t, `<?php
class C {
    public function run(): void {
        $unknown->save();
        $unknown->finalizeLater();
        $unknown->{$dyn}();
        $chained->one()->two();
    }
}
`)
	// Clause 2: `save` is a common name - refused outright.
	if em.hasEdgeTarget("save") {
		t.Errorf("common-name save must emit no edge: %v", em.edges)
	}
	// Clause 1: a non-common bare name carries ConfidenceNameCollision.
	e := em.edge(t, model.EdgeCalls, `C\run`, "finalizeLater")
	if e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("bare-name conf = %v, want %v", e.Confidence, extract.ConfidenceNameCollision)
	}
	// Dynamic method name - no literal target, no edge.
	for _, edge := range em.edges {
		if edge.TargetQualified == "" {
			t.Errorf("empty-target edge emitted: %+v", edge)
		}
	}
	// A chained receiver has no witness: `two` falls to the law as well.
	if e := em.edge(t, model.EdgeCalls, `C\run`, "two"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("chained-receiver conf = %v", e.Confidence)
	}
}

func TestParentCalls(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
class Child extends Base {
    public function boot(): void { parent::boot(); }
}
class Orphan {
    public function boot(): void { parent::boot(); }
}
`)
	if e := em.edge(t, model.EdgeCalls, `App\Child\boot`, `App\Base\boot`); e.Confidence != extract.ConfidenceStatic {
		t.Errorf("parent:: conf = %v", e.Confidence)
	}
	// No extends recorded - parent:: has no target, so no edge.
	for _, e := range em.edges {
		if e.SourceQualified == `App\Orphan\boot` {
			t.Errorf("orphan parent:: emitted %+v", e)
		}
	}
}

func TestScopedAndFunctionCalls(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Models\Order;
class C {
    public function run(): void {
        Order::query();
        \Vendor\Util::probe();
        format_amount(1);
        \App\Support\helper();
        $callable();
    }
}
`)
	em.edge(t, model.EdgeCalls, `App\C\run`, `App\Models\Order\query`)
	em.edge(t, model.EdgeCalls, `App\C\run`, `Vendor\Util\probe`)
	em.edge(t, model.EdgeCalls, `App\C\run`, `format_amount`)
	em.edge(t, model.EdgeCalls, `App\C\run`, `App\Support\helper`)
	for _, e := range em.edges {
		if e.TargetQualified == "$callable" || e.TargetQualified == "callable" {
			t.Errorf("variable call emitted an edge: %+v", e)
		}
	}
}

func TestSelfOutsideClassEmitsNothing(t *testing.T) {
	em := mustRun(t, `<?php
function f(): void {
    self::boom();
    $x = new static();
}
`)
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			t.Errorf("unexpected call edge outside class: %+v", e)
		}
	}
}

func TestNewStaticAndSelfResolveToEnclosingClass(t *testing.T) {
	em := mustRun(t, `<?php
class Factory {
    public static function make(): static { return new static(); }
    public static function clone(): self { return new self(); }
}
`)
	found := 0
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls && e.TargetQualified == `Factory` {
			found++
		}
	}
	if found != 2 {
		t.Errorf("new static/self edges = %d, want 2", found)
	}
}

func TestUnionAndPrimitiveTypesGiveNoWitness(t *testing.T) {
	em := mustRun(t, `<?php
class C {
    public function run(int $n, Order|Invoice $mixed, ?Logger $log): void {
        $mixed->totalize();
        $log->warn("x");
    }
}
`)
	// Union type: ambiguous, falls to the law.
	if e := em.edge(t, model.EdgeCalls, `C\run`, "totalize"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("union-typed receiver conf = %v", e.Confidence)
	}
	// Nullable single type is a witness.
	if e := em.edge(t, model.EdgeCalls, `C\run`, `Logger\warn`); e.Confidence != extract.ConfidenceDynamic {
		t.Errorf("nullable-typed receiver conf = %v", e.Confidence)
	}
}

func TestAbstractAndInterfaceMethodsHaveNoBodies(t *testing.T) {
	em := mustRun(t, `<?php
abstract class Base {
    abstract protected function step(): void;
}
interface I {
    public function sig(): void;
}
`)
	em.symbol(t, `Base\step`)
	em.symbol(t, `I\sig`)
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			t.Errorf("bodyless method emitted call %+v", e)
		}
	}
}

func TestTopLevelStatementsEmitCalls(t *testing.T) {
	// The routes-file shape: Laravel's routes/web.php is nothing but
	// top-level facade calls. This is card 4's entry fixture.
	em := mustRun(t, `<?php
use Illuminate\Support\Facades\Route;
Route::get('/orders', [OrderController::class, 'index']);
helper_boot();
$app->run();
`)
	e := em.edge(t, model.EdgeCalls, "", `Illuminate\Support\Facades\Route\get`)
	if e.Confidence != extract.ConfidenceStatic {
		t.Errorf("top-level scoped call conf = %v", e.Confidence)
	}
	em.edge(t, model.EdgeCalls, "", `helper_boot`)
	// $app is untyped at top level and `run` is a common name: refused.
	if em.hasEdgeTarget("run") {
		t.Errorf("common-name top-level member call must emit no edge: %v", em.edges)
	}
}

func TestTopLevelCallsAttributeToNamespace(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Boot;
wire_things();
`)
	em.edge(t, model.EdgeCalls, `App\Boot`, "wire_things")
}

func TestCommonNameGuardIsCaseInsensitive(t *testing.T) {
	em := mustRun(t, `<?php
class C {
    public function m(): void {
        $x->ToArray();
        $x->SAVE();
        $x->FinalizeLater();
    }
}
`)
	for _, folded := range []string{"ToArray", "SAVE"} {
		if em.hasEdgeTarget(folded) {
			t.Errorf("case-variant common name %s must emit no edge: %v", folded, em.edges)
		}
	}
	// Non-common names keep their written casing at the law's confidence.
	if e := em.edge(t, model.EdgeCalls, `C\m`, "FinalizeLater"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("FinalizeLater conf = %v", e.Confidence)
	}
}
