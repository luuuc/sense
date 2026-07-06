package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// djangoRefs models the same-named ProductVariant collision that breaks the
// Django reverse-FK fan-out: a GraphQL ObjectType (lower scan id) and the ORM
// model (higher id) share the qualified name "ProductVariant". OrderLine is the
// dependent declaring the FK, in an order/models.py models module. Extra
// fixtures cover the no-op gates (a non-models source, an all-non-model
// collision).
func djangoRefs() []model.SymbolRef {
	return []model.SymbolRef{
		// dependent declaring the FK, in a models module
		{ID: 50, Qualified: "OrderLine", FileID: 50, Language: "python", Path: "saleor/order/models.py"},
		// a service in the same app but NOT a models module
		{ID: 51, Qualified: "OrderService", FileID: 51, Language: "python", Path: "saleor/order/services.py"},
		// the collision: GraphQL type at the LOWER id, ORM model at the higher id
		{ID: 60, Qualified: "ProductVariant", FileID: 60, Language: "python", Path: "saleor/graphql/product/types/products.py"},
		{ID: 61, Qualified: "ProductVariant", FileID: 61, Language: "python", Path: "saleor/product/models.py"},
		// an all-non-model collision (two GraphQL-layer symbols, no models module)
		{ID: 70, Qualified: "Money", FileID: 60, Language: "python", Path: "saleor/graphql/core/types/money.py"},
		{ID: 71, Qualified: "Money", FileID: 62, Language: "python", Path: "saleor/graphql/order/types.py"},
	}
}

func TestResolveComposesPrefersDjangoModel(t *testing.T) {
	ix := resolve.NewIndex(djangoRefs())

	// A composes edge from OrderLine (an order/models.py models module) to the
	// colliding "ProductVariant" must bind to the ORM model (id 61), beating the
	// GraphQL type (id 60) that would otherwise win by lowest id. Confidence is
	// still clamped + flagged ambiguous: the gate picks the right candidate, it
	// does not remove the ambiguity.
	r, ok := ix.Resolve(resolve.Request{
		Target:         "ProductVariant",
		Kind:           model.EdgeComposes,
		SourceFileID:   50,
		BaseConfidence: 0.9,
	})
	if !ok {
		t.Fatal("expected resolution")
	}
	if r.SymbolID != 61 {
		t.Errorf("SymbolID = %d, want 61 (the ORM model, not the lower-id GraphQL type)", r.SymbolID)
	}
	if r.Confidence != 0.8 {
		t.Errorf("Confidence = %v, want 0.8 (ambiguous clamp still applies)", r.Confidence)
	}
	if !r.Ambiguous {
		t.Error("Ambiguous = false, want true (multiple candidates)")
	}
}

func TestResolveComposesModelPreferenceNoOps(t *testing.T) {
	ix := resolve.NewIndex(djangoRefs())

	cases := []struct {
		name   string
		req    resolve.Request
		wantID int64
	}{
		{
			// Not a composes edge: the gate never fires, lowest id wins.
			name:   "non-composes kind",
			req:    resolve.Request{Target: "ProductVariant", Kind: model.EdgeInherits, SourceFileID: 50, BaseConfidence: 0.9},
			wantID: 60,
		},
		{
			// Source is order/services.py, not a models module: not an ORM
			// relation, so no preference; lowest id wins.
			name:   "source not a models module",
			req:    resolve.Request{Target: "ProductVariant", Kind: model.EdgeComposes, SourceFileID: 51, BaseConfidence: 0.9},
			wantID: 60,
		},
		{
			// The collision has no models-module candidate (both GraphQL layer):
			// nothing to prefer, lowest id stands.
			name:   "no model candidate",
			req:    resolve.Request{Target: "Money", Kind: model.EdgeComposes, SourceFileID: 50, BaseConfidence: 0.9},
			wantID: 70,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, ok := ix.Resolve(c.req)
			if !ok {
				t.Fatal("expected resolution")
			}
			if r.SymbolID != c.wantID {
				t.Errorf("SymbolID = %d, want %d", r.SymbolID, c.wantID)
			}
		})
	}
}

func TestDjangoRelatedSyntheticUniquenessGate(t *testing.T) {
	// Two files declaring the same related_name emit two synthetics with the
	// same qualified name; an accessor edge to it proves nothing about WHICH
	// model anchors, so resolution must DROP it (closed-world), never bind one
	// at the confident tier. A repo-unique related_name resolves normally.
	// The index also carries a bare application symbol named like an accessor
	// ("addons") to prove the drop is terminal: resolveQualified must not fall
	// through to the leaf fallback and bind the bare name at a demoted tier.
	ix := resolve.NewIndex([]model.SymbolRef{
		{ID: 80, Qualified: "django-related:addons", FileID: 80, Language: "python", Path: "src/pretix/base/models/items.py"},
		{ID: 81, Qualified: "django-related:addons", FileID: 81, Language: "python", Path: "src/pretix/base/models/orders.py"},
		{ID: 82, Qualified: "django-related:all_positions", FileID: 81, Language: "python", Path: "src/pretix/base/models/orders.py"},
		{ID: 83, Qualified: "orders.addons", FileID: 85, Language: "python", Path: "src/pretix/base/decoys.py"},
	})

	if r, ok := ix.Resolve(resolve.Request{
		Target:         "django-related:addons",
		Kind:           model.EdgeCalls,
		SourceFileID:   90,
		BaseConfidence: 0.8,
	}); ok {
		t.Errorf("ambiguous django-related target must not resolve (got id=%d conf=%v)", r.SymbolID, r.Confidence)
	}

	// Same-file preference must not defeat the gate: a source file that IS one
	// of the synthetics' files still gets the drop — pickBest's same-file pick
	// among several candidates stays flagged Ambiguous.
	if r, ok := ix.Resolve(resolve.Request{
		Target:         "django-related:addons",
		Kind:           model.EdgeCalls,
		SourceFileID:   81,
		BaseConfidence: 0.8,
	}); ok {
		t.Errorf("same-file source must not defeat the ambiguity drop (got id=%d)", r.SymbolID)
	}

	r, ok := ix.Resolve(resolve.Request{
		Target:         "django-related:all_positions",
		Kind:           model.EdgeCalls,
		SourceFileID:   90,
		BaseConfidence: 0.8,
	})
	if !ok {
		t.Fatal("unique django-related target must resolve")
	}
	if r.SymbolID != 82 || r.Confidence != 0.8 || r.Ambiguous {
		t.Errorf("unique synthetic: got id=%d conf=%v ambiguous=%v, want 82/0.8/false", r.SymbolID, r.Confidence, r.Ambiguous)
	}
}
