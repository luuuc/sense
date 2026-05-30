package mcpio

import (
	"context"
	"testing"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func TestViewEdgesSignal(t *testing.T) {
	tests := []struct {
		name        string
		subjectFile string
		present     bool
		want        string
	}{
		{"present wins regardless of subject kind", "app/models/order.rb", true, viewEdgesPresent},
		{"helper reached from view is present", "app/helpers/orders_helper.rb", true, viewEdgesPresent},
		{"controller with no view edge reports none", "app/controllers/orders_controller.rb", false, viewEdgesNone},
		{"stimulus controller with no view edge reports none", "app/javascript/controllers/cart_controller.ts", false, viewEdgesNone},
		{"model with no view edge stays silent", "app/models/order.rb", false, ""},
		{"service with no view edge stays silent", "app/services/checkout_service.rb", false, ""},
		{"go symbol is never view-relevant", "internal/extract/ruby/ruby.go", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := viewEdgesSignal(tt.subjectFile, tt.present); got != tt.want {
				t.Errorf("viewEdgesSignal(%q, %v) = %q, want %q", tt.subjectFile, tt.present, got, tt.want)
			}
		})
	}
}

func TestAnyViewTemplate(t *testing.T) {
	if !anyViewTemplate([]string{"app/services/x.rb", "app/views/orders/show.html.erb"}) {
		t.Error("expected an .erb file to count as a view template")
	}
	if anyViewTemplate([]string{"app/services/x.rb", "config/routes.rb"}) {
		t.Error("no .erb file should mean not view-reached")
	}
}

func TestIsViewTemplate(t *testing.T) {
	if !isViewTemplate("app/views/orders/show.html.erb") {
		t.Error(".erb file should be a view template")
	}
	if isViewTemplate("app/models/order.rb") {
		t.Error(".rb file is not a view template")
	}
}

func TestViewReachQuestionRelevant(t *testing.T) {
	relevant := []string{
		"app/controllers/orders_controller.rb",
		"app/helpers/orders_helper.rb",
		"app/javascript/controllers/cart_controller.js",
		"app/javascript/controllers/cart_controller.ts",
	}
	for _, f := range relevant {
		if !viewReachQuestionRelevant(f) {
			t.Errorf("%q should be view-reach relevant", f)
		}
	}
	irrelevant := []string{
		"app/models/order.rb",
		"app/services/checkout_service.rb",
		"app/javascript/utils/format.js",
		"internal/extract/ruby/ruby.go",
	}
	for _, f := range irrelevant {
		if viewReachQuestionRelevant(f) {
			t.Errorf("%q should not be view-reach relevant", f)
		}
	}
}

// TestBuildGraphResponseViewEdges proves the graph builder reads the inbound
// edge's file_id (the ERB template), NOT the caller symbol's file — a view
// edge has a NULL source, so the caller symbol is absent.
func TestBuildGraphResponseViewEdges(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/controllers/orders_controller.rb",
		2: "app/views/orders/index.html.erb",
		3: "app/services/checkout_service.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	// A view edge: edge emitted from the ERB file (FileID 2), source symbol
	// absent (the real shape — source_id is NULL for ERB edges).
	present := &model.SymbolContext{
		Symbol: model.Symbol{ID: 10, Qualified: "OrdersController#index", Kind: "method", FileID: 1},
		File:   model.File{Path: "app/controllers/orders_controller.rb"},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, FileID: 2, Confidence: extract.ConfidenceConvention}},
		},
	}
	if got := BuildGraphResponse(context.Background(), present, files, BuildGraphRequest{}).ViewEdges; got != viewEdgesPresent {
		t.Errorf("ViewEdges = %q, want %q (controller reached from an ERB view)", got, viewEdgesPresent)
	}

	none := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Qualified: "OrdersController#create", Kind: "method", FileID: 1},
		File:   model.File{Path: "app/controllers/orders_controller.rb"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, FileID: 3, Confidence: 1.0},
				Target: model.Symbol{Qualified: "App::CheckoutService", FileID: 3},
			},
		},
	}
	if got := BuildGraphResponse(context.Background(), none, files, BuildGraphRequest{}).ViewEdges; got != viewEdgesNone {
		t.Errorf("ViewEdges = %q, want %q (controller with no view edge)", got, viewEdgesNone)
	}
}

// TestBuildBlastResponseViewEdges proves the blast builder maps the engine's
// ViewReached flag to the view_edges signal.
func TestBuildBlastResponseViewEdges(t *testing.T) {
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "app/controllers/orders_controller.rb", true
		}
		return "", false
	}

	present := blast.Result{
		Symbol:      model.Symbol{ID: 10, Qualified: "OrdersController#index", FileID: 1},
		ViewReached: true,
	}
	if got := BuildBlastResponse(context.Background(), present, files, nil).ViewEdges; got != viewEdgesPresent {
		t.Errorf("ViewEdges = %q, want %q (engine reported view-reached)", got, viewEdgesPresent)
	}

	none := blast.Result{
		Symbol:      model.Symbol{ID: 11, Qualified: "OrdersController#create", FileID: 1},
		ViewReached: false,
	}
	if got := BuildBlastResponse(context.Background(), none, files, nil).ViewEdges; got != viewEdgesNone {
		t.Errorf("ViewEdges = %q, want %q (controller, not view-reached)", got, viewEdgesNone)
	}
}
