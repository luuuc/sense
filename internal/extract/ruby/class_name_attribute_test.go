package ruby

import (
	"testing"

	"errors"

	"github.com/luuuc/sense/internal/extract"
)

// The class_name_attribute macro (solidus/Spree) declares a reconfigurable
// class accessor whose default names a class via a string literal. The
// extractor indexes the accessor as a symbol and, for a static string default
// that parses as a constant path, emits a calls edge to that constant so the
// config-string applier becomes reachable (the seam grep cannot follow).

func TestClassNameAttributeEmitsAccessorAndEdge(t *testing.T) {
	r := parseRubyWithPath(t, `module SolidusLegacyPromotions
  class Configuration < Spree::Preferences::Configuration
    class_name_attribute :order_adjuster_class, default: "Spree::Promotion::OrderAdjustmentsRecalculator"
  end
end
`, "configuration.rb")

	accessor := "SolidusLegacyPromotions::Configuration.order_adjuster_class"
	sym := findSymbol(r, accessor)
	if sym == nil {
		t.Fatalf("missing accessor symbol %q", accessor)
	}
	if sym.Name != "order_adjuster_class" {
		t.Errorf("accessor Name = %q, want order_adjuster_class", sym.Name)
	}
	if sym.ParentQualified != "SolidusLegacyPromotions::Configuration" {
		t.Errorf("accessor ParentQualified = %q", sym.ParentQualified)
	}

	edge := findEdge(r, accessor, "Spree::Promotion::OrderAdjustmentsRecalculator", "calls")
	if edge == nil {
		t.Fatal("missing config-string calls edge to the applier class")
	}
	if edge.Confidence != extract.ConfidenceConvention {
		t.Errorf("edge confidence = %v, want %v (convention)", edge.Confidence, extract.ConfidenceConvention)
	}
}

// The reopened-namespace form (`module Spree; class AppConfiguration`) must
// compose the full accessor path and resolve the default just like the nested
// form — this is the actual app_configuration.rb shape.
func TestClassNameAttributeReopenedNamespace(t *testing.T) {
	r := parseRubyWithPath(t, `module Spree
  class AppConfiguration
    class_name_attribute :order_recalculator_class, default: "Spree::OrderUpdater"
  end
end
`, "app_configuration.rb")

	accessor := "Spree::AppConfiguration.order_recalculator_class"
	if findSymbol(r, accessor) == nil {
		t.Fatalf("missing accessor symbol %q", accessor)
	}
	if findEdge(r, accessor, "Spree::OrderUpdater", "calls") == nil {
		t.Error("missing config-string calls edge Spree::AppConfiguration.order_recalculator_class -> Spree::OrderUpdater")
	}
}

// Leading `::` (absolute constant path) is trimmed and still resolves.
func TestClassNameAttributeAbsolutePath(t *testing.T) {
	r := parseRuby(t, `class C
  class_name_attribute :impl, default: "::Foo::Bar"
end
`)
	if findEdge(r, "C.impl", "Foo::Bar", "calls") == nil {
		t.Error("absolute-path default ::Foo::Bar should resolve to Foo::Bar")
	}
}

// Negative cases: the accessor symbol is always emitted (so `graph <name>`
// resolves), but NO edge is emitted when the default is absent, dynamic, a
// lambda, or not a constant path. A wrong high-confidence edge misleads worse
// than a missing one.
func TestClassNameAttributeNoEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		decl string
	}{
		{"no default", `class_name_attribute :impl`},
		{"interpolated default", "class_name_attribute :impl, default: \"Spree::#{flavor}\""},
		{"lambda default", `class_name_attribute :impl, default: -> { resolve_class }`},
		{"constant default (not a string)", `class_name_attribute :impl, default: Spree::OrderUpdater`},
		{"non-constant string", `class_name_attribute :impl, default: "not a class name"`},
		{"lowercase string", `class_name_attribute :impl, default: "spree/order_updater"`},
		{"empty string", `class_name_attribute :impl, default: ""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := parseRuby(t, "class C\n  "+tc.decl+"\nend\n")
			if findSymbol(r, "C.impl") == nil {
				t.Error("accessor symbol C.impl should still be emitted")
			}
			for _, e := range r.edges {
				if e.SourceQualified == "C.impl" && string(e.Kind) == "calls" {
					t.Errorf("expected no config-string edge, got %s -> %s", e.SourceQualified, e.TargetQualified)
				}
			}
		})
	}
}

// A non-symbol first argument is not a valid accessor declaration — emit
// nothing rather than a malformed symbol.
func TestClassNameAttributeNonSymbolArg(t *testing.T) {
	r := parseRuby(t, `class C
  class_name_attribute "impl", default: "Spree::OrderUpdater"
end
`)
	for _, s := range r.symbols {
		if s.Name == "impl" {
			t.Error("should not emit accessor symbol for non-symbol first argument")
		}
	}
}

// Documented limitation: only the first accessor symbol is handled. solidus
// declares one accessor per call, so a multi-symbol form is out of scope — the
// second name yields no symbol and the default binds to the first only. This
// test pins that contract so a future change is a deliberate decision.
func TestClassNameAttributeOnlyFirstSymbol(t *testing.T) {
	r := parseRuby(t, `class C
  class_name_attribute :a, :b, default: "Spree::OrderUpdater"
end
`)
	if findSymbol(r, "C.a") == nil {
		t.Error("first accessor C.a should be emitted")
	}
	if findSymbol(r, "C.b") != nil {
		t.Error("second accessor C.b is out of scope and should not be emitted")
	}
	if findEdge(r, "C.a", "Spree::OrderUpdater", "calls") == nil {
		t.Error("default should bind to the first accessor C.a")
	}
}

// A bare `class_name_attribute` with no arguments is malformed — emit nothing.
func TestClassNameAttributeNoArgs(t *testing.T) {
	r := parseRuby(t, `class C
  class_name_attribute
end
`)
	for _, e := range r.edges {
		if e.SourceQualified == "C.impl" || string(e.Kind) == "calls" && e.SourceQualified == "C" {
			t.Errorf("should not emit edge for argument-less class_name_attribute, got %s -> %s", e.SourceQualified, e.TargetQualified)
		}
	}
	for _, s := range r.symbols {
		if s.ParentQualified == "C" && s.Kind == "method" {
			t.Errorf("should not emit accessor symbol for argument-less class_name_attribute, got %q", s.Qualified)
		}
	}
}

// Empty-parens `class_name_attribute()` parses as a call with an empty
// argument list — emit nothing.
func TestClassNameAttributeEmptyArgs(t *testing.T) {
	r := parseRuby(t, `class C
  class_name_attribute()
end
`)
	for _, s := range r.symbols {
		if s.ParentQualified == "C" && s.Kind == "method" {
			t.Errorf("should not emit accessor symbol for empty-args class_name_attribute, got %q", s.Qualified)
		}
	}
}

// The accessor-symbol and config-string-edge emits must propagate emitter
// errors. `class C` emits the class (Symbol #1), then the accessor (Symbol #2)
// and its calls edge (Edge #1).
func TestClassNameAttributeEmitterErrorsPropagate(t *testing.T) {
	src := "class C\n  class_name_attribute :impl, default: \"Spree::OrderUpdater\"\nend\n"
	cases := []struct {
		name string
		emit *errEmit
	}{
		{"accessor symbol", &errEmit{failSymbolAt: 2}},
		{"config-string edge", &errEmit{failEdgeAt: 1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := runWithEmitter(t, src, c.emit); !errors.Is(err, errBoom) {
				t.Errorf("expected errBoom to propagate, got %v", err)
			}
		})
	}
}

func TestLooksLikeConstantPath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"Spree::Promotion::OrderAdjustmentsRecalculator", true},
		{"Foo", true},
		{"Foo::Bar2", true},
		{"My_Class", true},
		{"", false},
		{"foo", false},
		{"Foo::bar", false},
		{"Foo::", false},
		{"::Foo", false}, // caller trims the leading "::" before this check
		{"2Foo", false},
		{"Foo Bar", false},
		{"spree/order_updater", false},
	}
	for _, tc := range cases {
		if got := looksLikeConstantPath(tc.in); got != tc.want {
			t.Errorf("looksLikeConstantPath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
