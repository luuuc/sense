package dead

import "testing"

// These tests pin the pure symbol-classification predicates the arbiter and
// structural filters depend on. Each asserts a specific verdict for a specific
// shape — the branch table that decides whether a symbol is an entry point, a
// controller action, a value-object method, or a test — so a refactor that
// quietly broadens or narrows one of them fails here.

// TestIsTestSymbolBranches covers every recognised test-entry naming form plus
// a non-test control.
func TestIsTestSymbolBranches(t *testing.T) {
	cases := map[string]bool{
		"TestThing":   true,
		"test_thing":  true,
		"BenchmarkIt": true,
		"it":          true,
		"describe":    true,
		"specify":     true,
		"ordinary":    false,
	}
	for name, want := range cases {
		if got := isTestSymbol(Symbol{Name: name}); got != want {
			t.Errorf("isTestSymbol(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestRubyMethodParentNameBranches covers the no-separator, dotted, hashed,
// and namespaced forms.
func TestRubyMethodParentNameBranches(t *testing.T) {
	cases := map[string]string{
		"topLevel":                 "",
		"A.b":                      "A",
		"A#b":                      "A",
		"Checkout::Service#call":   "Service",
		"Deep::Nested::Klass.make": "Klass",
	}
	for q, want := range cases {
		if got := rubyMethodParentName(q); got != want {
			t.Errorf("rubyMethodParentName(%q) = %q, want %q", q, got, want)
		}
	}
}

// TestRubyInstanceMethodBranches distinguishes instance (#) from singleton (.)
// and top-level forms.
func TestRubyInstanceMethodBranches(t *testing.T) {
	if !rubyInstanceMethod("A#b") {
		t.Error("A#b should be an instance method")
	}
	if rubyInstanceMethod("A.b") {
		t.Error("A.b is a singleton method, not instance")
	}
	if rubyInstanceMethod("bare") {
		t.Error("bare name has no receiver separator")
	}
}

// TestIsValueObjectMethodBranches covers each guard: language, kind, nil
// parent, parent-not-in-set, singleton method, and the positive case.
func TestIsValueObjectMethodBranches(t *testing.T) {
	vo := map[int64]struct{}{7: {}}
	pid := int64(7)
	other := int64(8)

	if isValueObjectMethod(Symbol{Language: "go", Kind: "method", ParentID: &pid, Qualified: "A#b"}, vo) {
		t.Error("non-ruby should be false")
	}
	if isValueObjectMethod(Symbol{Language: "ruby", Kind: "class", ParentID: &pid, Qualified: "A"}, vo) {
		t.Error("non-method should be false")
	}
	if isValueObjectMethod(Symbol{Language: "ruby", Kind: "method", Qualified: "A#b"}, vo) {
		t.Error("nil parent should be false")
	}
	if isValueObjectMethod(Symbol{Language: "ruby", Kind: "method", ParentID: &other, Qualified: "A#b"}, vo) {
		t.Error("parent not in value-object set should be false")
	}
	if isValueObjectMethod(Symbol{Language: "ruby", Kind: "method", ParentID: &pid, Qualified: "A.b"}, vo) {
		t.Error("singleton method should be false")
	}
	if !isValueObjectMethod(Symbol{Language: "ruby", Kind: "method", ParentID: &pid, Qualified: "A#b"}, vo) {
		t.Error("instance method of a value object should be true")
	}
}

// TestIsRailsControllerActionBranches covers the language/kind guards, the
// visibility guard, the *Controller-parent path, the concern-module path, and
// the negative fallthrough.
func TestIsRailsControllerActionBranches(t *testing.T) {
	concerns := map[int64]struct{}{42: {}}
	pid := int64(42)
	otherPid := int64(99)

	if isRailsControllerAction(Symbol{Language: "go", Kind: "method"}, concerns) {
		t.Error("non-ruby should be false")
	}
	if isRailsControllerAction(Symbol{Language: "ruby", Kind: "class"}, concerns) {
		t.Error("non-method should be false")
	}
	if isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Visibility: "private", Qualified: "OrdersController#secret"}, concerns) {
		t.Error("private method should be false")
	}
	if !isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "OrdersController#index"}, concerns) {
		t.Error("public action on a *Controller should be true")
	}
	if !isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "Searchable#index", ParentID: &pid}, concerns) {
		t.Error("action on a controller concern module should be true")
	}
	if isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "Helper#noop", ParentID: &otherPid}, concerns) {
		t.Error("method on a non-controller, non-concern parent should be false")
	}
	if isRailsControllerAction(Symbol{Language: "ruby", Kind: "method", Qualified: "Helper#noop"}, concerns) {
		t.Error("method with no parent and non-Controller name should be false")
	}
}

// TestIsRailsControllerClassBranches covers the positive and each negative
// guard of the controller-class predicate.
func TestIsRailsControllerClassBranches(t *testing.T) {
	if !isRailsControllerClass(Symbol{Language: "ruby", Kind: "class", Name: "OrdersController"}) {
		t.Error("a Ruby *Controller class should be recognised")
	}
	if isRailsControllerClass(Symbol{Language: "ruby", Kind: "class", Name: "Order"}) {
		t.Error("a non-Controller class should not be recognised")
	}
	if isRailsControllerClass(Symbol{Language: "go", Kind: "class", Name: "OrdersController"}) {
		t.Error("a non-Ruby *Controller should not be recognised")
	}
	if isRailsControllerClass(Symbol{Language: "ruby", Kind: "method", Name: "OrdersController"}) {
		t.Error("a non-class should not be recognised")
	}
}

// TestEntryPointPredicates pins the small entry-point predicates: main, the
// constructor family, and the service-suffix matcher that keeps command
// objects' `call` open-world.
func TestEntryPointPredicates(t *testing.T) {
	if !isMainFunction(Symbol{Name: "main"}) || !isMainFunction(Symbol{Name: "Main"}) {
		t.Error("main/Main should be entry points")
	}
	if isMainFunction(Symbol{Name: "run"}) {
		t.Error("run is not main")
	}
	for _, n := range []string{"initialize", "__init__", "constructor", "init", "Init"} {
		if !isConstructor(Symbol{Name: n}) {
			t.Errorf("%q should be a constructor", n)
		}
	}
	if isConstructor(Symbol{Name: "build"}) {
		t.Error("build is not a constructor")
	}
	if !hasAnySuffix("PaymentService", serviceClassSuffixes) {
		t.Error("PaymentService should match a service suffix")
	}
	if hasAnySuffix("PaymentModel", serviceClassSuffixes) {
		t.Error("PaymentModel should not match a service suffix")
	}
}

// TestFrameworkNamesSorted pins the stable, sorted projection of the detected
// framework set used in the Result.
func TestFrameworkNamesSorted(t *testing.T) {
	names := frameworkNames(map[string]struct{}{"Rails": {}, "Sidekiq": {}})
	if len(names) != 2 || names[0] != "Rails" || names[1] != "Sidekiq" {
		t.Errorf("frameworkNames = %v, want sorted [Rails Sidekiq]", names)
	}
	if len(frameworkNames(nil)) != 0 {
		t.Error("frameworkNames(nil) should be empty")
	}
}
