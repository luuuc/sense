package conventions

import (
	"strings"
	"testing"
)

func TestRefinePHPSignificance(t *testing.T) {
	conventions := []Convention{
		{Category: CategoryInheritance, KeySymbol: `Illuminate\Database\Eloquent\Model`,
			Examples: []Example{{Path: "app/Models/Order.php"}}},
		{Category: CategoryInheritance, KeySymbol: `Model`,
			Examples: []Example{{Path: "app/Models/User.php"}}},
		{Category: CategoryInheritance, KeySymbol: `App\Support\FormRequest`,
			Examples: []Example{{Path: "app/Http/Requests/StoreOrder.php"}}},
		{Category: CategoryInheritance, KeySymbol: `App\Payments\BaseGateway`,
			Examples: []Example{{Path: "app/Payments/Stripe.php"}}},
		{Category: CategoryInheritance, KeySymbol: "ApplicationRecord",
			Examples: []Example{{Path: "app/models/order.rb"}}, Significance: 0.5},
		{Category: CategoryNaming, KeySymbol: `Model`,
			Examples: []Example{{Path: "app/Models/Order.php"}}, Significance: 0.5},
	}
	refinePHPSignificance(conventions)

	if conventions[0].Significance != 0.0 {
		t.Errorf("qualified framework base significance = %v, want 0", conventions[0].Significance)
	}
	if conventions[1].Significance != 0.0 {
		t.Errorf("bare framework base significance = %v, want 0", conventions[1].Significance)
	}
	// A project class whose LEAF matches a framework base name is still the
	// framework speaking (an app-level FormRequest re-export).
	if conventions[2].Significance != 0.0 {
		t.Errorf("leaf-matched framework base significance = %v, want 0", conventions[2].Significance)
	}
	if conventions[3].Significance != 1.0 {
		t.Errorf("project base significance = %v, want 1", conventions[3].Significance)
	}
	// Ruby rows and non-inheritance categories are untouched.
	if conventions[4].Significance != 0.5 || conventions[5].Significance != 0.5 {
		t.Errorf("non-PHP / non-inheritance rows touched: %v, %v",
			conventions[4].Significance, conventions[5].Significance)
	}
}

func TestDetectPHPTestStyle(t *testing.T) {
	files := map[int64]string{
		1: "tests/Unit/OrderTest.php",
		2: "tests/Unit/UserTest.php",
		3: "tests/Feature/CheckoutTest.php",
		4: "tests/Feature/PestOneTest.php",
		5: "tests/Feature/PestTwoTest.php",
		6: "tests/Feature/PestThreeTest.php",
		7: "app/Models/Order.php", // not a test file
		8: "spec/models/order_spec.rb",
	}
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "OrderTest", kind: "class"},
		{id: 2, fileID: 2, name: "UserTest", kind: "class"},
		{id: 3, fileID: 3, name: "CheckoutTest", kind: "class"},
		{id: 4, fileID: 7, name: "Order", kind: "class"},
	}
	out := detectPHPTestStyle(symbols, nil, nil, files)
	if len(out) != 2 {
		t.Fatalf("rows = %d, want 2 (PHPUnit + Pest): %+v", len(out), out)
	}
	var unit, pest *Convention
	for i := range out {
		if strings.Contains(out[i].Description, "PHPUnit") {
			unit = &out[i]
		} else if strings.Contains(out[i].Description, "Pest") {
			pest = &out[i]
		}
	}
	if unit == nil || unit.Instances != 3 || unit.Total != 6 {
		t.Errorf("PHPUnit row = %+v", unit)
	}
	if pest == nil || pest.Instances != 3 || pest.Total != 6 {
		t.Errorf("Pest row = %+v", pest)
	}
}

func TestDetectPHPTestStyleBelowFloor(t *testing.T) {
	files := map[int64]string{
		1: "tests/OrderTest.php",
		2: "tests/UserTest.php",
	}
	symbols := []symbolRow{
		{id: 1, fileID: 1, name: "OrderTest", kind: "class"},
	}
	if out := detectPHPTestStyle(symbols, nil, nil, files); len(out) != 0 {
		t.Errorf("sub-floor families emitted rows: %+v", out)
	}
}
