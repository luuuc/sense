package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// A model that invokes an acts_as_* plugin macro gains a calls edge to the
// macro method. The macro's own body wires it to the collaborator class
// (acts_as_attachable -> Attachment), so the model -> macro edge completes the
// two-hop path the model -> collaborator dependency travels — a grep-invisible
// link (the model never names the collaborator) that the index could not follow
// before.
func TestActsAsMacroEmitsCallEdge(t *testing.T) {
	r := parseRubyWithPath(t, `class Issue < ApplicationRecord
  acts_as_attachable
  acts_as_watchable
end
`, "issue.rb")

	for _, macro := range []string{"acts_as_attachable", "acts_as_watchable"} {
		edge := findEdge(r, "Issue", macro, "calls")
		if edge == nil {
			t.Fatalf("missing calls edge Issue -> %s", macro)
		}
		if edge.Confidence != extract.ConfidenceConvention {
			t.Errorf("%s edge confidence = %v, want %v (convention)", macro, edge.Confidence, extract.ConfidenceConvention)
		}
	}
}

// The macro edge is attributed to the fully-qualified enclosing model, including
// a namespace, so it resolves to the right class.
func TestActsAsMacroNamespacedModel(t *testing.T) {
	r := parseRubyWithPath(t, `module Spree
  class Order < ApplicationRecord
    acts_as_taggable
  end
end
`, "order.rb")

	if findEdge(r, "Spree::Order", "acts_as_taggable", "calls") == nil {
		t.Error("missing calls edge Spree::Order -> acts_as_taggable")
	}
}

// A bare method that merely starts with "acts" but is not an acts_as_* macro
// must not be treated as one (no spurious edge from the prefix check).
func TestActsAsMacroPrefixIsExact(t *testing.T) {
	r := parseRubyWithPath(t, `class C
  acts_like_a_duck
  action_required
end
`, "c.rb")

	for _, name := range []string{"acts_like_a_duck", "action_required"} {
		if findEdge(r, "C", name, "calls") != nil {
			t.Errorf("non-acts_as_ macro %q should not get a class-body macro edge", name)
		}
	}
}
