package erb

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func findEdgeKind(r *recorder, target string, kind model.EdgeKind) *extract.EmittedEdge {
	for i := range r.edges {
		if r.edges[i].TargetQualified == target && r.edges[i].Kind == kind {
			return &r.edges[i]
		}
	}
	return nil
}

func TestRenderCollection_PluralIvar(t *testing.T) {
	r := extractERB(t, "<%= render @posts %>", "app/views/posts/index.html.erb")
	e := findEdgeKind(r, extract.PrefixPartial+"posts/post", model.EdgeCalls)
	if e == nil {
		t.Fatalf("expected partial:posts/post edge, got %v", erbTargets(r))
	}
	if e.Line == nil || *e.Line != 1 {
		t.Errorf("edge line = %v, want 1", e.Line)
	}
}

func TestRenderCollection_KeywordAndCompoundName(t *testing.T) {
	r := extractERB(t, "<%= render collection: @line_items %>", "v.erb")
	if findEdgeKind(r, extract.PrefixPartial+"line_items/line_item", model.EdgeCalls) == nil {
		t.Fatalf("expected partial:line_items/line_item edge, got %v", erbTargets(r))
	}
}

func TestRenderCollection_SingularIvarSkipped(t *testing.T) {
	// `render @post` (singular) would need pluralization to build the directory;
	// guessing is a phantom, so no collection edge is emitted.
	r := extractERB(t, "<%= render @post %>", "v.erb")
	if findEdgeKind(r, extract.PrefixPartial+"post/post", model.EdgeCalls) != nil {
		t.Errorf("singular ivar must not emit a guessed collection partial; got %v", erbTargets(r))
	}
}

func TestRenderCollection_ExplicitPartialWins(t *testing.T) {
	// An explicit partial: path is authoritative; the convention must not also
	// guess posts/post from the collection ivar.
	r := extractERB(t, `<%= render partial: "posts/card", collection: @posts %>`, "v.erb")
	if findEdgeKind(r, extract.PrefixPartial+"posts/card", model.EdgeCalls) == nil {
		t.Errorf("expected the explicit partial:posts/card edge, got %v", erbTargets(r))
	}
	if findEdgeKind(r, extract.PrefixPartial+"posts/post", model.EdgeCalls) != nil {
		t.Errorf("convention must not override an explicit partial; got %v", erbTargets(r))
	}
}

func TestFormModel_KeywordShape(t *testing.T) {
	r := extractERB(t, `<%= form_with model: @order do |f| %>`, "app/views/orders/new.html.erb")
	e := findEdgeKind(r, "Order", model.EdgeReferences)
	if e == nil {
		t.Fatalf("expected references edge to Order, got %v", erbTargets(r))
	}
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("form-model edge confidence = %v, want %v", e.Confidence, extract.ConfidenceConvention)
	}
}

func TestFormModel_PositionalFormFor(t *testing.T) {
	r := extractERB(t, `<%= form_for @user do |f| %>`, "v.erb")
	if findEdgeKind(r, "User", model.EdgeReferences) == nil {
		t.Fatalf("expected references edge to User, got %v", erbTargets(r))
	}
}

func TestFormModel_NoModelNoEdge(t *testing.T) {
	r := extractERB(t, `<%= form_with url: search_path do |f| %>`, "v.erb")
	for _, e := range r.edges {
		if e.Kind == model.EdgeReferences {
			t.Errorf("a form with no model should emit no references edge, got %q", e.TargetQualified)
		}
	}
}

func TestFormModel_GatedOnFormContext(t *testing.T) {
	// `model:` outside a form_with/form_for is not a form binding — no edge.
	r := extractERB(t, `<%= some_helper model: @order %>`, "v.erb")
	if findEdgeKind(r, "Order", model.EdgeReferences) != nil {
		t.Errorf("model: outside a form must not emit a model edge; got %v", erbTargets(r))
	}
}

func TestRenderCollectionEmitError(t *testing.T) {
	err := (Extractor{}).ExtractRaw([]byte(`<%= render @posts %>`), "v.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on render-collection")
	}
}

func TestFormModelEmitError(t *testing.T) {
	err := (Extractor{}).ExtractRaw([]byte(`<%= form_with model: @order do |f| %>`), "v.erb", &failEdgeEmitter{})
	if err == nil {
		t.Error("expected error from failing edge emitter on form-model")
	}
}

func TestFormModel_DedupSameModel(t *testing.T) {
	// Both the positional and keyword shapes name the same model — emit once.
	r := extractERB(t, `<%= form_for @order, model: @order do |f| %>`, "v.erb")
	var count int
	for _, e := range r.edges {
		if e.TargetQualified == "Order" && e.Kind == model.EdgeReferences {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Order references edge emitted %d times, want 1 (deduped)", count)
	}
}
