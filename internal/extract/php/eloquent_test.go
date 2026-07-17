package php

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func TestEloquentRelationsCompose(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Models;
use App\Models\Payments\Refund;
class Order extends Model {
    public function items() { return $this->hasMany(OrderItem::class); }
    public function customer() { return $this->belongsTo(Customer::class, 'customer_id'); }
    public function refunds() { return $this->hasManyThrough(Refund::class, Payment::class); }
    public function taggable() { return $this->morphTo(); }
    public function other() { return $other->hasMany(NotMine::class); }
}
`)
	cases := []string{
		`App\Models\OrderItem`,
		`App\Models\Customer`,
		`App\Models\Payments\Refund`,
	}
	for _, related := range cases {
		e := em.edge(t, model.EdgeComposes, `App\Models\Order`, related)
		if e.Confidence != extract.ConfidenceConvention {
			t.Errorf("composes %s conf = %v", related, e.Confidence)
		}
	}
	// morphTo names no class; a non-$this receiver is not a declaration.
	for _, e := range em.edges {
		if e.Kind == model.EdgeComposes && e.TargetQualified == `App\Models\NotMine` {
			t.Errorf("non-$this relation composed: %+v", e)
		}
	}
	// The consumed verb calls leak no hasMany/belongsTo noise.
	if em.hasEdgeTarget(`App\Models\Order\hasMany`) {
		t.Errorf("relation verb leaked a call edge: %v", em.edges)
	}
}

func TestScopeAliasSymbolAndEdge(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Models;
class Order {
    public function scopeActive($q) { return $q->whereNotNull('shipped_at'); }
    public function scopePendingReview($q) {}
    public function scoped() {}
    public function scope() {}
}
`)
	active := em.symbol(t, `App\Models\Order\active`)
	if active.Kind != model.KindMethod || active.Visibility != "public" || active.Receiver != extract.ReceiverInstance {
		t.Errorf("alias symbol = %+v", active)
	}
	em.symbol(t, `App\Models\Order\pendingReview`)
	e := em.edge(t, model.EdgeCalls, `App\Models\Order\active`, `App\Models\Order\scopeActive`)
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("alias edge conf = %v", e.Confidence)
	}
	// `scoped` (lower-case continuation) and bare `scope` declare nothing.
	for _, s := range em.symbols {
		if s.Qualified == `App\Models\Order\d` || s.Qualified == `App\Models\Order\` {
			t.Errorf("false scope alias %+v", s)
		}
	}
}

func TestObserversBothForms(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use App\Observers\OrderObserver;
use App\Observers\AuditObserver;
#[ObservedBy([OrderObserver::class, AuditObserver::class])]
class Order {}
#[ObservedBy(SoloObserver::class)]
class Invoice {}
class Boot {
    public function register(): void {
        Order::observe(OrderObserver::class);
        Order::observe($computed);
    }
}
`)
	em.edge(t, model.EdgeCalls, `App\Order`, `App\Observers\OrderObserver`)
	em.edge(t, model.EdgeCalls, `App\Order`, `App\Observers\AuditObserver`)
	em.edge(t, model.EdgeCalls, `App\Invoice`, `App\SoloObserver`)
	e := em.edge(t, model.EdgeCalls, `App\Boot\register`, `App\Observers\OrderObserver`)
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("observe() edge conf = %v", e.Confidence)
	}
}

func TestScopeAliasHelper(t *testing.T) {
	cases := map[string]string{
		"scopeActive":        "active",
		"scopePendingReview": "pendingReview",
		"scope":              "",
		"scoped":             "",
		"scopeX":             "x",
		"notAScope":          "",
	}
	for in, want := range cases {
		if got := scopeAlias(in); got != want {
			t.Errorf("scopeAlias(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEloquentEmitterErrorsPropagate(t *testing.T) {
	relSrc := `<?php
class Order {
    public function items() { return $this->hasMany(OrderItem::class); }
}
`
	if err := run(t, relSrc, &rec{failEdgeAt: 1}); err == nil {
		t.Error("want relation edge error")
	}
	scopeSrc := `<?php
class Order { public function scopeActive($q) {} }
`
	// Symbols: 1 = class, 2 = scopeActive method, 3 = alias.
	if err := run(t, scopeSrc, &rec{failSymbolAt: 3}); err == nil {
		t.Error("want alias symbol error")
	}
	if err := run(t, scopeSrc, &rec{failEdgeAt: 1}); err == nil {
		t.Error("want alias edge error")
	}
	obsSrc := `<?php
#[ObservedBy(X::class)]
class Order {}
`
	if err := run(t, obsSrc, &rec{failEdgeAt: 1}); err == nil {
		t.Error("want ObservedBy edge error")
	}
}

func TestObservedByDegenerate(t *testing.T) {
	// Attribute-less class and a non-ObservedBy attribute emit nothing.
	em := mustRun(t, `<?php
#[Route("/x")]
class Plain {}
class Bare {}
`)
	for _, e := range em.edges {
		if e.Kind == model.EdgeCalls {
			t.Errorf("unexpected edge %+v", e)
		}
	}
}

func TestScopeAliasSuppressedByDeclaredMethod(t *testing.T) {
	// Eloquent's __call never fires when the class defines the real
	// method: no alias symbol, no alias edge.
	em := mustRun(t, `<?php
class Order {
    public function scopeActive($q) {}
    public function active() {}
    public function scopeShipped($q) {}
}
`)
	aliases := 0
	for _, s := range em.symbols {
		if s.Qualified == `Order\active` {
			aliases++
		}
	}
	if aliases != 1 {
		t.Errorf("Order\\active symbols = %d, want 1 (the declared method only)", aliases)
	}
	for _, e := range em.edges {
		if e.SourceQualified == `Order\active` {
			t.Errorf("suppressed alias emitted an edge: %+v", e)
		}
	}
	em.symbol(t, `Order\shipped`) // uncontested scope still aliases
}

// A concord repo expresses relations as `<X>Proxy::modelClass()`; the
// composes edge lands on the model class the proxy stands for. A
// non-proxy scope or a different static method resolves to nothing.
func TestEloquentRelationsThroughConcordProxy(t *testing.T) {
	em := mustRun(t, `<?php
namespace Webkul\Attribute\Models;
use Webkul\Product\Models\ProductProxy;
class AttributeFamily extends Model {
    public function products() { return $this->hasMany(ProductProxy::modelClass()); }
    public function local() { return $this->belongsTo(GroupProxy::modelClass()); }
    public function other() { return $this->hasMany(Helper::tableName()); }
    public function plain() { return $this->belongsTo(Proxy::modelClass()); }
    public function bare() { return $this->hasMany('products'); }
    public function relative() { return $this->belongsTo(static::modelClass()); }
    public function notProxy() { return $this->hasMany(Helper::modelClass()); }
}
`)
	e := em.edge(t, model.EdgeComposes, `Webkul\Attribute\Models\AttributeFamily`, `Webkul\Product\Models\Product`)
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("proxy relation conf = %v", e.Confidence)
	}
	em.edge(t, model.EdgeComposes, `Webkul\Attribute\Models\AttributeFamily`, `Webkul\Attribute\Models\Group`)
	for _, edge := range em.edges {
		if edge.Kind == model.EdgeComposes &&
			(edge.TargetQualified == `Webkul\Attribute\Models\Helper` ||
				edge.TargetQualified == `Webkul\Attribute\Models\` ||
				edge.TargetQualified == "") {
			t.Errorf("non-proxy static arg composed: %+v", edge)
		}
	}
}

// An aliased proxy import (`use ...ProductProxy as ProductModel`) still
// resolves the relation onto the model: the Proxy suffix is a property of
// the RESOLVED class, not of the written alias. A fully-qualified spelling
// (`\Webkul\...\GroupProxy::modelClass()`) rides the qualified_name scope
// branch to the same answer.
func TestEloquentRelationsThroughAliasedAndQualifiedProxy(t *testing.T) {
	em := mustRun(t, `<?php
namespace Webkul\Attribute\Models;
use Webkul\Product\Models\ProductProxy as ProductModel;
class Family extends Model {
    public function products() { return $this->hasMany(ProductModel::modelClass()); }
    public function groups() { return $this->belongsTo(\Webkul\Product\Models\GroupProxy::modelClass()); }
}
`)
	em.edge(t, model.EdgeComposes, `Webkul\Attribute\Models\Family`, `Webkul\Product\Models\Product`)
	em.edge(t, model.EdgeComposes, `Webkul\Attribute\Models\Family`, `Webkul\Product\Models\Group`)
}
