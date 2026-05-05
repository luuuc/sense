package mcpio

import (
	"encoding/json"
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

func TestMarshalConventionsNilSlices(t *testing.T) {
	resp := ConventionsResponse{
		KeySymbols:  nil,
		Conventions: nil,
		NextSteps:   nil,
	}
	out, err := MarshalConventions(resp)
	if err != nil {
		t.Fatal(err)
	}

	// Conventions and NextSteps don't have omitempty, so nil → [] matters
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	// conventions field should be "[]" not "null"
	if string(decoded["conventions"]) == "null" {
		t.Error("expected conventions to be [] not null")
	}
	// next_steps field should be "[]" not "null"
	if string(decoded["next_steps"]) == "null" {
		t.Error("expected next_steps to be [] not null")
	}
}

func TestMarshalConventionsNilInstances(t *testing.T) {
	resp := ConventionsResponse{
		KeySymbols: []KeySymbolEntry{
			{Name: "Router", Kind: "type", References: 5, Callers: nil},
			{Name: "Handler", Kind: "type", References: 3, Callers: []string{"main"}},
		},
		Conventions: []ConventionEntry{
			{Category: "design", Description: "test", Instances: nil},
		},
	}
	out, err := MarshalConventions(resp)
	if err != nil {
		t.Fatal(err)
	}

	// Instances (no omitempty) should become [] not null
	var decoded struct {
		KeySymbols []struct {
			Name    string   `json:"name"`
			Callers []string `json:"callers"`
		} `json:"key_symbols"`
		Conventions []struct {
			Instances []string `json:"instances"`
		} `json:"conventions"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Conventions[0].Instances == nil {
		t.Error("expected nil Instances to become [] in JSON")
	}
	// KeySymbols with callers populated should serialize correctly
	if len(decoded.KeySymbols) != 2 {
		t.Fatalf("expected 2 key symbols, got %d", len(decoded.KeySymbols))
	}
	if decoded.KeySymbols[0].Name != "Router" {
		t.Errorf("expected first key symbol Router, got %q", decoded.KeySymbols[0].Name)
	}
	// Handler has callers — should be present
	if len(decoded.KeySymbols[1].Callers) != 1 || decoded.KeySymbols[1].Callers[0] != "main" {
		t.Errorf("expected Handler callers=[main], got %v", decoded.KeySymbols[1].Callers)
	}
}
