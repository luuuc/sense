package python

import "testing"

// These tests exercise assignment and call shapes the walker must handle
// without emitting spurious symbols: tuple-target assignments (non-identifier
// LHS), getattr with an empty string literal, and Depends-edge propagation.

func TestModuleTupleAssignmentNoConstant(t *testing.T) {
	// `A, B = 1, 2` has a pattern_list LHS, not an identifier, so neither the
	// pre-scan nor handleAssignment emits a constant symbol.
	r := parse(t, `A, B = 1, 2
`)
	if findSymbol(r, "A") != nil {
		t.Error("tuple-target A should not be emitted as a constant")
	}
	if findSymbol(r, "B") != nil {
		t.Error("tuple-target B should not be emitted as a constant")
	}
}

func TestClassTupleAssignmentNoConstant(t *testing.T) {
	// A tuple assignment inside a class body likewise emits no constant.
	r := parse(t, `class Config:
    X, Y = 1, 2
`)
	if findSymbol(r, "Config.X") != nil {
		t.Error("class tuple-target X should not be emitted as a constant")
	}
	if findSymbol(r, "Config.Y") != nil {
		t.Error("class tuple-target Y should not be emitted as a constant")
	}
}

func TestGetattrEmptyStringLiteral(t *testing.T) {
	// `getattr(obj, "")` is a string literal with no string_content child, so
	// literalGetattrTarget returns ("", false) and no calls edge is emitted.
	r := parse(t, `def process():
    getattr(obj, "")
`)
	for _, e := range r.edges {
		if e.SourceQualified == "process" && string(e.Kind) == "calls" && e.TargetQualified == "" {
			t.Error("unexpected empty-target calls edge from getattr with empty string")
		}
	}
}

func TestDependsEdgeError(t *testing.T) {
	// A failing emitter propagates the error out of emitDependsEdges for a
	// decorated route whose parameter uses Depends().
	err := parseWithEmitter(t, `@app.get("/items")
def list_items(db = Depends(get_db)):
    pass
`, &failAfterN{symbolsLeft: 100, edgesLeft: 1})
	if err == nil {
		t.Error("expected error from failing emitter on Depends edge")
	}
}
