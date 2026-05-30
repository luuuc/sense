package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func hasRouteSymbol(r *recorder, name string) *extract.EmittedSymbol {
	return findSymbol(r, extract.PrefixRoute+name)
}

func hasRouteEdge(r *recorder, helper, action string) *extract.EmittedEdge {
	return findEdge(r, extract.PrefixRoute+helper, action, string(model.EdgeCalls))
}

func TestRouteHelpers_PluralResources(t *testing.T) {
	r := parseRuby(t, `resources :orders`)

	// Every standard helper, both _path and _url, with the right action edge.
	want := map[string]string{
		"orders_path":     "OrdersController#index",
		"order_path":      "OrdersController#show",
		"new_order_path":  "OrdersController#new",
		"edit_order_path": "OrdersController#edit",
	}
	for helper, action := range want {
		if hasRouteSymbol(r, helper) == nil {
			t.Errorf("missing route symbol %s", helper)
		}
		if hasRouteEdge(r, helper, action) == nil {
			t.Errorf("missing edge %s → %s", helper, action)
		}
		// _url twin exists and routes to the same action.
		url := helper[:len(helper)-len("_path")] + "_url"
		if hasRouteSymbol(r, url) == nil {
			t.Errorf("missing _url twin %s", url)
		}
		if hasRouteEdge(r, url, action) == nil {
			t.Errorf("missing edge %s → %s", url, action)
		}
	}
}

func TestRouteHelpers_SingularResourceNoCollection(t *testing.T) {
	r := parseRuby(t, `resource :profile`)

	// Singular member + new/edit, controller is pluralized (ProfilesController).
	if hasRouteEdge(r, "profile_path", "ProfilesController#show") == nil {
		t.Error("missing profile_path → ProfilesController#show")
	}
	if hasRouteSymbol(r, "new_profile_path") == nil || hasRouteSymbol(r, "edit_profile_path") == nil {
		t.Error("missing new_/edit_ profile helpers")
	}
	// No plural collection helper for a singular resource.
	if hasRouteSymbol(r, "profiles_path") != nil {
		t.Error("singular resource must not emit a plural collection helper")
	}
}

func TestRouteHelpers_Namespaced(t *testing.T) {
	r := parseRuby(t, "namespace :admin do\n  resources :orders\nend")

	if hasRouteSymbol(r, "admin_orders_path") == nil {
		t.Error("missing admin_orders_path")
	}
	if hasRouteEdge(r, "admin_orders_path", "Admin::OrdersController#index") == nil {
		t.Error("missing admin_orders_path → Admin::OrdersController#index")
	}
	// new_/edit_ prefix the whole name: new_admin_order_path.
	if hasRouteEdge(r, "new_admin_order_path", "Admin::OrdersController#new") == nil {
		t.Error("missing new_admin_order_path → Admin::OrdersController#new")
	}
	// The un-namespaced form must not be emitted.
	if hasRouteSymbol(r, "orders_path") != nil {
		t.Error("namespaced resource must not also emit the bare orders_path")
	}
}

func TestRouteHelpers_NestedResourceEmitsNoHelper(t *testing.T) {
	r := parseRuby(t, "resources :orders do\n  resources :items\nend")

	// The parent resource still gets its helpers.
	if hasRouteSymbol(r, "orders_path") == nil {
		t.Error("parent orders_path should still be emitted")
	}
	// The nested resource's helper name (order_items_path) can't be derived
	// from the inner declaration — emit nothing rather than a wrong items_path.
	if hasRouteSymbol(r, "items_path") != nil {
		t.Error("nested resource must not emit a (wrong) items_path helper")
	}
	// But its controller edges are still tracked.
	if findEdge(r, "routes", "ItemsController#index", string(model.EdgeCalls)) == nil {
		t.Error("nested resource controller edge should still be emitted")
	}
}

func TestRouteHelpers_VerbRouteWithAs(t *testing.T) {
	r := parseRuby(t, `get "reports", to: "reports#index", as: :reports`)

	if hasRouteEdge(r, "reports_path", "ReportsController#index") == nil {
		t.Error("missing reports_path → ReportsController#index")
	}
	if hasRouteSymbol(r, "reports_url") == nil {
		t.Error("missing reports_url twin")
	}
}

func TestRouteHelpers_VerbRouteWithoutAsEmitsNoHelper(t *testing.T) {
	r := parseRuby(t, `get "health", to: "health#show"`)

	// The controller edge is emitted, but no route helper (un-nameable).
	if findEdge(r, "routes", "HealthController#show", string(model.EdgeCalls)) == nil {
		t.Error("verb route controller edge should be emitted")
	}
	for _, s := range r.symbols {
		if len(s.Qualified) >= len(extract.PrefixRoute) && s.Qualified[:len(extract.PrefixRoute)] == extract.PrefixRoute {
			t.Errorf("verb route without as: must emit no route helper, got %s", s.Qualified)
		}
	}
}

func TestRouteHelpers_SymbolDedupedPerFile(t *testing.T) {
	// Two identical declarations must not emit duplicate symbols (UNIQUE
	// (file_id, qualified) would otherwise conflict).
	r := parseRuby(t, "resources :orders\nresources :orders")
	var count int
	for _, s := range r.symbols {
		if s.Qualified == extract.PrefixRoute+"orders_path" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("route:orders_path symbol emitted %d times, want 1 (deduped)", count)
	}
}

func TestRouteHelpers_SymbolEmitError(t *testing.T) {
	// resources emits controller edges first (those succeed), then route-helper
	// symbols — fail the first symbol to hit emitRouteHelper's symbol path.
	err := parseWithEmitter(t, `resources :orders`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error from failing symbol emitter on a route helper")
	}
}

func TestRouteHelpers_EdgeEmitError(t *testing.T) {
	// Allow the 7 RESTful controller edges, then fail the first route-helper
	// edge so emitRouteHelper's edge path is exercised.
	err := parseWithEmitter(t, `resources :orders`, &failAfterN{symbolsLeft: 100, edgesLeft: 7})
	if err == nil {
		t.Error("expected error from failing edge emitter on a route helper")
	}
}

func TestRouteHelpers_AsString(t *testing.T) {
	// `as: "foo"` (string form, not a symbol) names the helper too.
	r := parseRuby(t, `get "reports", to: "reports#index", as: "summary"`)
	if hasRouteEdge(r, "summary_path", "ReportsController#index") == nil {
		t.Errorf("missing summary_path from string as:, got %v", r.symbols)
	}
}

func TestRouteHelpers_VerbRouteNoTargetNoEmit(t *testing.T) {
	// No `to:` → nothing to route to; emit nothing (covers the guard).
	r := parseRuby(t, `get "health"`)
	for _, s := range r.symbols {
		if len(s.Qualified) >= len(extract.PrefixRoute) && s.Qualified[:len(extract.PrefixRoute)] == extract.PrefixRoute {
			t.Errorf("verb route with no to: must emit no helper, got %s", s.Qualified)
		}
	}
}
