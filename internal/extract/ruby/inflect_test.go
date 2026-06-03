package ruby

import "testing"

func TestSingularize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"orders", "order"},
		{"line_items", "line_item"},
		{"tags", "tag"},
		{"categories", "category"},
		{"variants", "variant"},
		{"addresses", "address"},
		{"statuses", "status"},
		{"classes", "class"},
		{"buses", "bus"},
		{"user", "user"},
		{"invoice", "invoice"},
		{"", ""},
		// Additional test cases for full coverage
		{"processes", "process"}, // sses → ss
		{"kisses", "kiss"},       // sses → ss
		{"boxes", "box"},         // xes → x
		{"faxes", "fax"},         // xes → x
		{"buzzes", "buzz"},       // zes → z
		{"quizzes", "quiz"},      // zes → z
		{"ass", "ass"},           // should not remove s from "ass"
		{"mass", "mass"},         // should not remove s from "mass"
		{"glass", "glass"},       // should not remove s from "glass"
		{"companies", "company"}, // ies → y
		{"cities", "city"},       // ies → y
		{"stories", "story"},     // ies → y
	}
	for _, tc := range cases {
		if got := singularize(tc.in); got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClassify(t *testing.T) {
	cases := []struct{ in, want string }{
		{"orders", "Order"},
		{"line_items", "LineItem"},
		{"tags", "Tag"},
		{"categories", "Category"},
		{"user", "User"},
		{"warehouse", "Warehouse"},
		{"product_category", "ProductCategory"},
	}
	for _, tc := range cases {
		if got := classify(tc.in); got != tc.want {
			t.Errorf("classify(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExportedInflectionWrappers(t *testing.T) {
	if got := Singularize("orders"); got != "order" {
		t.Errorf("Singularize(orders) = %q, want order", got)
	}
	if got := Classify("line_items"); got != "LineItem" {
		t.Errorf("Classify(line_items) = %q, want LineItem", got)
	}
}
