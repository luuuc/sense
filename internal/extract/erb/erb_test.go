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

func (r *recorder) Symbol(s extract.EmittedSymbol) error {
	r.symbols = append(r.symbols, s)
	return nil
}
func (r *recorder) Edge(e extract.EmittedEdge) error { r.edges = append(r.edges, e); return nil }

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

func TestGrammarReturnsNil(t *testing.T) {
	ex := Extractor{}
	if ex.Grammar() != nil {
		t.Error("Grammar() should return nil for raw extractor")
	}
}

func TestTierReturnsBasic(t *testing.T) {
	ex := Extractor{}
	if ex.Tier() != extract.TierBasic {
		t.Errorf("Tier() = %v, want TierBasic", ex.Tier())
	}
}

func TestExtractDelegatesToExtractRaw(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="hello"></div>`)

	r := &recorder{}
	// Extract (tree-sitter interface) should delegate to ExtractRaw
	if err := ex.Extract(nil, source, "test.html.erb", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(r.edges) != 1 {
		t.Fatalf("expected 1 edge from Extract, got %d", len(r.edges))
	}
}

func TestOutletExtraction(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="search">
  <div data-search-results-outlet=".results"></div>
</div>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}

	// Should have controller edge + outlet edge
	// The outlet regex captures the middle segment between the owning controller
	// and "-outlet", so for "data-search-results-outlet" it captures "search-results"
	// which becomes SearchResultsController.
	foundOutlet := false
	for _, e := range r.edges {
		if e.TargetQualified == "SearchResultsController" {
			foundOutlet = true
		}
	}
	if !foundOutlet {
		targets := make([]string, len(r.edges))
		for i, e := range r.edges {
			targets[i] = e.TargetQualified
		}
		t.Errorf("missing outlet edge to SearchResultsController, got targets: %v", targets)
	}
}

func TestMultipleControllersOnElement(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="search filter sort"></div>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 3 {
		t.Fatalf("expected 3 controller edges, got %d", len(r.edges))
	}
}

func TestTurboStreamFromHTMLTag(t *testing.T) {
	ex := Extractor{}
	// The regex matches <turbo-stream-from ... signed-stream-name="X">
	source := []byte(`<turbo-stream-from channel="Turbo::StreamsChannel" signed-stream-name="orders">`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 1 {
		t.Fatalf("expected 1 edge from turbo-stream-from tag, got %d", len(r.edges))
	}
	if r.edges[0].TargetQualified != "turbo-channel:orders" {
		t.Errorf("edge target = %q, want %q", r.edges[0].TargetQualified, "turbo-channel:orders")
	}
}

func TestTurboStreamFromHTMLTagAndHelper(t *testing.T) {
	ex := Extractor{}
	// Both HTML tag and helper on separate lines
	source := []byte(`<turbo-stream-from channel="Turbo::StreamsChannel" signed-stream-name="products">
<%= turbo_stream_from :inventory %>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 2 {
		t.Fatalf("expected 2 edges, got %d", len(r.edges))
	}
	if r.edges[0].TargetQualified != "turbo-channel:products" {
		t.Errorf("first edge = %q, want turbo-channel:products", r.edges[0].TargetQualified)
	}
	if r.edges[1].TargetQualified != "turbo-channel:inventory" {
		t.Errorf("second edge = %q, want turbo-channel:inventory", r.edges[1].TargetQualified)
	}
}

func TestEmptySource(t *testing.T) {
	ex := Extractor{}
	r := &recorder{}
	if err := ex.ExtractRaw([]byte(""), "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw empty: %v", err)
	}
	if len(r.edges) != 0 || len(r.symbols) != 0 {
		t.Error("expected no output from empty source")
	}
}

func TestTurboFrameHTMLTagOnly(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<turbo-frame id="sidebar">
  <p>Content</p>
</turbo-frame>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(r.symbols))
	}
	if r.symbols[0].Qualified != "turbo-frame:sidebar" {
		t.Errorf("symbol = %q, want turbo-frame:sidebar", r.symbols[0].Qualified)
	}
}

func TestTurboFrameHelperOnly(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<%= turbo_frame_tag "notifications" do %>
  <ul></ul>
<% end %>`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(r.symbols))
	}
	if r.symbols[0].Qualified != "turbo-frame:notifications" {
		t.Errorf("symbol = %q, want turbo-frame:notifications", r.symbols[0].Qualified)
	}
}

func TestAllPatternsOnSameLine(t *testing.T) {
	// Exercises all extraction functions in walk on a single-line source
	ex := Extractor{}
	source := []byte(`<div data-controller="tabs" data-action="click->tabs#switch" data-tabs-target="panel" data-tabs-sidebar-outlet=".sidebar">`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	// controller + action + target + outlet = 4 edges
	if len(r.edges) < 4 {
		t.Errorf("expected at least 4 edges from combined attributes, got %d", len(r.edges))
	}
}

func TestTargetExtraction(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<span data-search-target="results"></span>
<input data-filter-target="input">`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) != 2 {
		t.Fatalf("expected 2 target edges, got %d", len(r.edges))
	}
	want := map[string]bool{
		"SearchController.target:results": false,
		"FilterController.target:input":   false,
	}
	for _, e := range r.edges {
		if _, ok := want[e.TargetQualified]; ok {
			want[e.TargetQualified] = true
		}
	}
	for target, found := range want {
		if !found {
			t.Errorf("missing target edge %q", target)
		}
	}
}

type failEdgeEmitter struct {
	counter
}

func (f *failEdgeEmitter) Edge(_ extract.EmittedEdge) error {
	return errForceFail
}

type failSymbolEmitter struct {
	counter
}

func (f *failSymbolEmitter) Symbol(_ extract.EmittedSymbol) error {
	return errForceFail
}

var errForceFail = &testError{"forced"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func TestControllerEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-controller="cart"></div>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on controller")
	}
}

func TestActionEmitError(t *testing.T) {
	ex := Extractor{}
	// No controller on this element, so extractControllers won't fire an edge.
	// But data-action will trigger extractActions.
	source := []byte(`<button data-action="click->checkout#submit">Pay</button>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on action")
	}
}

func TestTargetEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<span data-search-target="results"></span>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on target")
	}
}

func TestOutletEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<div data-search-results-outlet=".results"></div>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on outlet")
	}
}

func TestTurboFrameEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<turbo-frame id="cart"></turbo-frame>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failSymbolEmitter{})
	if err == nil {
		t.Error("expected error from failing symbol emitter on turbo frame tag")
	}
}

func TestTurboFrameHelperEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<%= turbo_frame_tag "cart" do %>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failSymbolEmitter{})
	if err == nil {
		t.Error("expected error from failing symbol emitter on turbo frame helper")
	}
}

func TestTurboStreamFromTagEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<turbo-stream-from channel="X" signed-stream-name="orders">`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on turbo stream from tag")
	}
}

func TestTurboStreamHelperEmitError(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<%= turbo_stream_from :orders %>`)
	err := ex.ExtractRaw(source, "test.html.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on turbo stream helper")
	}
}

func TestMultipleActionsOnElement(t *testing.T) {
	ex := Extractor{}
	source := []byte(`<input data-action="input->search#query focus->search#highlight">`)

	r := &recorder{}
	if err := ex.ExtractRaw(source, "test.html.erb", r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	if len(r.edges) < 2 {
		t.Fatalf("expected at least 2 action edges, got %d", len(r.edges))
	}
}

// --- template Ruby extraction (helpers, partials, i18n) ---

func (r *recorder) hasEdgeTo(target string) bool {
	for _, e := range r.edges {
		if e.TargetQualified == target {
			return true
		}
	}
	return false
}

func (r *recorder) hasSymbol(qualified string) bool {
	for _, s := range r.symbols {
		if s.Qualified == qualified {
			return true
		}
	}
	return false
}

func extractERB(t *testing.T, src, path string) *recorder {
	t.Helper()
	r := &recorder{}
	if err := (Extractor{}).ExtractRaw([]byte(src), path, r); err != nil {
		t.Fatalf("ExtractRaw: %v", err)
	}
	return r
}

func TestExtractHelperCalls(t *testing.T) {
	r := extractERB(t, `<p><%= current_currency %></p>
<%= link_to "Edit profile", edit_user_path(user) %>`, "app/views/orders/show.html.erb")

	// edit_user_path is a route helper → it targets the reserved route: symbol,
	// not a bare self.edit_user_path that could phantom-match an app method.
	for _, want := range []string{"self.current_currency", "self.link_to", "route:edit_user_path", "self.user"} {
		if !r.hasEdgeTo(want) {
			t.Errorf("missing helper-call edge %q", want)
		}
	}
	if r.hasEdgeTo("self.edit_user_path") {
		t.Error("a *_path reference must not emit a bare self.edit_user_path edge")
	}
	// Words inside string copy must not become calls.
	for _, bad := range []string{"self.Edit", "self.profile", "self.edit"} {
		if r.hasEdgeTo(bad) {
			t.Errorf("string-literal word wrongly emitted as call: %q", bad)
		}
	}
}

func TestExtractRenderPartial(t *testing.T) {
	r := extractERB(t, `<%= render "shared/header" %>
<%= render partial: "form" %>`, "app/views/users/show.html.erb")

	if !r.hasEdgeTo("partial:shared/header") {
		t.Error("missing absolute partial render edge partial:shared/header")
	}
	// Bare "form" resolves relative to the rendering view's directory (users).
	if !r.hasEdgeTo("partial:users/form") {
		t.Error("missing relative partial render edge partial:users/form")
	}
}

func TestEmitPartialSymbol(t *testing.T) {
	r := extractERB(t, `<div>just markup</div>`, "app/views/users/_profile.html.erb")
	if !r.hasSymbol("partial:users/profile") {
		t.Error("partial file should emit symbol partial:users/profile")
	}

	// A non-partial template emits no partial symbol.
	r2 := extractERB(t, `<div>page</div>`, "app/views/users/show.html.erb")
	if r2.hasSymbol("partial:users/show") {
		t.Error("non-partial template must not emit a partial symbol")
	}
}

func TestExtractI18nKeys(t *testing.T) {
	r := extractERB(t, `<h1><%= t(".title") %></h1>
<p><%= t("shared.footer.copyright") %></p>
<span><%= I18n.t("users.greeting") %></span>`, "app/views/users/show.html.erb")

	for _, want := range []string{
		"i18n:users.show.title", // lazy key expanded against the view scope
		"i18n:shared.footer.copyright",
		"i18n:users.greeting",
	} {
		if !r.hasSymbol(want) {
			t.Errorf("missing i18n symbol %q", want)
		}
	}
}

func TestErbCommentTagSkipped(t *testing.T) {
	r := extractERB(t, `<%# render "shared/header" and t(".title") %>`, "app/views/users/show.html.erb")
	if len(r.edges) != 0 || len(r.symbols) != 0 {
		t.Errorf("comment tag should emit nothing, got %d edges %d symbols", len(r.edges), len(r.symbols))
	}
}

// failingEmitter returns an error on the Nth Symbol or Edge call so error
// propagation paths are exercised.
type failingEmitter struct {
	failSymbolOn int
	failEdgeOn   int
	syms, edges  int
}

func (f *failingEmitter) Symbol(extract.EmittedSymbol) error {
	f.syms++
	if f.syms == f.failSymbolOn {
		return errBoom
	}
	return nil
}
func (f *failingEmitter) Edge(extract.EmittedEdge) error {
	f.edges++
	if f.edges == f.failEdgeOn {
		return errBoom
	}
	return nil
}

var errBoom = errBoomType("boom")

type errBoomType string

func (e errBoomType) Error() string { return string(e) }

func TestExtractTemplateRubyPathsOutsideViewsRoot(t *testing.T) {
	// A template not under a views/ root: render targets and partial symbols
	// fall back to bare names with no directory anchor.
	r := extractERB(t, `<%= render "menu" %>`, "components/_menu.erb")
	if !r.hasEdgeTo("partial:menu") {
		t.Error("bare render outside views/ should target partial:menu")
	}
	if !r.hasSymbol("partial:menu") {
		t.Error("partial outside views/ should emit symbol partial:menu")
	}
}

func TestExtractErrorPropagation(t *testing.T) {
	src := `<%= render "shared/header" %>
<%= t(".title") %>
<%= current_user %>`
	// Symbol emission failure (partial self-symbol / i18n key) propagates.
	if err := (Extractor{}).ExtractRaw([]byte(src), "app/views/users/_show.html.erb", &failingEmitter{failSymbolOn: 1}); err == nil {
		t.Error("expected symbol emit error to propagate")
	}
	// Edge emission failure (render / helper call) propagates.
	if err := (Extractor{}).ExtractRaw([]byte(src), "app/views/users/show.html.erb", &failingEmitter{failEdgeOn: 1}); err == nil {
		t.Error("expected edge emit error to propagate")
	}
}
