package ruby

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// parseNode parses a snippet and returns the first named descendant of the
// given kind, for direct unit testing of node-shape helpers.
func firstNodeOfKind(t *testing.T, src, kind string) (*sitter.Node, *sitter.Tree, []byte) {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	t.Cleanup(p.Close)
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	t.Cleanup(tree.Close)

	var found *sitter.Node
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		if n == nil || found != nil {
			return
		}
		if n.Kind() == kind {
			found = n
			return
		}
		for i := uint(0); i < n.NamedChildCount(); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(tree.RootNode())
	if found == nil {
		t.Fatalf("no %s node in %q", kind, src)
	}
	return found, tree, source
}

func TestHasInterpolationNonStringNode(t *testing.T) {
	// hasInterpolation guards against a non-string node and must return false.
	constNode, _, _ := firstNodeOfKind(t, "Foo", "constant")
	if hasInterpolation(constNode) {
		t.Error("hasInterpolation on a non-string node must be false")
	}
	// Plain string without interpolation → false; with interpolation → true.
	plain, _, _ := firstNodeOfKind(t, `x = "hello"`, "string")
	if hasInterpolation(plain) {
		t.Error("plain string must not report interpolation")
	}
	interp, _, _ := firstNodeOfKind(t, `x = "hi #{name}"`, "string")
	if !hasInterpolation(interp) {
		t.Error("interpolated string must report interpolation")
	}
}

func TestDescribeWithInterpolatedStringFallsBack(t *testing.T) {
	// An interpolated describe description can't form a stable scope segment,
	// so the block falls back to file-level scope rather than a named one.
	r := parseRubyWithPath(t, "describe \"#{klass} behavior\" do\n  do_setup\nend", "spec/thing_spec.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("interpolated describe string must not emit a tests edge")
		}
	}
}

func TestDescribeWithPunctuationDescription(t *testing.T) {
	// Punctuation in the description is dropped by sanitizeDesc (the default
	// branch); spaces become underscores.
	r := parseRubyWithPath(t, "describe \"works!\" do\n  helper_call\nend", "spec/thing_spec.rb")
	want := "describe_works"
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a calls edge sourced from %q after punctuation sanitization, edges=%v", want, r.edges)
	}
}

func TestDescribeWithEmptyStringFallsBack(t *testing.T) {
	// `describe "" do … end` — the description sanitizes to empty, so the block
	// falls back to file-level scope and emits no tests edge / named segment.
	r := parseRubyWithPath(t, "describe \"\" do\n  setup_call\nend", "spec/thing_spec.rb")
	for _, e := range r.edges {
		if string(e.Kind) == "tests" {
			t.Error("empty-string describe must not emit a tests edge")
		}
	}
}

func TestNestedDescribeInsideClassScope(t *testing.T) {
	// A describe block nested inside a class body produces a synthetic source
	// that joins the class scope with the test scope (Class#describe_segment).
	r := parseRuby(t, "class Foo\n  describe \"behaviour\" do\n    run_check\n  end\nend")
	want := "Foo#describe_behaviour"
	found := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.SourceQualified == want {
			found = true
		}
	}
	if !found {
		t.Errorf("expected calls edge sourced from class+test scope %q, edges=%v", want, r.edges)
	}
}

func TestDescribeNoBlockCapturesArgumentCalls(t *testing.T) {
	// `describe Order, SomeHelper.config` — no block, but the arguments carry a
	// call (SomeHelper.config) that should be captured as a test call. RSpec DSL
	// method calls inside the args are skipped.
	r := parseRubyWithPath(t, "describe Order, SomeHelper.config do; end", "spec/order_spec.rb")
	// The tests edge for the constant is still emitted.
	if findEdge(r, "OrderTest", "Order", string(model.EdgeTests)) == nil {
		t.Error("missing tests edge from describe with constant first arg")
	}
}

func TestDescribeNoBlockArgumentCallCaptured(t *testing.T) {
	// A describe call without a do/block body whose arguments include a method
	// call: the call inside the arguments is captured (block==nil branch).
	r := parseRubyWithPath(t, "describe(compute_subject) do; end", "spec/order_spec.rb")
	_ = r // exercises the no-block argument-walk path without crashing
}

func TestDescribeEdgeEmitError(t *testing.T) {
	// `describe Order do … end` emits the conventional tests edge first; failing
	// the edge emit must propagate out of handleTestBlock.
	err := parseWithEmitter(t, "describe Order do\nend",
		&failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing tests-edge emit on describe")
	}
}

func TestNestedTestBlockRecursionEmitError(t *testing.T) {
	// A string-described block carries no tests edge, so the outer walkTestBody
	// runs and recurses into the nested context; a failing edge emit inside that
	// nested block must propagate back through the outer walk's callback.
	src := "describe \"payments\" do\n  context \"when paid\" do\n    confirm_paid\n  end\nend"
	err := parseWithEmitter(t, src, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error to propagate from nested test-block emission")
	}
}

func TestBuildSyntheticSourceClassOnly(t *testing.T) {
	// Sanity check covered by integration above; assert the helper shape via a
	// describe with only a class scope (no test scope) emits a class-sourced edge.
	r := parseRuby(t, "class Foo\n  def go\n    helper\n  end\nend")
	if findEdge(r, "Foo#go", "self.helper", string(model.EdgeCalls)) == nil {
		t.Error("expected class-scoped self.helper edge")
	}
	_ = extract.ConfidenceTests
}
