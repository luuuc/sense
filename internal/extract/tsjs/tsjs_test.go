package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// TestSmokeExtract proves each of the three registered extractors
// (TypeScript / TSX / JavaScript) parses a trivial snippet and emits at
// least one symbol without going through the fixture harness.
func TestSmokeExtract(t *testing.T) {
	cases := []struct {
		name string
		ex   extract.Extractor
		src  string
	}{
		{"typescript", TypeScript{}, "export class Foo {}\n"},
		{"tsx", TSX{}, "export const X = <div/>;\n"},
		{"javascript", JavaScript{}, "export class Foo {}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sitter.NewParser()
			defer p.Close()
			if err := p.SetLanguage(tc.ex.Grammar()); err != nil {
				t.Fatalf("SetLanguage: %v", err)
			}
			src := []byte(tc.src)
			tree := p.Parse(src, nil)
			if tree == nil {
				t.Fatal("Parse returned nil tree")
			}
			defer tree.Close()

			c := &counter{}
			if err := tc.ex.Extract(tree, src, "smoke", c); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if c.symbols == 0 {
				t.Errorf("%s emitted 0 symbols; expected ≥1", tc.name)
			}
		})
	}
}

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }

type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error { r.symbols = append(r.symbols, s); return nil }
func (r *recorder) Edge(e extract.EmittedEdge) error     { r.edges = append(r.edges, e); return nil }

func TestInferStimulusController(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"app/javascript/controllers/checkout_controller.js", "CheckoutController"},
		{"app/javascript/controllers/user_profile_controller.ts", "UserProfileController"},
		{"app/javascript/controllers/admin/users_controller.js", "Admin::UsersController"},
		{"app/javascript/controllers/admin/user_profile_controller.ts", "Admin::UserProfileController"},
		{"app/javascript/controllers/checkout_controller.mjs", "CheckoutController"},
		// Non-matches
		{"app/javascript/application.js", ""},
		{"app/javascript/controllers/checkout.js", ""},
		{"app/models/user.rb", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := inferStimulusController(tt.path)
		if got != tt.want {
			t.Errorf("inferStimulusController(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestStimulusAnonymousClass(t *testing.T) {
	src := []byte(`import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["output"]

  greet() {
    this.outputTarget.textContent = "Hello"
  }
}
`)
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/javascript/controllers/hello_controller.js", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var foundClass bool
	var foundMethod bool
	for _, s := range r.symbols {
		if s.Qualified == "HelloController" && s.Kind == "class" {
			foundClass = true
		}
		if s.Qualified == "HelloController.greet" && s.Kind == "method" {
			foundMethod = true
		}
	}
	if !foundClass {
		t.Error("expected symbol HelloController (class) from anonymous Stimulus controller")
	}
	if !foundMethod {
		t.Error("expected symbol HelloController.greet (method)")
	}
}

func TestStimulusNamedClass(t *testing.T) {
	src := []byte(`import { Controller } from "@hotwired/stimulus"

export default class CheckoutController extends Controller {
  submit() {}
}
`)
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/javascript/controllers/checkout_controller.js", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Named class should still be extracted with its actual name
	var foundClass bool
	for _, s := range r.symbols {
		if s.Qualified == "CheckoutController" && s.Kind == "class" {
			foundClass = true
		}
	}
	if !foundClass {
		t.Error("expected symbol CheckoutController from named Stimulus controller")
	}
}

func TestStimulusTargetsAndOutlets(t *testing.T) {
	src := []byte(`import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["output", "name"]
  static outlets = ["search", "admin--results"]

  greet() {}
}
`)
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/javascript/controllers/hello_controller.js", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Check target symbols
	wantTargets := map[string]bool{
		"HelloController.target:output": false,
		"HelloController.target:name":   false,
	}
	for _, s := range r.symbols {
		if _, ok := wantTargets[s.Qualified]; ok {
			wantTargets[s.Qualified] = true
		}
	}
	for q, found := range wantTargets {
		if !found {
			t.Errorf("missing target symbol %q", q)
		}
	}

	// Check outlet edges
	wantOutlets := map[string]bool{
		"SearchController":          false,
		"Admin::ResultsController":  false,
	}
	for _, e := range r.edges {
		if _, ok := wantOutlets[e.TargetQualified]; ok {
			wantOutlets[e.TargetQualified] = true
		}
	}
	for q, found := range wantOutlets {
		if !found {
			t.Errorf("missing outlet edge to %q", q)
		}
	}
}

func TestDefaultExportNaming(t *testing.T) {
	src := []byte(`export default class extends Base {}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := JavaScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/javascript/utils/helper.js", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "Helper" && s.Kind == "class" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol Helper (class) from anonymous default export named after file")
	}

	// Inherits edge should still be emitted.
	var hasEdge bool
	for _, e := range r.edges {
		if e.SourceQualified == "Helper" && e.TargetQualified == "Base" && e.Kind == "inherits" {
			hasEdge = true
		}
	}
	if !hasEdge {
		t.Error("expected inherits edge Helper → Base")
	}
}

func TestDefaultExportFunctionNaming(t *testing.T) {
	src := []byte(`export default function({ user }) {
  return process(user)
}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/UserProfile.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "UserProfile" && s.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol UserProfile (function) from anonymous default export")
	}

	var hasCall bool
	for _, e := range r.edges {
		if e.SourceQualified == "UserProfile" && e.TargetQualified == "process" && e.Kind == "calls" {
			hasCall = true
		}
	}
	if !hasCall {
		t.Error("expected calls edge UserProfile → process")
	}
}

func TestDefaultExportArrowFunctionNaming(t *testing.T) {
	src := []byte(`export default ({ items }) => {
  return items.map(format)
}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TSX{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/OrderSummary.tsx", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	var found bool
	for _, s := range r.symbols {
		if s.Qualified == "OrderSummary" && s.Kind == "function" {
			found = true
		}
	}
	if !found {
		t.Error("expected symbol OrderSummary (function) from arrow function default export")
	}
}

func TestDefaultExportIndexFileSkipped(t *testing.T) {
	src := []byte(`export default function() {}`)
	p := sitter.NewParser()
	defer p.Close()
	ex := TypeScript{}
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	r := &recorder{}
	if err := ex.Extract(tree, src, "app/components/index.ts", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	if len(r.symbols) != 0 {
		t.Errorf("expected 0 symbols for anonymous default export in index file, got %d", len(r.symbols))
	}
}
