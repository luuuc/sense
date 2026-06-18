package conventions

import (
	"strings"
	"testing"
)

// fileMap builds a filePathByID map from id->path pairs.
func fileMap(pairs map[int64]string) map[int64]string {
	out := make(map[int64]string, len(pairs))
	for id, p := range pairs {
		out[id] = p
	}
	return out
}

func TestAmbiguousTargetNames(t *testing.T) {
	nameByID := map[int64]string{
		1: "Base",  // collides with id 2
		2: "Base",  // collides with id 1
		3: "Order", // unique
	}
	got := ambiguousTargetNames(nameByID)
	if !got["Base"] {
		t.Error("Base shared by two distinct ids should be ambiguous")
	}
	if got["Order"] {
		t.Error("Order is held by a single id and must not be ambiguous")
	}
}

func TestBaseLabel(t *testing.T) {
	ambiguous := map[string]bool{"Base": true}
	// Ambiguous + distinct qualified -> qualify.
	if got := baseLabel("Base", "Foo::Base", ambiguous); got != "Foo::Base" {
		t.Errorf("ambiguous base label = %q, want Foo::Base", got)
	}
	// Ambiguous but no qualified name -> stay bare (cannot qualify).
	if got := baseLabel("Base", "", ambiguous); got != "Base" {
		t.Errorf("ambiguous base with empty qualified = %q, want Base", got)
	}
	// Ambiguous but qualified equals name -> stay bare (nothing to add).
	if got := baseLabel("Base", "Base", ambiguous); got != "Base" {
		t.Errorf("ambiguous base with qualified==name = %q, want Base", got)
	}
	// Not ambiguous -> stay bare even with a qualified name.
	if got := baseLabel("Order", "App::Order", ambiguous); got != "Order" {
		t.Errorf("unambiguous base label = %q, want Order", got)
	}
}

func TestDomainKindCounts_ExcludesTestFiles(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "A", kind: "class"},
		{id: 2, fileID: 2, name: "B", kind: "class"},
		{id: 3, fileID: 3, name: "ATest", kind: "class"}, // test file
	}
	paths := fileMap(map[int64]string{
		1: "app/a.rb",
		2: "app/b.rb",
		3: "test/a_test.rb",
	})
	counts := domainKindCounts(symbols, paths)
	if counts["class"] != 2 {
		t.Errorf("class count = %d, want 2 (test class excluded)", counts["class"])
	}
}

// TestDetectInheritance_QualifiesCollidingBasesAndExcludesTests pins both fixes:
// two distinct "Base" classes are qualified so they do not read as duplicates,
// and a base class inherited from a test file is not counted.
func TestDetectInheritance_QualifiesCollidingBasesAndExcludesTests(t *testing.T) {
	symbols := []symbolRow{
		// Two distinct base classes sharing the bare name "Base".
		{id: 100, fileID: 10, name: "Base", qualified: "Foo::Base", kind: "class"},
		{id: 101, fileID: 11, name: "Base", qualified: "Bar::Base", kind: "class"},
		// Foo family (3 domain subclasses).
		{id: 1, fileID: 1, name: "FooA", kind: "class"},
		{id: 2, fileID: 2, name: "FooB", kind: "class"},
		{id: 3, fileID: 3, name: "FooC", kind: "class"},
		// Bar family (3 domain subclasses).
		{id: 4, fileID: 4, name: "BarA", kind: "class"},
		{id: 5, fileID: 5, name: "BarB", kind: "class"},
		{id: 6, fileID: 6, name: "BarC", kind: "class"},
		// A test subclass that also extends Foo::Base — must be excluded.
		{id: 200, fileID: 20, name: "FooTest", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "app/foo_a.rb", 2: "app/foo_b.rb", 3: "app/foo_c.rb",
		4: "app/bar_a.rb", 5: "app/bar_b.rb", 6: "app/bar_c.rb",
		10: "lib/foo/base.rb", 11: "lib/bar/base.rb",
		20: "test/foo_test.rb",
	})
	edges := []edgeRow{
		{sourceID: 1, targetID: 100, kind: "inherits"},
		{sourceID: 2, targetID: 100, kind: "inherits"},
		{sourceID: 3, targetID: 100, kind: "inherits"},
		{sourceID: 4, targetID: 101, kind: "inherits"},
		{sourceID: 5, targetID: 101, kind: "inherits"},
		{sourceID: 6, targetID: 101, kind: "inherits"},
		{sourceID: 200, targetID: 100, kind: "inherits"}, // test source, excluded
	}
	convs := detectInheritance(symbols, edges, indexSymbols(symbols), paths)
	if len(convs) != 2 {
		t.Fatalf("expected 2 inheritance conventions, got %d: %v", len(convs), convs)
	}
	var foo, bar *Convention
	for i := range convs {
		switch {
		case strings.Contains(convs[i].Description, "Foo::Base"):
			foo = &convs[i]
		case strings.Contains(convs[i].Description, "Bar::Base"):
			bar = &convs[i]
		}
	}
	if foo == nil || bar == nil {
		t.Fatalf("expected qualified Foo::Base and Bar::Base descriptions, got: %v", convs)
	}
	// Test subclass excluded: Foo::Base has 3 instances, not 4.
	if foo.Instances != 3 {
		t.Errorf("Foo::Base instances = %d, want 3 (test subclass excluded)", foo.Instances)
	}
	// Denominator excludes the test class: 8 domain classes total.
	if foo.Total != 8 {
		t.Errorf("Foo::Base total = %d, want 8 (test class excluded from denominator)", foo.Total)
	}
}

// TestDetectComposition_QualifiesCollidingMixinsAndExcludesTests mirrors the
// inheritance test for the composition (include/compose) path.
func TestDetectComposition_QualifiesCollidingMixinsAndExcludesTests(t *testing.T) {
	symbols := []symbolRow{
		{id: 100, fileID: 10, name: "Trackable", qualified: "Billing::Trackable", kind: "module"},
		{id: 101, fileID: 11, name: "Trackable", qualified: "Audit::Trackable", kind: "module"},
		{id: 1, fileID: 1, name: "Invoice", kind: "class"},
		{id: 2, fileID: 2, name: "Payment", kind: "class"},
		{id: 3, fileID: 3, name: "Refund", kind: "class"},
		{id: 4, fileID: 4, name: "LoginEvent", kind: "class"},
		{id: 5, fileID: 5, name: "LogoutEvent", kind: "class"},
		{id: 6, fileID: 6, name: "ResetEvent", kind: "class"},
		{id: 200, fileID: 20, name: "InvoiceTest", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "app/invoice.rb", 2: "app/payment.rb", 3: "app/refund.rb",
		4: "app/login_event.rb", 5: "app/logout_event.rb", 6: "app/reset_event.rb",
		10: "lib/billing/trackable.rb", 11: "lib/audit/trackable.rb",
		20: "test/invoice_test.rb",
	})
	edges := []edgeRow{
		{sourceID: 1, targetID: 100, kind: "includes"},
		{sourceID: 2, targetID: 100, kind: "includes"},
		{sourceID: 3, targetID: 100, kind: "includes"},
		{sourceID: 4, targetID: 101, kind: "includes"},
		{sourceID: 5, targetID: 101, kind: "includes"},
		{sourceID: 6, targetID: 101, kind: "includes"},
		{sourceID: 200, targetID: 100, kind: "includes"}, // test source, excluded
	}
	convs := detectComposition(symbols, edges, indexSymbols(symbols), paths)
	if len(convs) != 2 {
		t.Fatalf("expected 2 composition conventions, got %d: %v", len(convs), convs)
	}
	gotBilling, gotAudit := false, false
	for _, c := range convs {
		if strings.Contains(c.Description, "Billing::Trackable") {
			gotBilling = true
			if c.Instances != 3 {
				t.Errorf("Billing::Trackable instances = %d, want 3 (test includer excluded)", c.Instances)
			}
		}
		if strings.Contains(c.Description, "Audit::Trackable") {
			gotAudit = true
		}
	}
	if !gotBilling || !gotAudit {
		t.Errorf("expected qualified Billing::Trackable and Audit::Trackable, got: %v", convs)
	}
}

func TestDetectNaming_ExcludesTestSymbols(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "CheckoutService", kind: "class"},
		{id: 2, fileID: 2, name: "PaymentService", kind: "class"},
		{id: 3, fileID: 3, name: "ShippingService", kind: "class"},
		// Three *Test classes in test files — must not form a naming convention.
		{id: 4, fileID: 4, name: "CheckoutServiceTest", kind: "class"},
		{id: 5, fileID: 5, name: "PaymentServiceTest", kind: "class"},
		{id: 6, fileID: 6, name: "ShippingServiceTest", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "app/checkout_service.rb", 2: "app/payment_service.rb", 3: "app/shipping_service.rb",
		4: "test/checkout_service_test.rb", 5: "test/payment_service_test.rb", 6: "test/shipping_service_test.rb",
	})
	convs := detectNaming(symbols, paths)
	for _, c := range convs {
		if strings.Contains(c.Description, "*Test ") || strings.Contains(c.Description, "*_test.rb") {
			t.Errorf("test-derived naming convention leaked into domain naming: %q", c.Description)
		}
	}
	// The domain *Service convention should still be present.
	hasService := false
	for _, c := range convs {
		if strings.Contains(c.Description, "*Service") {
			hasService = true
		}
	}
	if !hasService {
		t.Errorf("expected domain *Service naming convention, got: %v", convs)
	}
}

func TestDetectStructure_ExcludesTestDirs(t *testing.T) {
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "A", kind: "class"},
		{id: 2, fileID: 2, name: "B", kind: "class"},
		{id: 3, fileID: 3, name: "C", kind: "class"},
		{id: 4, fileID: 4, name: "ATest", kind: "class"},
		{id: 5, fileID: 5, name: "BTest", kind: "class"},
		{id: 6, fileID: 6, name: "CTest", kind: "class"},
	}
	paths := fileMap(map[int64]string{
		1: "app/models/a.rb", 2: "app/models/b.rb", 3: "app/models/c.rb",
		4: "test/models/a_test.rb", 5: "test/models/b_test.rb", 6: "test/models/c_test.rb",
	})
	convs := detectStructure(symbols, paths)
	for _, c := range convs {
		if strings.Contains(c.Description, "test/models") {
			t.Errorf("test directory leaked into structure conventions: %q", c.Description)
		}
	}
	hasAppModels := false
	for _, c := range convs {
		if strings.Contains(c.Description, "app/models") {
			hasAppModels = true
		}
	}
	if !hasAppModels {
		t.Errorf("expected app/models structure convention, got: %v", convs)
	}
}
