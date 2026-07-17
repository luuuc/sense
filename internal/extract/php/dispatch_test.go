package php

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func TestCallableArrayAndStringArguments(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Http\OrderController;
class Wiring {
    public function map(OrderController $typed): void {
        dispatch_to([OrderController::class, 'index']);
        dispatch_to([$this, 'ownHandler']);
        dispatch_to([$typed, 'show']);
        dispatch_to('OrderController@store');
        dispatch_to('not a callable @ all');
        dispatch_to([$unknown, 'mystery']);
        dispatch_to([OrderController::class, 'index', 'extra']);
    }
    public function ownHandler(): void {}
}
`)
	for _, target := range []string{
		`App\Http\OrderController\index`,
		`App\Wiring\ownHandler`,
		`App\Http\OrderController\show`,
		`App\Http\OrderController\store`,
	} {
		e := em.edge(t, model.EdgeCalls, `App\Wiring\map`, target)
		if e.Confidence != extract.ConfidenceConvention {
			t.Errorf("%s conf = %v", target, e.Confidence)
		}
	}
	for _, absent := range []string{"mystery", "extra"} {
		for _, e := range em.edges {
			if e.TargetQualified == absent || e.TargetQualified == `App\Http\OrderController\extra` {
				t.Errorf("non-callable emitted an edge: %+v", e)
			}
			_ = absent
		}
	}
}

func TestRouteRegistrations(t *testing.T) {
	em := mustRun(t, `<?php
use App\Http\OrderController;
use App\Http\PingController;
use App\Http\PhotoController;
Route::get('/orders', [OrderController::class, 'index']);
Route::post('/orders', 'OrderController@store');
Route::get('/ping', PingController::class);
Route::resource('photos', PhotoController::class);
Route::fallback(NotFoundController::class);
Route::get('/plain', '/no-handler-here');
`)
	cases := map[string]string{
		`App\Http\OrderController\index`:   "callable array handler",
		`App\Http\OrderController\store`:   "at-string handler (alias-resolved)",
		`App\Http\PingController\__invoke`: "invokable controller",
		`App\Http\PhotoController`:         "resource controller",
		`NotFoundController\__invoke`:      "fallback invokable",
	}
	for target, why := range cases {
		if e := em.edge(t, model.EdgeCalls, "", target); e.Confidence != extract.ConfidenceConvention {
			t.Errorf("%s (%s) conf = %v", target, why, e.Confidence)
		}
	}
}

func TestListenMapAndDispatchChain(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Providers;
use App\Events\OrderShipped;
use App\Listeners\SendShipmentNotification;
use App\Listeners\LogShipment;
class EventServiceProvider {
    protected $listen = [
        OrderShipped::class => [SendShipmentNotification::class, LogShipment::class],
        'computed' . $x => [Nope::class],
    ];
}
class ShipOrder {
    public function ship(): void {
        OrderShipped::dispatch($this);
        event(new OrderShipped($this));
    }
}
`)
	listen := extract.PrefixLaravelListen + `App\Events\OrderShipped`
	if s := em.symbol(t, listen); s.Kind != model.KindConstant {
		t.Errorf("listen symbol kind = %q", s.Kind)
	}
	em.edge(t, model.EdgeCalls, listen, `App\Listeners\SendShipmentNotification\handle`)
	em.edge(t, model.EdgeCalls, listen, `App\Listeners\LogShipment\handle`)

	// Both dispatch forms consume the same synthetic.
	consumers := 0
	for _, e := range em.edges {
		if e.SourceQualified == `App\Providers\ShipOrder\ship` && e.TargetQualified == listen {
			consumers++
		}
	}
	if consumers != 2 {
		t.Errorf("dispatch consumption edges = %d, want 2 (::dispatch + event())", consumers)
	}
	// The computed key emits nothing.
	for _, s := range em.symbols {
		if s.Qualified == extract.PrefixLaravelListen+"computed" {
			t.Errorf("computed listen key emitted %+v", s)
		}
	}
}

func TestMiddlewareAliasesAndUse(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Http;
use App\Http\Middleware\Authenticate;
use App\Http\Middleware\EnsureVerified;
class Kernel {
    protected $middlewareAliases = [
        'auth' => Authenticate::class,
        'verified' => EnsureVerified::class,
    ];
}
class RouteFile {
    public function wire($route): void {
        $route->middleware('auth');
        $route->middleware(['auth', 'verified']);
        $route->middleware($computed);
    }
}
`)
	auth := extract.PrefixLaravelMiddleware + "auth"
	em.symbol(t, auth)
	em.edge(t, model.EdgeCalls, auth, `App\Http\Middleware\Authenticate\handle`)
	em.edge(t, model.EdgeCalls, extract.PrefixLaravelMiddleware+"verified", `App\Http\Middleware\EnsureVerified\handle`)

	uses := 0
	for _, e := range em.edges {
		if e.SourceQualified == `App\Http\RouteFile\wire` && e.TargetQualified == auth {
			uses++
		}
	}
	if uses != 2 {
		t.Errorf("middleware('auth') consumption edges = %d, want 2", uses)
	}
	// The computed alias falls through to the bare-name law instead.
	if e := em.edge(t, model.EdgeCalls, `App\Http\RouteFile\wire`, "middleware"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("computed middleware() conf = %v", e.Confidence)
	}
}

func TestOrdinaryPropertiesAndStringsStayQuiet(t *testing.T) {
	em := mustRun(t, `<?php
class Plain {
    protected $listen = 'not-a-map';
    protected $other = [OrderShipped::class => [X::class]];
    public function m(): void {
        log_line('user@example.com');
        tag(['a', 'b']);
    }
}
`)
	for _, s := range em.symbols {
		if s.Kind == model.KindConstant {
			t.Errorf("unexpected synthetic %+v", s)
		}
	}
	for _, e := range em.edges {
		if e.TargetQualified == `user\example.com` || e.TargetQualified == "example.com" {
			t.Errorf("email string became a callable edge: %+v", e)
		}
	}
}

func TestDispatchEmitterErrorsPropagate(t *testing.T) {
	listenSrc := `<?php
use App\Events\OrderShipped;
use App\Listeners\LogShipment;
class P { protected $listen = [OrderShipped::class => [LogShipment::class]]; }
`
	// Edge channel: 1-2 imports, 3 = listen edge. Symbol: 1 = class P,
	// 2 = listen synthetic.
	if err := run(t, listenSrc, &rec{failSymbolAt: 2}); err == nil {
		t.Error("want listen symbol error")
	}
	if err := run(t, listenSrc, &rec{failEdgeAt: 3}); err == nil {
		t.Error("want listen edge error")
	}
	if err := run(t, `<?php Route::get('/x', [C::class, 'm']);`, &rec{failEdgeAt: 1}); err == nil {
		t.Error("want callable-arg edge error")
	}
	if err := run(t, `<?php class K { public function w($r): void { $r->middleware(['a']); } }`, &rec{failEdgeAt: 1}); err == nil {
		t.Error("want middleware consumption edge error")
	}
}

// TestDispatchGuards drives the literal-only refusals across the dispatch
// surface: computed keys, interpolated strings, malformed callables,
// non-map shapes.
func TestDispatchGuards(t *testing.T) {
	em := mustRun(t, `<?php
namespace G;
Route::view('/v', 'welcome');
Route::resource('a', 'b');
class K {
    protected $listen = [
        \G\Ev::class => \G\Solo::class,
        \G\Ev2::class => 'string-listener',
    ];
    protected $middlewareAliases = ['solo', 'x' => $var, 'y' => \G\H::class];
    public function m($r): void {
        wire(['k' => 1, 'v' => 2]);
        wire("interp {$x}@y");
        wire('Bad-Class@m');
        $r->middleware([$x]);
    }
}
`)
	// A scalar $listen value still wires: Ev -> Solo\handle.
	em.edge(t, model.EdgeCalls, extract.PrefixLaravelListen+`G\Ev`, `G\Solo\handle`)
	// A string listener wires nothing.
	for _, e := range em.edges {
		if e.SourceQualified == extract.PrefixLaravelListen+`G\Ev2` {
			t.Errorf("string listener emitted %+v", e)
		}
	}
	em.edge(t, model.EdgeCalls, extract.PrefixLaravelMiddleware+"y", `G\H\handle`)
	for _, bad := range []string{
		extract.PrefixLaravelMiddleware + "solo",
		extract.PrefixLaravelMiddleware + "x",
	} {
		for _, s := range em.symbols {
			if s.Qualified == bad {
				t.Errorf("non-pair alias emitted %+v", s)
			}
		}
	}
	// Computed middleware arg falls to the law; malformed callables quiet.
	if e := em.edge(t, model.EdgeCalls, `G\K\m`, "middleware"); e.Confidence != extract.ConfidenceNameCollision {
		t.Errorf("computed middleware conf = %v", e.Confidence)
	}
	for _, e := range em.edges {
		if e.TargetQualified == `G\Bad-Class\m` || e.TargetQualified == "welcome" {
			t.Errorf("malformed dispatch emitted %+v", e)
		}
	}
}

func TestDispatchDegenerateNodes(t *testing.T) {
	w := &walker{
		source:     []byte("<?php $x = 1;"),
		emit:       &rec{},
		uses:       map[string]string{},
		propTypes:  map[string]map[string]string{},
		parents:    map[string]string{},
		synthetics: map[string]bool{},
	}
	root := parse(t, "<?php $x = 1;").RootNode()
	if err := w.emitPropertyDispatch(root, ""); err != nil {
		t.Errorf("emitPropertyDispatch(program): %v", err)
	}
	if got := w.stringLiteral(root); got != "" {
		t.Errorf("stringLiteral(program) = %q", got)
	}
	for _, bad := range []string{"", "9x", "has space"} {
		if isMethodIdent(bad) {
			t.Errorf("isMethodIdent(%q) = true", bad)
		}
	}
	if got := w.callableFromString("@m"); got != "" {
		t.Errorf("callableFromString(@m) = %q", got)
	}
	if got := w.listenerClasses(nil); got != nil {
		t.Errorf("listenerClasses(nil) = %v", got)
	}
}

func TestEmailShapedStringsRefused(t *testing.T) {
	em := mustRun(t, `<?php
class C {
    public function m(): void {
        notify('user@host');
        wire('OrderController@store');
    }
}
`)
	for _, e := range em.edges {
		if e.TargetQualified == `user\host` || e.TargetQualified == "host" {
			t.Errorf("lowercase class segment emitted an edge: %+v", e)
		}
	}
	em.edge(t, model.EdgeCalls, `C\m`, `OrderController\store`)
}

func TestJobDispatchEdgesHandle(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Jobs\ProcessPodcast;
class Publisher {
    public function publish(): void {
        ProcessPodcast::dispatch($this);
    }
}
`)
	// Both dispatch families' edges emit; the index keeps whichever
	// target exists.
	em.edge(t, model.EdgeCalls, `App\Publisher\publish`, extract.PrefixLaravelListen+`App\Jobs\ProcessPodcast`)
	e := em.edge(t, model.EdgeCalls, `App\Publisher\publish`, `App\Jobs\ProcessPodcast\handle`)
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("job handle edge conf = %v", e.Confidence)
	}
}
