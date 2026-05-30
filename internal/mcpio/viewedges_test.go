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
		callerFiles []string
		want        string
	}{
		{
			name:        "ruby helper reached from a view template is present",
			subjectFile: "app/helpers/orders_helper.rb",
			callerFiles: []string{"app/views/orders/show.html.erb"},
			want:        viewEdgesPresent,
		},
		{
			name:        "stimulus controller dispatched from erb is present",
			subjectFile: "app/javascript/controllers/checkout_controller.js",
			callerFiles: []string{"app/views/checkout/new.html.erb"},
			want:        viewEdgesPresent,
		},
		{
			name:        "present wins even for a non-view-adjacent subject",
			subjectFile: "app/models/order.rb",
			callerFiles: []string{"app/views/orders/_row.html.erb"},
			want:        viewEdgesPresent,
		},
		{
			name:        "controller with no view edge reports none",
			subjectFile: "app/controllers/orders_controller.rb",
			callerFiles: []string{"app/services/checkout_service.rb"},
			want:        viewEdgesNone,
		},
		{
			name:        "stimulus controller with no view edge reports none",
			subjectFile: "app/javascript/controllers/cart_controller.ts",
			callerFiles: nil,
			want:        viewEdgesNone,
		},
		{
			name:        "model with no view edge stays silent",
			subjectFile: "app/models/order.rb",
			callerFiles: []string{"app/services/checkout_service.rb"},
			want:        "",
		},
		{
			name:        "service with no view edge stays silent",
			subjectFile: "app/services/checkout_service.rb",
			callerFiles: nil,
			want:        "",
		},
		{
			name:        "go symbol is never view-relevant",
			subjectFile: "internal/extract/ruby/ruby.go",
			callerFiles: []string{"internal/scan/scan.go"},
			want:        "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := viewEdgesSignal(tt.subjectFile, tt.callerFiles); got != tt.want {
				t.Errorf("viewEdgesSignal(%q, %v) = %q, want %q",
					tt.subjectFile, tt.callerFiles, got, tt.want)
			}
		})
	}
}

// TestBuildGraphResponseViewEdges proves the graph builder reports view_edges
// "present" when an ERB caller reaches the subject and "none" when a
// view-relevant subject has no view edge.
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

	present := &model.SymbolContext{
		Symbol: model.Symbol{ID: 10, Name: "index", Qualified: "OrdersController#index", Kind: "method", FileID: 1},
		File:   model.File{Path: "app/controllers/orders_controller.rb"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: extract.ConfidenceConvention},
				Target: model.Symbol{Qualified: "app/views/orders/index.html.erb", FileID: 2},
			},
		},
	}
	if got := BuildGraphResponse(context.Background(), present, files, BuildGraphRequest{}).ViewEdges; got != viewEdgesPresent {
		t.Errorf("ViewEdges = %q, want %q (controller reached from an ERB view)", got, viewEdgesPresent)
	}

	none := &model.SymbolContext{
		Symbol: model.Symbol{ID: 11, Name: "create", Qualified: "OrdersController#create", Kind: "method", FileID: 1},
		File:   model.File{Path: "app/controllers/orders_controller.rb"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
				Target: model.Symbol{Qualified: "App::CheckoutService", FileID: 3},
			},
		},
	}
	if got := BuildGraphResponse(context.Background(), none, files, BuildGraphRequest{}).ViewEdges; got != viewEdgesNone {
		t.Errorf("ViewEdges = %q, want %q (controller with no view edge)", got, viewEdgesNone)
	}
}

// TestBuildBlastResponseViewEdges proves the blast builder reports view_edges
// "present" when an ERB direct caller reaches the subject and "none" for a
// view-relevant subject with no view edge.
func TestBuildBlastResponseViewEdges(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/controllers/orders_controller.rb",
		2: "app/views/orders/index.html.erb",
		3: "app/services/checkout_service.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	present := blast.Result{
		Symbol:        model.Symbol{ID: 10, Qualified: "OrdersController#index", FileID: 1},
		DirectCallers: []model.Symbol{{ID: 20, Qualified: "app/views/orders/index.html.erb", FileID: 2}},
	}
	if got := BuildBlastResponse(context.Background(), present, files, nil).ViewEdges; got != viewEdgesPresent {
		t.Errorf("ViewEdges = %q, want %q (controller reached from an ERB view)", got, viewEdgesPresent)
	}

	none := blast.Result{
		Symbol:        model.Symbol{ID: 11, Qualified: "OrdersController#create", FileID: 1},
		DirectCallers: []model.Symbol{{ID: 21, Qualified: "App::CheckoutService", FileID: 3}},
	}
	if got := BuildBlastResponse(context.Background(), none, files, nil).ViewEdges; got != viewEdgesNone {
		t.Errorf("ViewEdges = %q, want %q (controller with no view edge)", got, viewEdgesNone)
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
		"app/javascript/utils/format.js", // not a controller
		"internal/extract/ruby/ruby.go",
	}
	for _, f := range irrelevant {
		if viewReachQuestionRelevant(f) {
			t.Errorf("%q should not be view-reach relevant", f)
		}
	}
}
