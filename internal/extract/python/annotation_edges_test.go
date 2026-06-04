package python

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// TestAnnotationNilTypeNode covers the nil-guard at the top of
// emitTypeAnnotationEdge: callers that recurse into an absent type node
// (an empty `type` wrapper or a malformed parameter) must be a no-op.
func TestAnnotationNilTypeNode(t *testing.T) {
	rec := &recorder{}
	w := &walker{source: []byte(""), emit: rec, pkgBindings: map[string]string{}}
	if err := w.emitTypeAnnotationEdge(nil, "Owner", 1); err != nil {
		t.Fatalf("emitTypeAnnotationEdge(nil): %v", err)
	}
	if len(rec.edges) != 0 {
		t.Errorf("expected no edges for nil type node, got %d", len(rec.edges))
	}
	var _ extract.Emitter = rec
}

// These tests exercise type-annotation edge cases: annotations whose kind
// produces no composes edge (forward-reference strings, lowercase names),
// generic outer names that are neither known wrappers nor classes, and
// error propagation through nested type-parameter walking.

func TestAnnotationForwardRefStringSkipped(t *testing.T) {
	// A string forward-reference annotation (`x: "Order"`) is not an
	// identifier/generic/attribute node, so no composes edge is emitted.
	r := parse(t, `class Holder:
    ref: "Order"
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Holder" {
			t.Errorf("unexpected composes edge for forward-ref string: %v", e.TargetQualified)
		}
	}
}

func TestAnnotationLowercaseIdentifierSkipped(t *testing.T) {
	// A non-PascalCase, non-primitive identifier annotation is treated as a
	// non-class name and skipped.
	r := parse(t, `class Holder:
    handler: callback
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Holder" {
			t.Errorf("unexpected composes edge for lowercase annotation: %v", e.TargetQualified)
		}
	}
}

func TestAnnotationGenericLowercaseOuterSkipped(t *testing.T) {
	// `x: container[Order]` — the outer name is lowercase and not a known
	// wrapper, so emitGenericAnnotation emits no edge for the outer and does
	// not unwrap the parameters.
	r := parse(t, `class Holder:
    box: container[Order]
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Holder" {
			t.Errorf("unexpected composes edge for lowercase generic outer: %v", e.TargetQualified)
		}
	}
}

func TestAnnotationGenericCustomOuterComposes(t *testing.T) {
	// A PascalCase generic outer that is not a known wrapper composes to the
	// outer type itself.
	r := parse(t, `class Service:
    repo: Repository[Order]
`)
	if findEdge(r, "Service", "Repository", "composes") == nil {
		t.Error("missing composes edge Service -> Repository for custom generic outer")
	}
}

func TestAnnotationTypeWrapperUnwrap(t *testing.T) {
	// `x: type[Order]` — the `type` wrapper recurses into its inner type.
	r := parse(t, `class Factory:
    target: Type[Order]
`)
	if findEdge(r, "Factory", "Order", "composes") == nil {
		t.Error("missing composes edge Factory -> Order through Type[...] wrapper")
	}
}

func TestAnnotationNestedGenericEdgeError(t *testing.T) {
	// A failing emitter propagates out of emitTypeParamEdges when walking the
	// inner type parameters of a wrapper annotation.
	err := parseWithEmitter(t, `class Holder:
    items: List[Order]
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on nested generic annotation edge")
	}
}
