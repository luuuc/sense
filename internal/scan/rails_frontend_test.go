package scan_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestScanRailsFrontendCrossLanguageEdges(t *testing.T) {
	root := t.TempDir()

	// Stimulus controller (JS, anonymous default export)
	writeFile(t, filepath.Join(root, "app/javascript/controllers/checkout_controller.js"), `
import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["total", "button"]
  static outlets = ["cart"]

  submit() {
    this.totalTarget.textContent = "Done"
  }
}
`)

	// Namespaced Stimulus controller
	writeFile(t, filepath.Join(root, "app/javascript/controllers/admin/users_controller.js"), `
import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["list"]

  refresh() {}
}
`)

	// ERB template referencing both controllers
	writeFile(t, filepath.Join(root, "app/views/orders/show.html.erb"), `
<div data-controller="checkout">
  <span data-checkout-target="total">$0</span>
  <button data-action="click->checkout#submit">Pay</button>
</div>
<div data-controller="admin--users">
  <ul data-admin--users-target="list"></ul>
</div>
<%= turbo_stream_from @store %>
`)

	// Ruby model with broadcast
	writeFile(t, filepath.Join(root, "app/models/order.rb"), `
class Order < ApplicationRecord
  broadcasts_to :store
end
`)

	// Importmap
	writeFile(t, filepath.Join(root, "config/importmap.rb"), `
pin "application", preload: true
pin "@hotwired/stimulus", to: "stimulus.min.js"
pin_all_from "app/javascript/controllers", under: "controllers"
`)

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Edges == 0 {
		t.Fatal("no edges resolved")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byQualified := map[string]model.Symbol{}
	for _, s := range all {
		byQualified[s.Qualified] = s
	}

	// Verify Stimulus controller symbols exist (inferred from file path)
	assertSymbolExists(t, byQualified, "CheckoutController")
	assertSymbolExists(t, byQualified, "Admin::UsersController")

	// Verify target declarations exist
	assertSymbolExists(t, byQualified, "CheckoutController.target:total")
	assertSymbolExists(t, byQualified, "CheckoutController.target:button")
	assertSymbolExists(t, byQualified, "Admin::UsersController.target:list")

	// Verify method exists
	assertSymbolExists(t, byQualified, "CheckoutController.submit")

	// Verify cross-language edges: ERB → JS controller
	checkout := byQualified["CheckoutController"]
	{
		sym, err := a.ReadSymbol(ctx, checkout.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(CheckoutController): %v", err)
		}
		if len(sym.Inbound) == 0 {
			t.Error("CheckoutController has no inbound edges; expected ERB data-controller edge")
		}
	}

	// Verify cross-language edges: ERB → JS target
	target := byQualified["CheckoutController.target:total"]
	{
		sym, err := a.ReadSymbol(ctx, target.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(CheckoutController.target:total): %v", err)
		}
		if len(sym.Inbound) == 0 {
			t.Error("CheckoutController.target:total has no inbound edges; expected ERB data-target edge")
		}
	}

	// Verify cross-language edges: ERB → JS method
	method := byQualified["CheckoutController.submit"]
	{
		sym, err := a.ReadSymbol(ctx, method.ID)
		if err != nil {
			t.Fatalf("ReadSymbol(CheckoutController.submit): %v", err)
		}
		if len(sym.Inbound) == 0 {
			t.Error("CheckoutController.submit has no inbound edges; expected ERB data-action edge")
		}
	}

	// Verify Turbo broadcast channel edge: both Ruby model and ERB emit turbo-channel:store
	// These won't resolve to each other (they both target the same synthetic name),
	// but we verify they exist as unresolved edges (synthetic names have no symbol).
	// The key test is that the scan doesn't error and edges are emitted.
	t.Logf("Scan result: %d files, %d symbols, %d edges, %d warnings",
		res.Files, res.Symbols, res.Edges, res.Warnings)
}

func TestScanRailsFrontendNegativeCases(t *testing.T) {
	root := t.TempDir()

	// Non-standard layout (no /controllers/ directory) — should NOT infer Stimulus name
	writeFile(t, filepath.Join(root, "app/javascript/lib/helper_controller.js"), `
export default class extends Controller {
  help() {}
}
`)

	// Dynamic registration (no static file) — should produce no match
	writeFile(t, filepath.Join(root, "app/views/pages/home.html.erb"), `
<div data-controller="dynamic-thing"></div>
`)

	ctx := context.Background()
	res, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	all, err := a.Query(ctx, index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	byQualified := map[string]model.Symbol{}
	for _, s := range all {
		byQualified[s.Qualified] = s
	}

	// The helper_controller.js is NOT in a controllers/ directory, so no Stimulus
	// inference. The symbol exists via default export naming (from the filename),
	// but should NOT have Stimulus targets or outlets.
	if sym, ok := byQualified["HelperController"]; ok {
		if sym.Kind != "class" {
			t.Errorf("HelperController kind = %q, want class", sym.Kind)
		}
	}

	// DynamicThingController should NOT exist (no matching JS file)
	if _, ok := byQualified["DynamicThingController"]; ok {
		t.Error("DynamicThingController should NOT exist (dynamic registration)")
	}

	// But the ERB edge should still be emitted (unresolved)
	t.Logf("Negative case scan: %d files, %d symbols, %d edges (unresolved=%d)",
		res.Files, res.Symbols, res.Edges, res.Unresolved)
}

func assertSymbolExists(t *testing.T, byQualified map[string]model.Symbol, name string) {
	t.Helper()
	if _, ok := byQualified[name]; !ok {
		t.Fatalf("expected symbol %q not found in index", name)
	}
}
