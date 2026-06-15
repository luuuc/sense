package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func TestTestSuperclassWithoutTestSuffixEmitsNoTestsEdge(t *testing.T) {
	// `class Helpers < ActiveSupport::TestCase` inherits a test base class, but
	// the class name has no "Test" suffix so inferTestedClass yields "" — the
	// inherits edge is still emitted, but no conventional tests edge.
	r := parseRuby(t, "class Helpers < ActiveSupport::TestCase\nend")
	if findEdge(r, "Helpers", "ActiveSupport::TestCase", string(model.EdgeInherits)) == nil {
		t.Error("missing inherits edge to the test base class")
	}
	for _, e := range r.edges {
		if string(e.Kind) == string(model.EdgeTests) {
			t.Errorf("class without a Test suffix must emit no tests edge, got %v", e.TargetQualified)
		}
	}
}

func TestBareIdentifierCallEdgeEmitError(t *testing.T) {
	// `helper` in a method body is a bare-identifier call. With the class and
	// method symbols allowed, the bare-identifier calls edge is the first edge
	// emitted; failing it must propagate out of handleMethod.
	err := parseWithEmitter(t, "class Foo\n  def go\n    helper\n  end\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing bare-identifier calls edge to propagate")
	}
}

// TestRelativeSuperclassEmitsBareAncestorName pins the namespaced-inheritance
// gap (the root cause of `blast Shop::Base` missing its subclasses). A subclass
// nested two modules deep that names its superclass *relatively* (`< Base`,
// where the real base is `Shop::Base` one level up) emits the inherits target as
// the bare written text "Base" — superclassName returns the raw source text with
// no lexical-scope qualification. Because the resolver matches inherits by exact
// qualified name only (it is not a gated kind, so there is no leaf fallback),
// that bare "Base" never binds to the `Shop::Base` symbol and the edge is
// dropped (target_id is NOT NULL). Collision-free fixture: a single Base, a
// single Widget, so nothing here can resolve by coincidence.
func TestRelativeSuperclassEmitsBareAncestorName(t *testing.T) {
	src := `module Shop
  class Base
  end
  module Items
    class Widget < Base
    end
  end
end`
	r := parseRuby(t, src)

	if findSymbol(r, "Shop::Base") == nil {
		t.Fatal("expected base symbol Shop::Base to be recorded")
	}
	if findSymbol(r, "Shop::Items::Widget") == nil {
		t.Fatal("expected subclass symbol Shop::Items::Widget to be recorded")
	}

	// The gap: inherits target is the bare "Base", which cannot exact-match the
	// recorded "Shop::Base".
	if findEdge(r, "Shop::Items::Widget", "Base", string(model.EdgeInherits)) == nil {
		t.Fatal(`expected inherits edge with bare target "Base" (the namespaced-inheritance gap)`)
	}
	// And it was NOT qualified to the resolvable name. If this ever fires, the
	// extractor began qualifying ancestors — flip this test to assert resolution.
	if findEdge(r, "Shop::Items::Widget", "Shop::Base", string(model.EdgeInherits)) != nil {
		t.Error("extractor unexpectedly qualified the ancestor to Shop::Base — gap may be fixed; update this test")
	}
}

// TestQualifiedSuperclassEmitsResolvableName is the contrast: the same class
// written with a fully-qualified ancestor emits the full path, which exact-
// matches and resolves. This is why `< Spree::Base` resolves while `< Base`
// does not.
func TestQualifiedSuperclassEmitsResolvableName(t *testing.T) {
	src := `module Shop
  class Base
  end
  module Items
    class Widget < Shop::Base
    end
  end
end`
	r := parseRuby(t, src)
	if findEdge(r, "Shop::Items::Widget", "Shop::Base", string(model.EdgeInherits)) == nil {
		t.Fatal(`expected inherits edge with fully-qualified target "Shop::Base"`)
	}
}
