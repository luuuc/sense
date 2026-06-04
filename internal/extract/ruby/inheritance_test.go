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
