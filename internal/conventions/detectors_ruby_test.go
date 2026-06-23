package conventions

import "testing"

func TestRubyBaseSignificance(t *testing.T) {
	tests := []struct {
		qualified string
		want      float64
	}{
		{"Payment::BaseProviderStrategy", 2.0}, // namespaced sub-architecture
		{"LafricaClient::Error", 2.0},          // per-client taxonomy
		{"ApplicationRecord", 0.0},             // generic framework base (bare)
		{"ActionController::Base", 0.0},        // framework base wins over the :: heuristic
		{"BaseCalculatorService", 1.0},         // custom unnamespaced base
		{"AdminController", 1.0},
	}
	for _, tt := range tests {
		if got := rubyBaseSignificance(tt.qualified); got != tt.want {
			t.Errorf("rubyBaseSignificance(%q) = %v, want %v", tt.qualified, got, tt.want)
		}
	}
}

func TestHasRubyExample(t *testing.T) {
	if !hasRubyExample([]Example{{Path: "app/models/order.rb"}}) {
		t.Error("expected .rb example to be Ruby")
	}
	if hasRubyExample([]Example{{Path: "internal/scan/store.go"}}) {
		t.Error("expected .go example not to be Ruby")
	}
	if hasRubyExample(nil) {
		t.Error("expected empty examples not to be Ruby")
	}
}

func TestRefineRubySignificance(t *testing.T) {
	rb := []Example{{Path: "app/controllers/admin_controller.rb"}}
	goEx := []Example{{Path: "internal/scan/store.go"}}
	conventions := []Convention{
		// Ruby inheritance: scored by base significance.
		{Category: CategoryInheritance, KeySymbol: "Payment::BaseProviderStrategy", Examples: rb},
		{Category: CategoryInheritance, KeySymbol: "ApplicationRecord", Examples: rb},
		{Category: CategoryComposition, KeySymbol: "Trackable", Examples: rb},
		// Go inheritance: left untouched (not Ruby).
		{Category: CategoryInheritance, KeySymbol: "io.Reader", Examples: goEx},
		// Non-inheritance/composition category: left untouched.
		{Category: CategoryNaming, KeySymbol: "Service", Examples: rb},
	}
	refineRubySignificance(conventions)

	if got := conventions[0].Significance; got != 2.0 {
		t.Errorf("namespaced Ruby base: Significance = %v, want 2.0", got)
	}
	if got := conventions[1].Significance; got != 0.0 {
		t.Errorf("framework Ruby base: Significance = %v, want 0.0", got)
	}
	if got := conventions[2].Significance; got != 1.0 {
		t.Errorf("custom Ruby mixin: Significance = %v, want 1.0", got)
	}
	if got := conventions[3].Significance; got != 0.0 {
		t.Errorf("Go convention should be untouched: Significance = %v, want 0.0", got)
	}
	if got := conventions[4].Significance; got != 0.0 {
		t.Errorf("naming convention should be untouched: Significance = %v, want 0.0", got)
	}
}

func TestDropSyntheticSymbols(t *testing.T) {
	in := []symbolRow{
		{name: "Order", qualified: "Order"},
		{name: "orders_path", qualified: "route:orders_path"},
		{name: "title", qualified: "i18n:users.show.title"},
		{name: "Account", qualified: "Account"},
	}
	out := dropSyntheticSymbols(in)
	if len(out) != 2 {
		t.Fatalf("dropSyntheticSymbols kept %d, want 2", len(out))
	}
	for _, s := range out {
		if s.qualified != "Order" && s.qualified != "Account" {
			t.Errorf("unexpected surviving symbol %q", s.qualified)
		}
	}
}
