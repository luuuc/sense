package erb

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// link_to / button_to route helpers retarget the reserved route: symbol so the
// view → route-helper → controller chain connects.
func TestRouteRef_LinkToTargetsRouteSymbol(t *testing.T) {
	r := extractERB(t, `<%= link_to "Orders", orders_path %>`, "app/views/home/index.html.erb")
	if findEdgeKind(r, "route:orders_path", model.EdgeCalls) == nil {
		t.Errorf("expected edge to route:orders_path, got %v", erbTargets(r))
	}
	if findEdgeKind(r, "self.orders_path", model.EdgeCalls) != nil {
		t.Error("must not emit a bare self.orders_path edge")
	}
}

// The phantom guard: a *_path reference that happens to share a name with an
// application method targets the route: symbol, never the bare app-method name.
func TestRouteRef_PhantomGuard(t *testing.T) {
	r := extractERB(t, `<%= button_to "Verify", verifications_path %>`, "app/views/sessions/new.html.erb")
	if findEdgeKind(r, "route:verifications_path", model.EdgeCalls) == nil {
		t.Errorf("expected edge to route:verifications_path, got %v", erbTargets(r))
	}
	// Neither a bare self.verifications_path nor an unqualified verifications_path
	// (either of which could resolve to an unrelated app method).
	if findEdgeKind(r, "self.verifications_path", model.EdgeCalls) != nil ||
		findEdgeKind(r, "verifications_path", model.EdgeCalls) != nil {
		t.Errorf("verifications_path must only target the route: symbol; got %v", erbTargets(r))
	}
}

func TestRouteRef_UrlVariant(t *testing.T) {
	r := extractERB(t, `<%= redirect_to order_url(@order) %>`, "v.erb")
	if findEdgeKind(r, "route:order_url", model.EdgeCalls) == nil {
		t.Errorf("expected edge to route:order_url, got %v", erbTargets(r))
	}
}

// A method named *_path on an explicit constant receiver is a real method
// call, not a route helper — it must not be rewritten to route:.
func TestRouteRef_ConstantReceiverNotRewritten(t *testing.T) {
	r := extractERB(t, `<%= Assets.image_path %>`, "v.erb")
	if findEdgeKind(r, extract.PrefixRoute+"image_path", model.EdgeCalls) != nil {
		t.Errorf("a constant-receiver .image_path must not become a route helper; got %v", erbTargets(r))
	}
}

func TestIsRouteHelperName(t *testing.T) {
	yes := []string{"orders_path", "edit_admin_order_url", "x_path", "a1_url"}
	for _, s := range yes {
		if !isRouteHelperName(s) {
			t.Errorf("isRouteHelperName(%q) = false, want true", s)
		}
	}
	no := []string{"orders", "path", "Money.orders_path", "order.path", "to_path_string", ""}
	for _, s := range no {
		if isRouteHelperName(s) {
			t.Errorf("isRouteHelperName(%q) = true, want false", s)
		}
	}
}

// Framework context accessors (request/params/session/…) are never application
// methods — a bare reference must not emit a self-call that the resolver then
// binds to a coincidental same-named symbol (e.g. a test fake's #request).
func TestFrameworkAccessorsNotEmitted(t *testing.T) {
	r := extractERB(t, `<%= form_with url: request.path, method: :get %><%= params[:q] %>`, "v.erb")
	for _, bad := range []string{"self.request", "self.params"} {
		if findEdgeKind(r, bad, model.EdgeCalls) != nil {
			t.Errorf("framework accessor %q must not be emitted, got %v", bad, erbTargets(r))
		}
	}
}
