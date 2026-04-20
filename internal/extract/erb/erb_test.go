package erb

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(_ extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(_ extract.EmittedEdge) error     { c.edges++; return nil }

type recorder struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (r *recorder) Symbol(s extract.EmittedSymbol) error { r.symbols = append(r.symbols, s); return nil }
func (r *recorder) Edge(e extract.EmittedEdge) error     { r.edges = append(r.edges, e); return nil }

func TestSmokeExtract(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="checkout">
  <button data-action="click->checkout#submit">Pay</button>
  <span data-checkout-target="total"></span>
</div>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "app/views/orders/show.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) < 3 {
		t.Fatalf("expected at least 3 edges (controller, action, target), got %d", len(r.edges))
	}

	// Assert actual edge content
	wantTargets := map[string]bool{
		"CheckoutController":              false,
		"CheckoutController.submit":       false,
		"CheckoutController.target:total": false,
	}
	for _, e := range r.edges {
		if _, ok := wantTargets[e.TargetQualified]; ok {
			wantTargets[e.TargetQualified] = true
		}
	}
	for target, found := range wantTargets {
		if !found {
			t.Errorf("missing expected edge target %q", target)
		}
	}
}

func TestStimulusNaming(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"checkout", "CheckoutController"},
		{"user-profile", "UserProfileController"},
		{"admin--users", "Admin::UsersController"},
		{"admin--user-profile", "Admin::UserProfileController"},
	}
	for _, tt := range tests {
		got := extract.StimulusControllerQualified(tt.input)
		if got != tt.want {
			t.Errorf("extract.StimulusControllerQualified(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTurboFrameSymbol(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<%= turbo_frame_tag "cart" do %>
  <div>content</div>
<% end %>
<turbo-frame id="order_details">
</turbo-frame>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "app/views/orders/show.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.symbols) != 2 {
		t.Fatalf("expected 2 turbo frame symbols, got %d", len(r.symbols))
	}
	if r.symbols[0].Qualified != "turbo-frame:cart" {
		t.Errorf("first symbol qualified = %q, want %q", r.symbols[0].Qualified, "turbo-frame:cart")
	}
	if r.symbols[1].Qualified != "turbo-frame:order_details" {
		t.Errorf("second symbol qualified = %q, want %q", r.symbols[1].Qualified, "turbo-frame:order_details")
	}
}

func TestTurboStreamFrom(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<%= turbo_stream_from @store %>
<%= turbo_stream_from :orders %>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "app/views/stores/show.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 2 {
		t.Fatalf("expected 2 turbo stream edges, got %d", len(r.edges))
	}
	if r.edges[0].TargetQualified != "turbo-channel:store" {
		t.Errorf("first edge target = %q, want %q", r.edges[0].TargetQualified, "turbo-channel:store")
	}
	if r.edges[1].TargetQualified != "turbo-channel:orders" {
		t.Errorf("second edge target = %q, want %q", r.edges[1].TargetQualified, "turbo-channel:orders")
	}
}

func TestNegativeCases(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller-connected="true">
  <span data-value="not-a-controller"></span>
  <button data-action="not-a-valid-action">No match</button>
  <input data-target="bare-target-no-controller">
  <div data-random-attribute="something"></div>
  <div class="data-controller-lookalike"></div>
</div>`)

	c := &counter{}
	if err := ex.ExtractRaw(source, "app/views/fake.html.erb", c); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if c.edges != 0 {
		t.Errorf("expected 0 edges from non-Stimulus attributes, got %d", c.edges)
	}
	if c.symbols != 0 {
		t.Errorf("expected 0 symbols from non-Stimulus attributes, got %d", c.symbols)
	}
}

func TestMalformedActions(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<button data-action="checkout#">No method</button>
<button data-action="#">Empty</button>
<button data-action="">Empty string</button>
<button data-action="just-text-no-hash">No hash</button>`)

	c := &counter{}
	if err := ex.ExtractRaw(source, "app/views/fake.html.erb", c); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if c.edges != 0 {
		t.Errorf("expected 0 edges from malformed actions, got %d", c.edges)
	}
}

func TestRawExtractorInterface(t *testing.T) {
	var ex extract.Extractor = Extractor{}
	if _, ok := ex.(extract.RawExtractor); !ok {
		t.Error("Extractor does not implement RawExtractor")
	}
}

func TestFilePathAsSource(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="cart"></div>`)
	filePath := "app/views/orders/show.html.erb"

	r := &recorder{}
	if err := ex.ExtractRaw(source, filePath, r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(r.edges))
	}
	if r.edges[0].SourceQualified != filePath {
		t.Errorf("edge source = %q, want file path %q", r.edges[0].SourceQualified, filePath)
	}
}
