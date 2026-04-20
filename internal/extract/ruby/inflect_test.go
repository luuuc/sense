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
