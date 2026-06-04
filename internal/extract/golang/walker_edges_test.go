package golang

import "testing"

// These tests cover declaration-walking shapes that the two-pass walker
// handles specially: comments inside grouped declarations, blank-identifier
// vars, type-alias error propagation, and constant-reference skip rules for
// call targets and selector operands.

func TestConstGroupWithInterleavedComment(t *testing.T) {
	// A comment child precedes the const_spec inside the parentheses; the
	// walker skips the non-const_spec child and still emits A.
	r := parse(t, `package cfg

const (
	// Retry budget.
	A = 1
)
`)
	if findSymbol(r, "cfg.A") == nil {
		t.Error("missing symbol cfg.A from commented const group")
	}
}

func TestVarBlankIdentifierNotEmitted(t *testing.T) {
	// `var _ = …` is a blank declaration, not a named symbol.
	r := parse(t, `package svc

var _ = "unused"
var Real = "kept"
`)
	if findSymbol(r, "svc._") != nil {
		t.Error("blank var _ should not be emitted as a symbol")
	}
	if findSymbol(r, "svc.Real") == nil {
		t.Error("missing symbol svc.Real")
	}
}

func TestVarSymbolEmitError(t *testing.T) {
	err := parseWithEmitter(t, `package main
var localhostIP = "127.0.0.1"
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on var symbol emit")
	}
}

func TestTypeAliasSymbolEmitError(t *testing.T) {
	// The type_alias branch of handleTypeDeclaration propagates emit errors.
	err := parseWithEmitter(t, `package main
type Celsius = float64
`, &failAfterN{symbolsLeft: 0, edgesLeft: 100})
	if err == nil {
		t.Error("expected error on type alias symbol emit")
	}
}

func TestConstReferenceSkipsBareCallTarget(t *testing.T) {
	// A package-level binding used as a bare call target (`Handler()`) is a
	// call, not a reference — no references edge for the call-site identifier.
	r := parse(t, `package svc

var Handler = func() {}

func Run() {
	Handler()
	h := Handler
	_ = h
}
`)
	count := 0
	for _, e := range r.edges {
		if e.Kind == "references" && e.TargetQualified == "svc.Handler" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 references edge to svc.Handler (value read only), got %d", count)
	}
}

func TestConstReferenceSkipsSelectorObject(t *testing.T) {
	// A package-level binding used as the object of a selector
	// (`Registry.Lookup()`) is skipped: selector operands are not plain
	// value references.
	r := parse(t, `package svc

var Registry = newRegistry()

func Run() {
	Registry.Lookup("x")
}
`)
	if findEdge(r, "svc.Run", "svc.Registry", "references") != nil {
		t.Error("should not emit references edge for selector-object binding")
	}
}

func TestSelectorCallUnresolvedKeepsSurfaceText(t *testing.T) {
	// A selector callee on an unknown receiver keeps the written surface text.
	r := parse(t, `package svc

func Run() {
	log.Printf("hi")
}
`)
	if findEdge(r, "svc.Run", "log.Printf", "calls") == nil {
		t.Error("missing calls edge svc.Run -> log.Printf")
	}
}
