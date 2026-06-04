package tsjs

// frameworks_test.go holds the Stimulus-controller extraction tests,
// matching the production split in frameworks.go.

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

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
		"SearchController":         false,
		"Admin::ResultsController": false,
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

func TestStimulusClassWithTargets(t *testing.T) {
	// Use JavaScript extractor: TypeScript grammar parses static fields as
	// "public_field_definition" which walkClassBody does not handle yet.
	r := parseJS(t, `import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static targets = ["output", "input"];

  connect() {}
}
`, "app/javascript/controllers/hello_controller.js")
	s := findSym(r, "HelloController")
	if s == nil {
		t.Fatal("missing symbol HelloController from stimulus")
	}
	// Check targets are emitted
	foundTarget := false
	for _, s := range r.symbols {
		if s.Qualified == "HelloController.target:output" || s.Qualified == "HelloController.target:input" {
			foundTarget = true
		}
	}
	if !foundTarget {
		t.Error("missing stimulus target symbols")
	}
}

func TestStimulusClassWithOutlets(t *testing.T) {
	// Use JavaScript extractor: TypeScript grammar parses static fields as
	// "public_field_definition" which walkClassBody does not handle yet.
	r := parseJS(t, `import { Controller } from "@hotwired/stimulus"

export default class extends Controller {
  static outlets = ["search"];
}
`, "app/javascript/controllers/filter_controller.js")
	s := findSym(r, "FilterController")
	if s == nil {
		t.Fatal("missing symbol FilterController from stimulus")
	}
	// Outlets emit calls edges
	foundOutlet := false
	for _, e := range r.edges {
		if string(e.Kind) == "calls" && e.TargetQualified == "SearchController" {
			foundOutlet = true
		}
	}
	if !foundOutlet {
		t.Error("missing calls edge to SearchController from outlet")
	}
}
