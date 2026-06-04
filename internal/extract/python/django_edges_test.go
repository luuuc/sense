package python

import "testing"

// These tests exercise Django URL-routing and model-field edge cases:
// urlpatterns shapes that emit no edges, view arguments that are neither
// as_view() nor include(), and field-constructor callees that aren't a
// plain identifier or attribute.

func TestURLPatternsNotAList(t *testing.T) {
	// `urlpatterns = build()` — RHS is a call, not a list; no URL edges.
	r := parse(t, `urlpatterns = build_patterns()
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" {
			t.Errorf("unexpected urlpatterns edge for non-list RHS: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternUnknownFunction(t *testing.T) {
	// A call that isn't path/re_path/include is ignored inside urlpatterns.
	r := parse(t, `urlpatterns = [
    route("home/", views.home),
]
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" {
			t.Errorf("unexpected urlpatterns edge for unknown routing fn: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternSinglePositionalArg(t *testing.T) {
	// `path("home/")` has only one positional argument — no view to wire.
	r := parse(t, `urlpatterns = [
    path("home/"),
]
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" && string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge for path with single arg: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternKeywordSecondArg(t *testing.T) {
	// `path("home/", name="home")` has only one positional arg; the keyword
	// argument is not a view reference.
	r := parse(t, `urlpatterns = [
    path("home/", name="home"),
]
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" && string(e.Kind) == "calls" {
			t.Errorf("unexpected calls edge for path with keyword second arg: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternViewCallNotAsViewOrInclude(t *testing.T) {
	// The view argument is a call, but neither `X.as_view()` nor `include()`,
	// so emitURLViewCall falls through with no edge.
	r := parse(t, `urlpatterns = [
    path("home/", make_view()),
]
`)
	for _, e := range r.edges {
		if e.SourceQualified == "urlpatterns" {
			t.Errorf("unexpected urlpatterns edge for plain view-factory call: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternBareIncludeCall(t *testing.T) {
	// `include("api.urls")` as the first call in the list (identifier callee
	// named include) wires an imports edge.
	r := parse(t, `urlpatterns = [
    include("api.urls"),
]
`)
	if findEdge(r, "urlpatterns", "api.urls", "imports") == nil {
		t.Error("missing imports edge from bare include() in urlpatterns")
	}
}

func TestModelFieldSubscriptCallee(t *testing.T) {
	// A field whose constructor callee is a subscript (`FIELDS[0](User)`)
	// yields no relation name, so djangoFieldName returns "" and no edge.
	r := parse(t, `class Order:
    customer = FIELDS[0](User)
`)
	for _, e := range r.edges {
		if string(e.Kind) == "composes" && e.SourceQualified == "Order" {
			t.Errorf("unexpected composes edge for subscript-callee field: %v", e.TargetQualified)
		}
	}
}

func TestURLPatternEdgeError(t *testing.T) {
	// A failing emitter propagates the error out of emitSingleURLPattern.
	err := parseWithEmitter(t, `urlpatterns = [
    path("orders/", views.order_list),
]
`, &failAfterN{symbolsLeft: 100, edgesLeft: 0})
	if err == nil {
		t.Error("expected error from failing emitter on URL pattern edge")
	}
}
