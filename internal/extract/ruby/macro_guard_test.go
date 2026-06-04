package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// A macro written in block form (`macro do … end`) parses as a `call` node
// with a `do_block` but no `arguments` field. The class-body and top-level
// DSL handlers must treat the missing argument list as a no-op rather than
// crash, emitting no symbol or edge for the macro.

func TestIncludeBlockFormNoArgsNoEdge(t *testing.T) {
	r := parseRuby(t, "class C\n  include do\n  end\nend")
	for _, e := range r.edges {
		if string(e.Kind) == "includes" {
			t.Errorf("include with no argument list must emit no includes edge, got %v", e.TargetQualified)
		}
	}
}

func TestAssociationBlockFormNoArgsNoEdge(t *testing.T) {
	r := parseRuby(t, "class Order\n  has_many do\n  end\nend")
	for _, e := range r.edges {
		if string(e.Kind) == "composes" {
			t.Errorf("has_many with no argument list must emit no composes edge, got %v", e.TargetQualified)
		}
	}
}

func TestAssociationNonSymbolFirstArgNoEdge(t *testing.T) {
	// `has_many "string"` — the first argument is a string, not a simple
	// symbol, so no association name can be derived and no edge is emitted.
	r := parseRuby(t, `class Order
  has_many "line_items"
end
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" {
			t.Errorf("has_many with a non-symbol first arg must emit no composes edge, got %v", e.TargetQualified)
		}
	}
}

func TestBroadcastBlockFormNoArgsNoEdge(t *testing.T) {
	r := parseRuby(t, "class Order\n  broadcasts_to do\n  end\nend")
	for _, e := range r.edges {
		if e.SourceQualified == "Order" && string(e.Kind) == "calls" {
			t.Errorf("broadcasts_to with no argument list must emit no edge, got %v", e.TargetQualified)
		}
	}
}

func TestCallbackBlockFormNoArgsNoSymbol(t *testing.T) {
	// `before_save do … end` has a block but no symbol arguments — emit no
	// callback symbol and no calls edge.
	r := parseRuby(t, "class Order\n  before_save do\n    recompute\n  end\nend")
	if findSymbol(r, "Order.before_save") != nil {
		t.Error("block-form callback must not emit a callback declaration symbol")
	}
}

func TestScopeBlockFormNoArgsNoSymbol(t *testing.T) {
	r := parseRuby(t, "class Product\n  scope do\n  end\nend")
	for _, s := range r.symbols {
		if s.ParentQualified == "Product" && s.Kind == model.KindMethod {
			t.Errorf("block-form scope must not emit a scope method symbol, got %v", s.Qualified)
		}
	}
}

func TestResourcesBlockFormNoArgsNoEdge(t *testing.T) {
	r := parseRuby(t, "resources do\nend")
	for _, e := range r.edges {
		if e.SourceQualified == "routes" {
			t.Errorf("resources with no argument list must emit no route edge, got %v", e.TargetQualified)
		}
	}
}

func TestResourcesNonSymbolFirstArgNoEdge(t *testing.T) {
	// `resources "orders"` — string first arg, not a simple symbol.
	r := parseRuby(t, `resources "orders"`)
	for _, e := range r.edges {
		if e.SourceQualified == "routes" {
			t.Errorf("resources with a non-symbol first arg must emit no route edge, got %v", e.TargetQualified)
		}
	}
}

func TestVerbRouteBlockFormNoArgsNoEdge(t *testing.T) {
	r := parseRuby(t, "get do\nend")
	for _, e := range r.edges {
		if e.SourceQualified == "routes" {
			t.Errorf("verb route with no argument list must emit no edge, got %v", e.TargetQualified)
		}
	}
}

func TestNamespaceBlockFormNoArgsNoCrash(t *testing.T) {
	// `namespace do … end` has a block but no symbol argument, so the
	// namespace name cannot be derived; the inner resource is walked under
	// no namespace prefix rather than crashing.
	r := parseRuby(t, "namespace do\nend")
	_ = r // no panic is the assertion
}

func TestNamespacedVerbRouteWithAsHelper(t *testing.T) {
	// A namespaced verb route with `as:` exercises the namespace-prefixed
	// route-helper branch of handleVerbRoute.
	r := parseRuby(t, "namespace :admin do\n  get \"reports\", to: \"reports#index\", as: :summary\nend")
	if findEdge(r, extract.PrefixRoute+"admin_summary_path", "Admin::ReportsController#index", string(model.EdgeCalls)) == nil {
		t.Errorf("missing admin_summary_path helper edge, got symbols %v", r.symbols)
	}
	if findSymbol(r, extract.PrefixRoute+"admin_summary_url") == nil {
		t.Error("missing admin_summary_url helper twin")
	}
}

// --- emit-error propagation for the macro handlers ---

func TestScopeSymbolEmitError(t *testing.T) {
	// class Product symbol emits first (allowed), then the scope method symbol
	// fails.
	err := parseWithEmitter(t, "class Product\n  scope :active, -> { x }\nend",
		&failAfterN{symbolsLeft: 1, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing symbol emit on a scope declaration")
	}
}

func TestCallbackEdgeEmitError(t *testing.T) {
	// Order class + Order.before_save symbol emit fine; the calls edge fails.
	err := parseWithEmitter(t, "class Order\n  before_save :validate_total\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing edge emit on a callback")
	}
}

func TestResourcesControllerEdgeEmitError(t *testing.T) {
	// Fail the very first RESTful controller edge (no symbols precede it).
	err := parseWithEmitter(t, `resources :orders`,
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing controller edge emit on resources")
	}
}

func TestVerbRouteEdgeEmitError(t *testing.T) {
	err := parseWithEmitter(t, `get "/home", to: "pages#home"`,
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing edge emit on a verb route")
	}
}

func TestIncludeEdgeEmitError(t *testing.T) {
	err := parseWithEmitter(t, "class Order\n  include Printable\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing edge emit on an include")
	}
}

func TestAssociationEdgeEmitError(t *testing.T) {
	err := parseWithEmitter(t, "class Order\n  has_many :line_items\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing edge emit on an association")
	}
}

func TestImportmapPinBlockFormNoArgsNoEdge(t *testing.T) {
	// `pin do … end` in importmap.rb is a call with a block but no argument
	// list — emit no imports edge.
	r := parseRubyWithPath(t, "pin do\nend", "config/importmap.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "imports" {
			t.Errorf("pin with no argument list must emit no imports edge, got %v", e.TargetQualified)
		}
	}
}

func TestVerbRouteAsHelperEdgeEmitError(t *testing.T) {
	// `get … as: :reports` emits the controller edge, then the route-helper
	// symbol and its edge. Allow the controller edge, then fail the helper edge.
	err := parseWithEmitter(t, `get "reports", to: "reports#index", as: :reports`,
		&failAfterN{symbolsLeft: 100, edgesLeft: 1})
	if err == nil {
		t.Error("expected error from failing route-helper edge emit on a verb route with as:")
	}
}
