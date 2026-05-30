package ruby

import (
	"errors"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// These tests drive the real CST walker on source bytes — the walker is
// the code under test, so a hand-built EmittedSymbol would prove nothing.
// They pin the exact qualified name of a method defined inside a
// `Struct.new`/`Data.define`/`Class.new` block, plus the persistence-
// relevant facts: the constant is a class and the inherits edge points at
// the synthetic base.

func TestStructAttribution_WithBlock(t *testing.T) {
	// The maket repro: a nested Result struct with predicate methods.
	r := parseRuby(t, `class Checkout::ProcessPaymentService
  Result = Struct.new(:success, keyword_init: true) do
    def success?
      success
    end

    def failure?
      !success
    end
  end
end
`)

	// The methods must qualify to the struct, NOT the enclosing service.
	for _, name := range []string{"success?", "failure?"} {
		want := "Checkout::ProcessPaymentService::Result#" + name
		if s := findSymbol(r, want); s == nil {
			t.Errorf("missing method symbol %q", want)
		}
		// The mis-attribution bug: enclosing-class qualname must be gone.
		bad := "Checkout::ProcessPaymentService#" + name
		if s := findSymbol(r, bad); s != nil {
			t.Errorf("method still mis-attributed to enclosing class: %q", bad)
		}
	}

	// Result is a class, parented to the service.
	result := findSymbol(r, "Checkout::ProcessPaymentService::Result")
	if result == nil {
		t.Fatal("missing Result class symbol")
	}
	if result.Kind != "class" {
		t.Errorf("Result.Kind = %q, want class", result.Kind)
	}
	if result.ParentQualified != "Checkout::ProcessPaymentService" {
		t.Errorf("Result.Parent = %q, want Checkout::ProcessPaymentService", result.ParentQualified)
	}

	// The inherits edge points at the synthetic Struct base, and that
	// base symbol is emitted so the edge can resolve.
	e := findEdge(r, "Checkout::ProcessPaymentService::Result", extract.RubyCoreStruct, "inherits")
	if e == nil {
		t.Fatal("missing inherits edge Result -> ruby-core:Struct")
	}
	if base := findSymbol(r, extract.RubyCoreStruct); base == nil {
		t.Fatal("synthetic base symbol ruby-core:Struct not emitted")
	}
}

func TestStructAttribution_NoBlock(t *testing.T) {
	// maket's confirm_shop_payment_service.rb form: no block, accessors
	// generated implicitly. There are no `def`s to re-attribute, but the
	// constant must still be a value-object class with the inherits edge.
	r := parseRuby(t, `class ConfirmShopPaymentService
  Result = Struct.new(:success, :order, keyword_init: true)
end
`)
	result := findSymbol(r, "ConfirmShopPaymentService::Result")
	if result == nil {
		t.Fatal("missing Result class symbol")
	}
	if result.Kind != "class" {
		t.Errorf("Result.Kind = %q, want class", result.Kind)
	}
	if e := findEdge(r, "ConfirmShopPaymentService::Result", extract.RubyCoreStruct, "inherits"); e == nil {
		t.Error("missing inherits edge Result -> ruby-core:Struct")
	}
}

func TestDataDefineAttribution(t *testing.T) {
	r := parseRuby(t, `class Money
  Amount = Data.define(:cents) do
    def positive?
      cents > 0
    end
  end
end
`)
	if s := findSymbol(r, "Money::Amount#positive?"); s == nil {
		t.Error("missing method symbol Money::Amount#positive?")
	}
	amount := findSymbol(r, "Money::Amount")
	if amount == nil {
		t.Fatal("missing Amount class symbol")
	}
	if amount.Kind != "class" {
		t.Errorf("Amount.Kind = %q, want class", amount.Kind)
	}
	if e := findEdge(r, "Money::Amount", extract.RubyCoreData, "inherits"); e == nil {
		t.Error("missing inherits edge Amount -> ruby-core:Data")
	}
}

func TestClassNewAttribution(t *testing.T) {
	// Anonymous class assigned to a constant. The superclass is the
	// Class.new argument — NOT Struct — so it must NOT be a value object.
	r := parseRuby(t, `class Base
end

Widget = Class.new(Base) do
  def render
  end
end
`)
	if s := findSymbol(r, "Widget#render"); s == nil {
		t.Error("missing method symbol Widget#render")
	}
	widget := findSymbol(r, "Widget")
	if widget == nil {
		t.Fatal("missing Widget class symbol")
	}
	if widget.Kind != "class" {
		t.Errorf("Widget.Kind = %q, want class", widget.Kind)
	}
	// Inherits the real superclass, not a synthetic base.
	if e := findEdge(r, "Widget", "Base", "inherits"); e == nil {
		t.Error("missing inherits edge Widget -> Base")
	}
	// No synthetic base symbol — Class.new(Super) is not a value object.
	if s := findSymbol(r, extract.RubyCoreStruct); s != nil {
		t.Error("ruby-core:Struct emitted for a Class.new(Super) — should not be")
	}
}

func TestModuleNestedStructAttribution(t *testing.T) {
	// Scope push must compose with module/class nesting.
	r := parseRuby(t, `module Billing
  class Invoice
    Line = Struct.new(:amount) do
      def zero?
        amount.zero?
      end
    end
  end
end
`)
	want := "Billing::Invoice::Line#zero?"
	if s := findSymbol(r, want); s == nil {
		t.Errorf("missing method symbol %q", want)
	}
	if s := findSymbol(r, "Billing::Invoice::Line"); s == nil || s.Kind != "class" {
		t.Errorf("Line not emitted as a nested class")
	}
}

func TestStructAttribution_NegativeCase(t *testing.T) {
	// A def directly on the enclosing class (outside the struct block)
	// still qualifies to the class — the scope push is local to the block.
	r := parseRuby(t, `class Order
  Result = Struct.new(:ok) do
    def ok?
      ok
    end
  end

  def process
  end
end
`)
	if s := findSymbol(r, "Order#process"); s == nil {
		t.Error("def on the class must still qualify to the class: Order#process missing")
	}
	if s := findSymbol(r, "Order::Result#ok?"); s == nil {
		t.Error("def in the struct must qualify to the struct: Order::Result#ok? missing")
	}
	// And the struct method is NOT on the class.
	if s := findSymbol(r, "Order#ok?"); s != nil {
		t.Error("struct method leaked onto the enclosing class: Order#ok?")
	}
}

// Non-builder RHS forms fall through to plain-constant handling rather
// than being mistaken for a class. These cover the detection guards.
func TestClassBuilderDetection_NonBuilders(t *testing.T) {
	cases := []struct {
		name, src, qualified string
	}{
		// Receiverless call (`compute(...)`) — not a `Recv.method` builder.
		{"receiverless call", "X = compute(1)\n", "X"},
		// A `Recv.method` that is not a recognised builder.
		{"unrelated dotted call", "X = Foo.bar(1)\n", "X"},
		// A plain literal — not a call at all.
		{"plain literal", "X = 42\n", "X"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parseRuby(t, c.src)
			s := findSymbol(r, c.qualified)
			if s == nil {
				t.Fatalf("missing symbol %q", c.qualified)
			}
			if s.Kind != "constant" {
				t.Errorf("%s: Kind = %q, want constant (not a class builder)", c.name, s.Kind)
			}
		})
	}
}

// Class.new with no constant superclass argument yields a class with no
// inherits edge — covering the "no args" and "non-constant first arg" arms
// of firstConstantArg.
func TestClassNew_NoKnownSuperclass(t *testing.T) {
	cases := []struct{ name, src string }{
		{"no arguments", "Widget = Class.new\n"},
		{"non-constant argument", "Widget = Class.new(factory) do\n  def go; end\nend\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := parseRuby(t, c.src)
			w := findSymbol(r, "Widget")
			if w == nil || w.Kind != "class" {
				t.Fatalf("%s: Widget not emitted as a class", c.name)
			}
			for _, e := range r.edges {
				if e.SourceQualified == "Widget" && e.Kind == "inherits" {
					t.Errorf("%s: unexpected inherits edge to %q", c.name, e.TargetQualified)
				}
			}
		})
	}
}

// errEmit is an Emitter that fails on a chosen Symbol or Edge call, so the
// extractor's error-propagation paths in handleClassBuilderAssignment are
// exercised (the recorder never errors).
type errEmit struct {
	failSymbolAt, failEdgeAt int
	nSym, nEdge              int
}

var errBoom = errors.New("boom")

func (e *errEmit) Symbol(extract.EmittedSymbol) error {
	e.nSym++
	if e.nSym == e.failSymbolAt {
		return errBoom
	}
	return nil
}
func (e *errEmit) Edge(extract.EmittedEdge) error {
	e.nEdge++
	if e.nEdge == e.failEdgeAt {
		return errBoom
	}
	return nil
}

func runWithEmitter(t *testing.T, src string, emit extract.Emitter) error {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	defer tree.Close()
	return ex.Extract(tree, source, "test.rb", emit)
}

func TestClassBuilderAssignment_EmitterErrorsPropagate(t *testing.T) {
	// Struct with a block: emits Result class (Symbol #1), synthetic base
	// (Symbol #2), the method inside the block (Symbol #3), and the inherits
	// edge (Edge #1). Each failure point must surface as an error.
	src := "Result = Struct.new(:a) do\n  def a?; a; end\nend\n"

	cases := []struct {
		name string
		emit *errEmit
	}{
		{"value-object class symbol", &errEmit{failSymbolAt: 1}},
		{"synthetic base symbol", &errEmit{failSymbolAt: 2}},
		{"block member symbol", &errEmit{failSymbolAt: 3}},
		{"inherits edge", &errEmit{failEdgeAt: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := runWithEmitter(t, src, c.emit); !errors.Is(err, errBoom) {
				t.Errorf("%s: err = %v, want errBoom", c.name, err)
			}
		})
	}
}

func TestSyntheticBase_SingleEmissionPerFile(t *testing.T) {
	// Two structs in one file share exactly one synthetic base symbol.
	r := parseRuby(t, `class A
  R1 = Struct.new(:x) do
    def x?; x; end
  end
  R2 = Struct.new(:y) do
    def y?; y; end
  end
end
`)
	count := 0
	for _, s := range r.symbols {
		if s.Qualified == extract.RubyCoreStruct {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ruby-core:Struct emitted %d times, want exactly 1", count)
	}
	// Both structs still carry their own inherits edge to the shared base.
	for _, src := range []string{"A::R1", "A::R2"} {
		if e := findEdge(r, src, extract.RubyCoreStruct, "inherits"); e == nil {
			t.Errorf("missing inherits edge %s -> ruby-core:Struct", src)
		}
	}
}
