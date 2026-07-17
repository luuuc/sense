package php

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

const providerSrc = `<?php
namespace App\Providers;
use App\Contracts\Gateway;
use App\Payments\StripeGateway;
use App\Mail\Mailer;
use App\Mail\SmtpMailer;
class AppServiceProvider extends ServiceProvider {
    public function register(): void {
        $this->app->bind(Gateway::class, StripeGateway::class);
        $this->app->bind(Gateway::class, StripeGateway::class);
        $this->app->singleton('cache.store', \App\Cache\RedisStore::class);
        App::bind(Mailer::class, SmtpMailer::class);
    }
}
`

func TestContainerBindingEmitsSyntheticChain(t *testing.T) {
	em := mustRun(t, providerSrc)

	bindings := map[string]string{
		extract.PrefixLaravelBinding + `App\Contracts\Gateway`: `App\Payments\StripeGateway`,
		extract.PrefixLaravelBinding + "cache.store":           `App\Cache\RedisStore`,
		extract.PrefixLaravelBinding + `App\Mail\Mailer`:       `App\Mail\SmtpMailer`,
	}
	for synthetic, concrete := range bindings {
		if s := em.symbol(t, synthetic); s.Kind != model.KindConstant {
			t.Errorf("binding symbol kind = %q", s.Kind)
		}
		e := em.edge(t, model.EdgeCalls, synthetic, concrete)
		if e.Confidence != extract.ConfidenceConvention {
			t.Errorf("%s -> %s conf = %v", synthetic, concrete, e.Confidence)
		}
	}
	// The duplicate registration emits ONE symbol (deduped) and two edges.
	symbols := 0
	for _, s := range em.symbols {
		if s.Qualified == extract.PrefixLaravelBinding+`App\Contracts\Gateway` {
			symbols++
		}
	}
	if symbols != 1 {
		t.Errorf("duplicate bind emitted %d synthetic symbols, want 1", symbols)
	}
	// The handled registration calls emit no `bind`/`singleton` noise.
	for _, target := range []string{"bind", "singleton"} {
		if em.hasEdgeTarget(target) {
			t.Errorf("handled registration leaked a %q edge: %v", target, em.edges)
		}
	}
}

func TestClosureAndComputedBindingsFallThrough(t *testing.T) {
	em := mustRun(t, `<?php
class P {
    public function register(): void {
        $this->app->bind(Gateway::class, fn () => new StripeGateway());
        $this->app->singleton($key, Concrete::class);
    }
}
`)
	for _, s := range em.symbols {
		if s.Kind == model.KindConstant {
			t.Errorf("non-literal binding emitted synthetic %+v", s)
		}
	}
	// Unhandled, the call falls to the bare-name law: `bind` is not a
	// common name, so it survives at ConfidenceNameCollision only.
	if e := em.edge(t, model.EdgeCalls, `P\register`, "bind"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("fallthrough bind conf = %v", e.Confidence)
	}
}

func TestContainerConsumptionAndTyping(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Contracts\Gateway;
class Checkout {
    public function run(): void {
        $g = app(Gateway::class);
        $g->charge(1);
        app(Gateway::class)->refund(2);
        resolve('cache.store')->flush();
        $m = $this->app->make(Gateway::class);
    }
}
`)
	binding := extract.PrefixLaravelBinding + `App\Contracts\Gateway`
	consumption := 0
	for _, e := range em.edges {
		if e.TargetQualified == binding && e.Confidence == extract.ConfidenceConvention {
			consumption++
		}
	}
	if consumption != 3 {
		t.Errorf("consumption edges to %s = %d, want 3 (assign, chain, make)", binding, consumption)
	}
	// The container-made value is a type witness for method calls.
	for _, target := range []string{`App\Contracts\Gateway\charge`, `App\Contracts\Gateway\refund`} {
		if e := em.edge(t, model.EdgeCalls, `App\Checkout\run`, target); e.Confidence != extract.ConfidenceDynamic {
			t.Errorf("%s conf = %v", target, e.Confidence)
		}
	}
	// A string key consumes the binding but types nothing: `flush` falls to
	// the law.
	em.edge(t, model.EdgeCalls, `App\Checkout\run`, extract.PrefixLaravelBinding+"cache.store")
	if e := em.edge(t, model.EdgeCalls, `App\Checkout\run`, "flush"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("string-key chain conf = %v", e.Confidence)
	}
}

func TestBareAppCallStaysAFunctionCall(t *testing.T) {
	em := mustRun(t, `<?php
function f(): void { app()->boot2(); resolve(); }
`)
	em.edge(t, model.EdgeCalls, "f", "app")
	em.edge(t, model.EdgeCalls, "f", "resolve")
	for _, e := range em.edges {
		if e.TargetQualified == extract.PrefixLaravelBinding {
			t.Errorf("bare app() emitted an empty binding edge: %+v", e)
		}
	}
}

func TestFacadeAccessorEmitsProxyInherits(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Facades;
use Illuminate\Support\Facades\Facade;
use App\Services\PaymentService;
class Payments extends Facade {
    protected static function getFacadeAccessor(): string {
        return PaymentService::class;
    }
}
`)
	// The extends edge stays, and the proxy-IS-A accessor edge joins it.
	em.edge(t, model.EdgeInherits, `App\Facades\Payments`, `Illuminate\Support\Facades\Facade`)
	e := em.edge(t, model.EdgeInherits, `App\Facades\Payments`, `App\Services\PaymentService`)
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("accessor inherits conf = %v", e.Confidence)
	}
}

func TestStringAccessorFacadeEmitsNoAccessorEdge(t *testing.T) {
	em := mustRun(t, `<?php
use Illuminate\Support\Facades\Facade;
class CacheFacade extends Facade {
    protected static function getFacadeAccessor(): string { return 'cache'; }
}
`)
	inherits := 0
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			inherits++
		}
	}
	if inherits != 1 {
		t.Errorf("string accessor must add no edge beyond extends, got %d inherits", inherits)
	}
}

func TestNonFacadeAccessorMethodIgnored(t *testing.T) {
	em := mustRun(t, `<?php
class Weird {
    protected static function getFacadeAccessor(): string { return Thing::class; }
}
`)
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			t.Errorf("non-facade emitted inherits %+v", e)
		}
	}
}

func TestLaravelEmitterErrorsPropagate(t *testing.T) {
	// Symbol channel: symbol 1 = namespace, 2 = class, 3 = register method,
	// 4 = first binding synthetic. Edge channel: 1-4 = imports, 5 = extends
	// inherits, 6 = first binding edge.
	cases := map[string]*rec{
		"binding symbol": {failSymbolAt: 4},
		"binding edge":   {failEdgeAt: 6},
	}
	for name, em := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(t, providerSrc, em); err == nil {
				t.Error("want injected error, got nil")
			}
		})
	}
	t.Run("facade accessor edge", func(t *testing.T) {
		em := &rec{failEdgeAt: 3} // 1-2 imports, 3 = extends... accessor follows
		err := run(t, `<?php
use Illuminate\Support\Facades\Facade;
use App\Services\PaymentService;
class Payments extends Facade {
    protected static function getFacadeAccessor(): string { return PaymentService::class; }
}
`, em)
		if err == nil {
			t.Error("want injected error, got nil")
		}
	})
}

// TestLaravelHelperGuards drives the literal-only guards: computed
// arguments, absent bodies, and non-container function calls.
func TestLaravelHelperGuards(t *testing.T) {
	em := mustRun(t, `<?php
use Illuminate\Support\Facades\Facade;
class C {
    public function m(): void {
        $this->app->bind(static::class, Concrete::class);
        $x = helperFn();
        $x->pokeIt();
    }
}
class Empty2 extends Facade {
    public const NAME = 'x';
    public static function other(): void {}
}
`)
	for _, s := range em.symbols {
		if s.Kind == model.KindConstant {
			t.Errorf("static::class binding emitted synthetic %+v", s)
		}
	}
	// helperFn() is not a container lookup: no witness, pokeIt rides the law.
	if e := em.edge(t, model.EdgeCalls, `C\m`, "pokeIt"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("non-container assignment typed the receiver: %v", e.Confidence)
	}
	// A facade with no getFacadeAccessor emits only its extends edge.
	inherits := 0
	for _, e := range em.edges {
		if e.Kind == model.EdgeInherits {
			inherits++
		}
	}
	if inherits != 1 {
		t.Errorf("accessor-less facade inherits = %d, want 1", inherits)
	}
}

func TestLaravelDegenerateNodes(t *testing.T) {
	w := &walker{
		source:     []byte("<?php $x = 1;"),
		emit:       &rec{},
		uses:       map[string]string{},
		propTypes:  map[string]map[string]string{},
		parents:    map[string]string{"Q": "Facade"},
		synthetics: map[string]bool{},
	}
	root := parse(t, "<?php $x = 1;").RootNode()
	if got := w.classConstant(nil); got != "" {
		t.Errorf("classConstant(nil) = %q", got)
	}
	if got := w.classConstant(root); got != "" {
		t.Errorf("classConstant(program) = %q", got)
	}
	if got := argExpr(root, 0); got != nil {
		t.Errorf("argExpr(program) = %v", got)
	}
	if got := w.containerMadeType(nil); got != "" {
		t.Errorf("containerMadeType(nil) = %q", got)
	}
	if got := w.containerMadeType(root); got != "" {
		t.Errorf("containerMadeType(program) = %q", got)
	}
	if got := w.facadeAccessor(root, "Q"); got != "" {
		t.Errorf("facadeAccessor(bodyless) = %q", got)
	}
}
