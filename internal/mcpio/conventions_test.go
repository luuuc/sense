package mcpio

import (
	"strings"
	"testing"
)

func makeConventions(n int) []ConventionEntry {
	entries := make([]ConventionEntry, n)
	for i := range entries {
		strength := 1.0 - float64(i)*0.05
		if strength < 0.1 {
			strength = 0.1
		}
		entries[i] = ConventionEntry{
			Category:       "naming",
			Description:    "pattern " + string(rune('A'+i)),
			Strength:       Confidence(strength),
			Instances:      []string{"Foo", "Bar", "Baz"},
			TotalInstances: 10,
		}
	}
	return entries
}

func TestApplyTokenBudgetNoTruncation(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: makeConventions(3),
	}
	ApplyTokenBudget(&resp, DefaultTokenBudget)
	if resp.Truncated {
		t.Error("expected truncated=false for small response")
	}
	if len(resp.Conventions) != 3 {
		t.Errorf("expected 3 conventions, got %d", len(resp.Conventions))
	}
	if resp.TokenBudget != DefaultTokenBudget {
		t.Errorf("expected token_budget=%d, got %d", DefaultTokenBudget, resp.TokenBudget)
	}
}

func TestApplyTokenBudgetTruncatesWeakest(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: makeConventions(20),
	}
	before := len(resp.Conventions)

	// Use a small budget that forces truncation
	ApplyTokenBudget(&resp, 500)

	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
	if len(resp.Conventions) >= before {
		t.Errorf("expected fewer conventions after truncation: before=%d after=%d", before, len(resp.Conventions))
	}
	if len(resp.Conventions) == 0 {
		t.Fatal("should not truncate to zero conventions")
	}
	for i := 1; i < len(resp.Conventions); i++ {
		if resp.Conventions[i].Strength > resp.Conventions[i-1].Strength {
			t.Errorf("conventions not in strength order after truncation at index %d", i)
		}
	}
}

func TestApplyTokenBudgetTinyBudget(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: makeConventions(5),
	}
	ApplyTokenBudget(&resp, 1)
	if len(resp.Conventions) != 0 {
		t.Errorf("expected 0 conventions with budget=1, got %d", len(resp.Conventions))
	}
	if !resp.Truncated {
		t.Error("expected truncated=true")
	}
}

func TestApplyTokenBudgetEmpty(t *testing.T) {
	resp := ConventionsResponse{}
	ApplyTokenBudget(&resp, DefaultTokenBudget)
	if resp.Truncated {
		t.Error("expected truncated=false for empty response")
	}
}

func TestBuildConventionsSummary(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: []ConventionEntry{
			{Description: "All services inherit ApplicationService", Instances: []string{"CheckoutService", "PaymentService"}},
			{Description: "Controllers include Authentication.", Instances: []string{"OrdersController", "UsersController"}},
			{Description: "Tests mirror source structure", Instances: []string{"checkout_test.rb", "payment_test.rb"}},
		},
	}
	BuildConventionsSummary(&resp)
	want := "All services inherit ApplicationService; Controllers include Authentication; Tests mirror source structure."
	if resp.Summary != want {
		t.Errorf("summary mismatch\n got: %q\nwant: %q", resp.Summary, want)
	}
}

func TestBuildConventionsSummaryPrefersTypeNames(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: []ConventionEntry{
			{Description: "test files *_test.rb pattern", Instances: []string{"checkout_test.rb", "payment_test.rb", "order_test.rb"}},
			{Description: "class files *_service.rb pattern", Instances: []string{"checkout_service.rb", "payment_service.rb"}},
			{Description: "CheckoutService, PaymentService inherit ApplicationService", Instances: []string{"CheckoutService", "PaymentService", "ShippingService"}},
			{Description: "OrdersController, UsersController include Authentication", Instances: []string{"OrdersController", "UsersController", "AdminController"}},
			{Description: "class pattern: Order, User, Product in app/models/", Instances: []string{"Order", "User", "Product"}},
		},
	}
	BuildConventionsSummary(&resp)
	if strings.Contains(resp.Summary, "_test.rb") || strings.Contains(resp.Summary, "_service.rb") {
		t.Errorf("summary should prefer type-name conventions over file-name conventions, got: %q", resp.Summary)
	}
	if !strings.Contains(resp.Summary, "ApplicationService") || !strings.Contains(resp.Summary, "Authentication") {
		t.Errorf("summary should include type-rich conventions, got: %q", resp.Summary)
	}
}

func TestBuildConventionsSummaryEmpty(t *testing.T) {
	resp := ConventionsResponse{}
	BuildConventionsSummary(&resp)
	if resp.Summary != "" {
		t.Errorf("expected empty summary for no conventions, got %q", resp.Summary)
	}
}

func TestBuildConventionsSummaryFewerThanThree(t *testing.T) {
	resp := ConventionsResponse{
		Conventions: []ConventionEntry{
			{Description: "Single pattern"},
		},
	}
	BuildConventionsSummary(&resp)
	if resp.Summary != "Single pattern." {
		t.Errorf("expected 'Single pattern.', got %q", resp.Summary)
	}
}
