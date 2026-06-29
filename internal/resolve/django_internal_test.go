package resolve

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func TestIsDjangoModelPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"saleor/product/models.py", true},                  // app/models.py
		{"saleor/attribute/models/base.py", true},           // app/models/ package
		{"saleor/attribute/models/__init__.py", true},       // package init
		{"models.py", true},                                 // models.py at repo root
		{"models/foo.py", true},                             // models/ package at repo root
		{"saleor/graphql/product/types/products.py", false}, // GraphQL type, not a model
		{"saleor/order/services.py", false},
		{"saleor/product/managers.py", false}, // "models" substring absent
		{"app/model.py", false},               // singular, not the convention
		{"", false},
	}
	for _, c := range cases {
		if got := isDjangoModelPath(c.path); got != c.want {
			t.Errorf("isDjangoModelPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsDjangoModelModuleRef(t *testing.T) {
	cases := []struct {
		name string
		ref  model.SymbolRef
		want bool
	}{
		{"python model module", model.SymbolRef{Language: "python", Path: "saleor/product/models.py"}, true},
		{"python non-model", model.SymbolRef{Language: "python", Path: "saleor/order/services.py"}, false},
		{"ruby models path is not Django", model.SymbolRef{Language: "ruby", Path: "app/models/order.rb"}, false},
		{"empty", model.SymbolRef{}, false},
	}
	for _, c := range cases {
		if got := isDjangoModelModuleRef(c.ref); got != c.want {
			t.Errorf("%s: isDjangoModelModuleRef = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestPreferDjangoModelComposesAllModels covers the branch where every candidate
// is already in a models module: the gate must not narrow (there is nothing to
// discriminate), leaving pickBest's lowest-id tie-break to stand.
func TestPreferDjangoModelComposesAllModels(t *testing.T) {
	refs := []model.SymbolRef{
		{ID: 1, Qualified: "OrderLine", FileID: 1, Language: "python", Path: "saleor/order/models.py"},
		// two same-named models in different apps' models modules
		{ID: 2, Qualified: "Address", FileID: 2, Language: "python", Path: "saleor/account/models.py"},
		{ID: 3, Qualified: "Address", FileID: 3, Language: "python", Path: "saleor/order/models/address.py"},
	}
	ix := NewIndex(refs)
	matches := ix.byQualified["Address"]
	got := ix.preferDjangoModelComposes(matches, Request{
		Target: "Address", Kind: model.EdgeComposes, SourceFileID: 1, BaseConfidence: 0.9,
	})
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2 (all-models candidate set must not be narrowed)", len(got))
	}
}
