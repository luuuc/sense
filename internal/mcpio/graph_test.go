package mcpio

import (
	"bytes"
	"context"
	"strconv"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

func bigGraphResponse(callers, layers int) GraphResponse {
	r := GraphResponse{Symbol: GraphSymbol{Name: "Hub", Qualified: "pkg.Hub"}}
	for i := 0; i < callers; i++ {
		f := "app/models/caller" + itoa(i) + ".rb"
		r.Edges.CalledBy = append(r.Edges.CalledBy, CallEdgeRef{
			Symbol: "pkg.Caller" + itoa(i), File: &f, LineStart: i, Confidence: 1.0,
			CallSite: &CallSite{Line: i, Snippet: "a representative line of calling source code"},
		})
	}
	for d := 0; d < layers; d++ {
		layer := GraphLayer{Depth: d + 2}
		for i := 0; i < 20; i++ {
			s := "pkg.L" + itoa(d) + "_" + itoa(i)
			layer.Edges.CalledBy = append(layer.Edges.CalledBy, CallEdgeRef{Symbol: s, Confidence: 1.0})
		}
		r.Layers = append(r.Layers, layer)
	}
	return r
}

func TestApplyGraphBudgetNoopAndDisabled(t *testing.T) {
	r := bigGraphResponse(3, 0)
	ApplyGraphBudget(&r, 1_000_000)
	if r.Truncated || r.OmittedEdges != 0 || len(r.Edges.CalledBy) != 3 {
		t.Error("under-budget graph response should be untouched")
	}
	r2 := bigGraphResponse(200, 2)
	ApplyGraphBudget(&r2, 0)
	if r2.Truncated || len(r2.Edges.CalledBy) != 200 {
		t.Error("budget <= 0 must disable trimming")
	}
}

func TestApplyGraphBudgetDropsLayersFirst(t *testing.T) {
	r := bigGraphResponse(15, 3)
	const budget = 1200
	ApplyGraphBudget(&r, budget)
	if got := estimateJSONTokens(&r); got > budget {
		t.Errorf("after trim tokens=%d exceed budget=%d", got, budget)
	}
	if !r.Truncated || r.OmittedEdges == 0 {
		t.Errorf("expected Truncated + OmittedEdges>0, got Truncated=%v Omitted=%d", r.Truncated, r.OmittedEdges)
	}
	if len(r.Layers) == 3 {
		t.Error("expected deeper layers to be dropped first")
	}
}

func TestApplyGraphBudgetTrimsLongestEdgeList(t *testing.T) {
	r := bigGraphResponse(400, 0)
	const budget = 800
	ApplyGraphBudget(&r, budget)
	if got := estimateJSONTokens(&r); got > budget {
		t.Errorf("after trim tokens=%d exceed budget=%d", got, budget)
	}
	if len(r.Edges.CalledBy) < 1 {
		t.Error("must keep at least one edge per kind")
	}
	if r.OmittedEdges != 400-len(r.Edges.CalledBy) {
		t.Errorf("OmittedEdges=%d should equal dropped=%d", r.OmittedEdges, 400-len(r.Edges.CalledBy))
	}
}

func TestApplyGraphBudgetTrimsEveryEdgeKind(t *testing.T) {
	// All ten edge kinds start equally long; a tiny budget forces the
	// trimmer to cycle through and shrink each kind's slice in turn. The
	// directed inheritance/composition split adds ComposedBy and
	// InheritedBy as their own buckets, so each must have its drop path
	// exercised alongside the outbound Composes / Inherits kinds.
	r := GraphResponse{Symbol: GraphSymbol{Name: "Hub", Qualified: "pkg.Hub"}}
	const per = 12
	for i := 0; i < per; i++ {
		s := "pkg.S" + itoa(i)
		r.Edges.CalledBy = append(r.Edges.CalledBy, CallEdgeRef{Symbol: s, Confidence: 1.0})
		r.Edges.Calls = append(r.Edges.Calls, CallEdgeRef{Symbol: s, Confidence: 1.0})
		r.Edges.Inherits = append(r.Edges.Inherits, InheritEdgeRef{Symbol: s})
		r.Edges.InheritedBy = append(r.Edges.InheritedBy, InheritEdgeRef{Symbol: s})
		r.Edges.Composes = append(r.Edges.Composes, ComposeEdgeRef{Symbol: s})
		r.Edges.ComposedBy = append(r.Edges.ComposedBy, ComposeEdgeRef{Symbol: s})
		r.Edges.Includes = append(r.Edges.Includes, IncludeEdgeRef{Symbol: s})
		r.Edges.Imports = append(r.Edges.Imports, ImportEdgeRef{Symbol: s})
		r.Edges.Temporal = append(r.Edges.Temporal, TemporalEdgeRef{Symbol: s, Strength: 0.5})
		r.Edges.Tests = append(r.Edges.Tests, TestEdgeRef{File: "test/" + s + "_test.rb", Confidence: 0.8})
	}
	// A budget below the one-entry-per-kind floor forces every kind to be
	// trimmed down to its single representative, exercising each kind's
	// drop path while proving no relationship type is dropped entirely.
	ApplyGraphBudget(&r, 200)
	kinds := []int{
		len(r.Edges.CalledBy), len(r.Edges.Calls), len(r.Edges.Inherits), len(r.Edges.InheritedBy),
		len(r.Edges.Composes), len(r.Edges.ComposedBy), len(r.Edges.Includes), len(r.Edges.Imports),
		len(r.Edges.Temporal), len(r.Edges.Tests),
	}
	for i, n := range kinds {
		if n != 1 {
			t.Errorf("edge kind %d trimmed to %d, want 1", i, n)
		}
	}
	if !r.Truncated {
		t.Error("expected Truncated=true")
	}
	if want := (per - 1) * 10; r.OmittedEdges != want {
		t.Errorf("OmittedEdges = %d, want %d", r.OmittedEdges, want)
	}
}

func TestTrimLongestEdgeListStopsWhenMinimal(t *testing.T) {
	e := &GraphEdges{CalledBy: []CallEdgeRef{{Symbol: "a"}}, Calls: []CallEdgeRef{{Symbol: "b"}}}
	resp := &GraphResponse{}
	if trimLongestEdgeList(e, resp) {
		t.Error("should return false when no list has more than one entry")
	}
}

func TestBuildGraphResponseComposesEdges(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/models/user.rb",
		2: "app/models/order.rb",
		3: "app/models/wallet.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			ID: 1, Name: "User", Qualified: "User",
			Kind: "class", FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeComposes, Confidence: 1.0},
				Target: model.Symbol{Qualified: "Order", FileID: 2},
			},
			{
				Edge:   model.Edge{Kind: model.EdgeComposes, Confidence: 1.0},
				Target: model.Symbol{Qualified: "Wallet", FileID: 3},
			},
		},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeComposes, Confidence: 1.0},
				Target: model.Symbol{Qualified: "Order", FileID: 2},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	// Outbound composes ("what User owns") land in Composes; the inbound composer
	// ("what owns User") lands in ComposedBy — the two directions are no longer
	// conflated.
	if len(resp.Edges.Composes) != 2 {
		t.Fatalf("Composes = %d, want 2 (outbound only)", len(resp.Edges.Composes))
	}
	if resp.Edges.Composes[0].Symbol != "Order" {
		t.Errorf("Composes[0].Symbol = %q, want %q", resp.Edges.Composes[0].Symbol, "Order")
	}
	if resp.Edges.Composes[0].File == nil {
		t.Error("Composes[0].File = nil, want non-nil")
	}
	if len(resp.Edges.ComposedBy) != 1 {
		t.Fatalf("ComposedBy = %d, want 1 (the inbound composer)", len(resp.Edges.ComposedBy))
	}
	if resp.Edges.ComposedBy[0].Symbol != "Order" {
		t.Errorf("ComposedBy[0].Symbol = %q, want %q", resp.Edges.ComposedBy[0].Symbol, "Order")
	}
}

func TestBuildGraphResponseViewOriginInboundEdgeSurfacesTemplate(t *testing.T) {
	// A Stimulus controller's inbound calls originate in ERB templates whose
	// source is not a named symbol (source_id == 0), so the LEFT-joined source
	// columns come back empty. The edge's own file id carries the template, so
	// the caller must surface the template path instead of a blank
	// {symbol:"", file:null} stub.
	const erbFileID = 7
	const tmpl = "app/views/marketplace/photo_upload/_form.html.erb"
	files := func(id int64) (string, bool) {
		if id == erbFileID {
			return tmpl, true
		}
		return "", false
	}
	const callLine = 12
	line := callLine
	sc := &model.SymbolContext{
		Symbol: model.Symbol{ID: 1, Name: "PhotoUploadController", Qualified: "Marketplace::PhotoUploadController", Kind: "class", FileID: 1},
		File:   model.File{Path: "app/javascript/controllers/marketplace/photo_upload_controller.js"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 0.9, FileID: erbFileID, Line: &line},
				Target: model.Symbol{ID: 0}, // unresolved view source
			},
		},
	}
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	got := resp.Edges.CalledBy[0]
	if got.Symbol != tmpl {
		t.Errorf("Symbol = %q, want template path %q", got.Symbol, tmpl)
	}
	if got.File == nil || *got.File != tmpl {
		t.Errorf("File = %v, want %q", got.File, tmpl)
	}
	// The call-site line is half of "where is this used?" — it must be lifted
	// from the edge onto the recovered ref, including the formatted Ref.
	if got.LineStart != callLine || got.LineEnd != callLine {
		t.Errorf("LineStart/LineEnd = %d/%d, want %d/%d", got.LineStart, got.LineEnd, callLine, callLine)
	}
	wantRef := tmpl + ":" + strconv.Itoa(callLine)
	if got.Ref != wantRef {
		t.Errorf("Ref = %q, want %q", got.Ref, wantRef)
	}
}

func TestBuildGraphResponseViewOriginInboundEdgeWithoutLine(t *testing.T) {
	// A view-origin edge whose call site carries no line (e.Edge.Line == nil)
	// still recovers the template path; the line fields stay zero rather than
	// inheriting stale values from the unresolved target.
	const erbFileID = 7
	const tmpl = "app/views/marketplace/photo_upload/_index.html.erb"
	files := func(id int64) (string, bool) {
		if id == erbFileID {
			return tmpl, true
		}
		return "", false
	}
	sc := &model.SymbolContext{
		Symbol: model.Symbol{ID: 1, Qualified: "Marketplace::PhotoUploadController", Kind: "class", FileID: 1},
		File:   model.File{Path: "app/javascript/controllers/marketplace/photo_upload_controller.js"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 0.9, FileID: erbFileID}, // Line nil
				Target: model.Symbol{ID: 0},
			},
		},
	}
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	got := resp.Edges.CalledBy[0]
	if got.Symbol != tmpl || got.File == nil || *got.File != tmpl {
		t.Errorf("want template path recovered, got symbol=%q file=%v", got.Symbol, got.File)
	}
	if got.LineStart != 0 || got.LineEnd != 0 {
		t.Errorf("LineStart/LineEnd = %d/%d, want 0/0 (no call-site line)", got.LineStart, got.LineEnd)
	}
}

func TestBuildGraphResponseUnresolvedInboundWithoutFileStaysBlank(t *testing.T) {
	// Guard: when the source is unresolved AND the edge's file id is unknown,
	// keep the prior (blank) behavior rather than crashing.
	files := func(int64) (string, bool) { return "", false }
	sc := &model.SymbolContext{
		Symbol: model.Symbol{ID: 1, Qualified: "X", Kind: "class"},
		File:   model.File{Path: "x.js"},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.9, FileID: 99}, Target: model.Symbol{ID: 0}},
		},
	}
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	if resp.Edges.CalledBy[0].Symbol != "" || resp.Edges.CalledBy[0].File != nil {
		t.Errorf("want blank stub when no file to recover, got symbol=%q file=%v",
			resp.Edges.CalledBy[0].Symbol, resp.Edges.CalledBy[0].File)
	}
}

func TestBuildGraphResponseComposesDirection(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "User", Qualified: "User", Kind: "class"},
		File:   model.File{Path: "user.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeComposes},
				Target: model.Symbol{Qualified: "Order"},
			},
		},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeComposes},
				Target: model.Symbol{Qualified: "Profile"},
			},
		},
	}
	files := func(int64) (string, bool) { return "", false }

	// Callees = outbound only: the owned Order surfaces in Composes; the inbound
	// composer is out of scope.
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallees})
	if len(resp.Edges.Composes) != 1 || resp.Edges.Composes[0].Symbol != "Order" {
		t.Errorf("callees direction: want only outbound Order in Composes, got %v", resp.Edges.Composes)
	}
	if len(resp.Edges.ComposedBy) != 0 {
		t.Errorf("callees direction: want empty ComposedBy, got %v", resp.Edges.ComposedBy)
	}

	// Callers = inbound only: the composer Profile surfaces in ComposedBy; the
	// outbound Composes bucket is out of scope.
	resp = BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(resp.Edges.ComposedBy) != 1 || resp.Edges.ComposedBy[0].Symbol != "Profile" {
		t.Errorf("callers direction: want only inbound Profile in ComposedBy, got %v", resp.Edges.ComposedBy)
	}
	if len(resp.Edges.Composes) != 0 {
		t.Errorf("callers direction: want empty Composes, got %v", resp.Edges.Composes)
	}
}

func TestBuildGraphResponseIncludesImports(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/models/user.rb",
		2: "app/concerns/soft_deletable.rb",
		3: "src/utils.ts",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "User", Qualified: "User",
			Kind: "class", FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeIncludes},
				Target: model.Symbol{Qualified: "SoftDeletable", FileID: 2},
			},
			{
				Edge:   model.Edge{Kind: model.EdgeImports},
				Target: model.Symbol{Qualified: "utils", FileID: 3},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.Includes) != 1 {
		t.Fatalf("Includes = %d, want 1", len(resp.Edges.Includes))
	}
	if resp.Edges.Includes[0].Symbol != "SoftDeletable" {
		t.Errorf("Includes[0].Symbol = %q, want %q", resp.Edges.Includes[0].Symbol, "SoftDeletable")
	}

	if len(resp.Edges.Imports) != 1 {
		t.Fatalf("Imports = %d, want 1", len(resp.Edges.Imports))
	}
	if resp.Edges.Imports[0].Symbol != "utils" {
		t.Errorf("Imports[0].Symbol = %q, want %q", resp.Edges.Imports[0].Symbol, "utils")
	}
}

func TestBuildGraphResponseTemporalEdges(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/services/checkout.rb",
		2: "app/jobs/export_cron.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	coChanges := 14
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			ID: 1, Name: "Checkout", Qualified: "Checkout",
			Kind: "class", FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/services/checkout.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeTemporal, Confidence: 0.7, Line: &coChanges},
				Target: model.Symbol{ID: 2, Qualified: "ExportCron", FileID: 2},
			},
		},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeTemporal, Confidence: 0.7, Line: &coChanges},
				Target: model.Symbol{ID: 2, Qualified: "ExportCron", FileID: 2},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	// Temporal edges are deduplicated — even though the same symbol appears
	// in both inbound and outbound, only one entry should appear.
	if len(resp.Edges.Temporal) != 1 {
		t.Fatalf("Temporal = %d, want 1 (deduplicated)", len(resp.Edges.Temporal))
	}
	te := resp.Edges.Temporal[0]
	if te.Symbol != "ExportCron" {
		t.Errorf("Symbol = %q, want %q", te.Symbol, "ExportCron")
	}
	if te.CoChanges != 14 {
		t.Errorf("CoChanges = %d, want 14", te.CoChanges)
	}
	if float64(te.Strength) != 0.7 {
		t.Errorf("Strength = %v, want 0.7", te.Strength)
	}
	if te.File == nil || *te.File != "app/jobs/export_cron.rb" {
		t.Errorf("File = %v, want app/jobs/export_cron.rb", te.File)
	}
}

func TestBuildGraphResponseTemporalEmptyWhenNoEdges(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "Foo", Qualified: "Foo", Kind: "class"},
		File:   model.File{Path: "foo.rb"},
	}
	files := func(int64) (string, bool) { return "", false }
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})
	if resp.Edges.Temporal != nil {
		t.Errorf("Temporal should be nil (not empty slice) before normalization, got %v", resp.Edges.Temporal)
	}
}

func TestBuildGraphResponseTemporalDirectionIndependent(t *testing.T) {
	coChanges := 5
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "A", Qualified: "A", Kind: "class"},
		File:   model.File{Path: "a.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeTemporal, Confidence: 0.5, Line: &coChanges},
				Target: model.Symbol{ID: 2, Qualified: "B", FileID: 2},
			},
		},
	}
	files := func(int64) (string, bool) { return "", false }

	// Temporal should appear regardless of direction filter.
	for _, dir := range []model.Direction{model.DirectionBoth, model.DirectionCallers, model.DirectionCallees} {
		resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: dir})
		if len(resp.Edges.Temporal) != 1 {
			t.Errorf("direction=%q: Temporal = %d, want 1", dir, len(resp.Edges.Temporal))
		}
	}
}

func TestCallerSegmentation(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/models/user.rb",
		2: "app/services/auth.rb",
		3: "app/controllers/sessions.rb",
		4: "spec/models/user_spec.rb",
		5: "spec/services/auth_spec.rb",
		6: "test/controllers/sessions_test.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			ID: 1, Name: "User", Qualified: "User",
			Kind: "class", FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "AuthService#login", FileID: 2}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "SessionsController#create", FileID: 3}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "UserSpec#test_login", FileID: 4}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "AuthSpec#test_auth", FileID: 5}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "SessionsTest#test_create", FileID: 6}},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: true})

	if len(resp.Edges.CalledBy) != 2 {
		t.Fatalf("CalledBy (production) = %d, want 2", len(resp.Edges.CalledBy))
	}
	if resp.TestCallerSummary == nil {
		t.Fatal("TestCallerSummary is nil, want non-nil")
	}
	if resp.TestCallerSummary.Count != 3 {
		t.Errorf("TestCallerSummary.Count = %d, want 3", resp.TestCallerSummary.Count)
	}
	if resp.SenseMetrics.SymbolsReturned != 5 {
		t.Errorf("SymbolsReturned = %d, want 5 (2 prod + 3 test)", resp.SenseMetrics.SymbolsReturned)
	}

	// Without SegmentCallers, all callers stay in CalledBy.
	resp = BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: false})
	if len(resp.Edges.CalledBy) != 5 {
		t.Fatalf("CalledBy (unsegmented) = %d, want 5", len(resp.Edges.CalledBy))
	}
	if resp.TestCallerSummary != nil {
		t.Errorf("TestCallerSummary should be nil when segmentation disabled, got %v", resp.TestCallerSummary)
	}
}

func TestCallerSegmentationCollapseOver20(t *testing.T) {
	testFiles := []string{
		"spec/models/user_spec.rb",
		"spec/services/auth_spec.rb",
		"test/controllers/sessions_test.rb",
		"spec/integration/user_flow_spec.rb",
		"test/models/user_test.rb",
	}
	filePaths := map[int64]string{1: "app/models/user.rb"}
	for i := int64(2); i <= 32; i++ {
		filePaths[i] = testFiles[(i-2)%int64(len(testFiles))]
	}
	filePaths[33] = "app/services/auth.rb"
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	var inbound []model.EdgeRef
	for i := int64(2); i <= 32; i++ {
		inbound = append(inbound, model.EdgeRef{
			Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
			Target: model.Symbol{Qualified: "TestCaller", FileID: i},
		})
	}
	inbound = append(inbound, model.EdgeRef{
		Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
		Target: model.Symbol{Qualified: "AuthService#login", FileID: 33},
	})

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			ID: 1, Name: "User", Qualified: "User",
			Kind: "class", FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File:    model.File{Path: "app/models/user.rb"},
		Inbound: inbound,
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: true})

	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy (production) = %d, want 1", len(resp.Edges.CalledBy))
	}
	if resp.TestCallerSummary == nil {
		t.Fatal("TestCallerSummary is nil")
	}
	if resp.TestCallerSummary.Count != 31 {
		t.Errorf("TestCallerSummary.Count = %d, want 31", resp.TestCallerSummary.Count)
	}
	if len(resp.TestCallerSummary.Examples) != 3 {
		t.Errorf("TestCallerSummary.Examples = %d, want exactly 3 (truncated from 5 unique paths)", len(resp.TestCallerSummary.Examples))
	}
}

func TestBuildGraphResponseTemporalCountsInMetrics(t *testing.T) {
	coChanges := 3
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "A", Qualified: "A", Kind: "class", FileID: 1},
		File:   model.File{Path: "a.rb"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeTemporal, Confidence: 0.5, Line: &coChanges},
				Target: model.Symbol{ID: 2, Qualified: "B", FileID: 2},
			},
		},
	}
	filePaths := map[int64]string{2: "b.rb"}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})
	if resp.SenseMetrics.SymbolsReturned != 1 {
		t.Errorf("SymbolsReturned = %d, want 1", resp.SenseMetrics.SymbolsReturned)
	}
	if resp.SenseMetrics.EstimatedFileReadsAvoided != 1 {
		t.Errorf("EstimatedFileReadsAvoided = %d, want 1", resp.SenseMetrics.EstimatedFileReadsAvoided)
	}
}

func TestIsTestPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"internal/foo/foo_test.go", true},
		{"src/app.test.ts", true},
		{"test/helpers.rb", true},
		{"tests/unit/auth.py", true},
		{"spec/models/user_spec.rb", true},
		{"internal/testdata/fixture.json", true},
		{"src/main/java/com/example/UserTest.java", true},
		{"src/test/kotlin/TestUser.kt", true},
		{"src/test/java/UserTests.java", true},
		{"lib/test_auth.py", true},
		{"src/main/java/com/example/User.java", false},
		{"src/main/java/com/example/TestUtils.java", false},
		{"src/main/java/com/example/Contest.java", false},
		{"internal/foo/foo.go", false},
		{"lib/auth.rb", false},
	}
	for _, tt := range tests {
		if got := IsTestPath(tt.path); got != tt.want {
			t.Errorf("IsTestPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestCollectEdgeFiles(t *testing.T) {
	p1 := "a.go"
	p2 := "b.go"
	p3 := "test/c.go"
	edges := GraphEdges{
		Calls:    []CallEdgeRef{{Symbol: "A", File: &p1}},
		CalledBy: []CallEdgeRef{{Symbol: "B", File: nil}},
		Inherits: []InheritEdgeRef{{Symbol: "C", File: &p2}},
		Composes: []ComposeEdgeRef{},
		Includes: []IncludeEdgeRef{{Symbol: "D", File: nil}},
		Imports:  []ImportEdgeRef{{Symbol: "E", File: nil}},
		Tests:    []TestEdgeRef{{File: p3}},
		Temporal: []TemporalEdgeRef{{Symbol: "F", File: nil}},
	}
	seen := map[string]struct{}{}
	collectEdgeFiles(edges, seen)
	if len(seen) != 3 {
		t.Errorf("collectEdgeFiles collected %d unique files, want 3", len(seen))
	}
	if _, ok := seen[p1]; !ok {
		t.Errorf("expected a.go in seen")
	}
	if _, ok := seen[p2]; !ok {
		t.Errorf("expected b.go in seen")
	}
	if _, ok := seen[p3]; !ok {
		t.Errorf("expected test/c.go in seen")
	}
}

func TestFileRefOrNil(t *testing.T) {
	files := map[int64]string{1: "a.go", 2: "b.go"}
	lookup := func(id int64) (string, bool) {
		p, ok := files[id]
		return p, ok
	}

	if ref := fileRefOrNil(1, lookup); ref == nil || *ref != "a.go" {
		t.Errorf("fileRefOrNil(1) = %v, want *a.go", ref)
	}
	if ref := fileRefOrNil(2, lookup); ref == nil || *ref != "b.go" {
		t.Errorf("fileRefOrNil(2) = %v, want *b.go", ref)
	}
	if ref := fileRefOrNil(999, lookup); ref != nil {
		t.Errorf("fileRefOrNil(999) = %v, want nil", ref)
	}
}

func TestBuildTestCallerSummaryUnderThreshold(t *testing.T) {
	ref1 := "spec/a_spec.rb"
	ref2 := "test/b_test.rb"
	callers := []CallEdgeRef{
		{Symbol: "TestA", File: &ref1},
		{Symbol: "TestB", File: &ref2},
	}
	sum := buildTestCallerSummary(callers)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
	if sum.Count != 2 {
		t.Errorf("Count = %d, want 2", sum.Count)
	}
	if len(sum.Examples) != 2 {
		t.Errorf("Examples len = %d, want 2", len(sum.Examples))
	}
}

func TestBuildTestCallerSummaryExactlyThreshold(t *testing.T) {
	callers := make([]CallEdgeRef, testCallerCollapseThreshold)
	for i := range callers {
		p := "spec/file" + string(rune('A'+i%26)) + string(rune('0'+i/26)) + "_spec.rb"
		callers[i] = CallEdgeRef{Symbol: "Test" + string(rune('A'+i)), File: &p}
	}
	sum := buildTestCallerSummary(callers)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
	if sum.Count != testCallerCollapseThreshold {
		t.Errorf("Count = %d, want %d", sum.Count, testCallerCollapseThreshold)
	}
	if len(sum.Examples) != testCallerCollapseThreshold {
		t.Errorf("Examples len = %d, want %d (not truncated when exactly at threshold)", len(sum.Examples), testCallerCollapseThreshold)
	}
}

func TestBuildTestCallerSummaryOverThreshold(t *testing.T) {
	callers := make([]CallEdgeRef, testCallerCollapseThreshold+10)
	for i := range callers {
		p := "spec/test" + string(rune('A'+i)) + "_spec.rb"
		callers[i] = CallEdgeRef{Symbol: "Test" + string(rune('A'+i)), File: &p}
	}
	sum := buildTestCallerSummary(callers)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
	if sum.Count != testCallerCollapseThreshold+10 {
		t.Errorf("Count = %d, want %d", sum.Count, testCallerCollapseThreshold+10)
	}
	if len(sum.Examples) != 3 {
		t.Errorf("Examples len = %d, want 3 (truncated)", len(sum.Examples))
	}
}

func TestBuildTestCallerSummaryNilFiles(t *testing.T) {
	callers := []CallEdgeRef{
		{Symbol: "TestA", File: nil},
		{Symbol: "TestB", File: nil},
	}
	sum := buildTestCallerSummary(callers)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
	if sum.Count != 2 {
		t.Errorf("Count = %d, want 2", sum.Count)
	}
	if len(sum.Examples) != 0 {
		t.Errorf("Examples len = %d, want 0 (nil files skipped)", len(sum.Examples))
	}
}

func TestBuildTestCallerSummaryDuplicateFiles(t *testing.T) {
	p := "spec/a_spec.rb"
	callers := []CallEdgeRef{
		{Symbol: "TestA", File: &p},
		{Symbol: "TestB", File: &p},
		{Symbol: "TestC", File: &p},
	}
	sum := buildTestCallerSummary(callers)
	if sum == nil {
		t.Fatal("expected non-nil summary")
	}
	if sum.Count != 3 {
		t.Errorf("Count = %d, want 3", sum.Count)
	}
	if len(sum.Examples) != 1 {
		t.Errorf("Examples len = %d, want 1 (deduplicated to 1 unique file)", len(sum.Examples))
	}
}

func TestGraphVerifyHintConstantZeroCallers(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "NEXT_REQUEST_ID_HEADER", Qualified: "NEXT_REQUEST_ID_HEADER",
			Kind: model.KindConstant, FileID: 1, LineStart: 5, LineEnd: 5,
		},
		File: model.File{Path: "lib/headers.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "String#freeze", FileID: 2}},
		},
	}
	files := func(id int64) (string, bool) {
		m := map[int64]string{1: "lib/headers.rb", 2: "lib/string.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if resp.VerifyHint == "" {
		t.Fatal("VerifyHint should be set for constant with zero callers and outgoing calls")
	}
	if resp.VerifyHint == "" || len(resp.VerifyHint) < 10 {
		t.Errorf("VerifyHint too short: %q", resp.VerifyHint)
	}
}

func TestGraphVerifyHintFunctionZeroCallers(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "helper", Qualified: "helper",
			Kind: model.KindFunction, FileID: 1, LineStart: 10, LineEnd: 20,
		},
		File: model.File{Path: "lib/utils.go"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "fmt.Println"}},
		},
	}
	files := func(int64) (string, bool) { return "", false }

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if resp.VerifyHint == "" {
		t.Fatal("VerifyHint should be set for function with zero callers and outgoing calls")
	}
}

func TestGraphVerifyHintNotEmittedWithCallers(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "MY_CONST", Qualified: "MY_CONST",
			Kind: model.KindConstant, FileID: 1, LineStart: 1, LineEnd: 1,
		},
		File: model.File{Path: "lib/const.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "String#freeze"}},
		},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "Caller#use_const", FileID: 2}},
		},
	}
	files := func(id int64) (string, bool) {
		m := map[int64]string{1: "lib/const.rb", 2: "lib/caller.rb"}
		p, ok := m[id]
		return p, ok
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty when callers exist, got %q", resp.VerifyHint)
	}
}

func TestGraphVerifyHintNotEmittedForClass(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "User", Qualified: "User",
			Kind: model.KindClass, FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "Order"}},
		},
	}
	files := func(int64) (string, bool) { return "", false }

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty for class kind, got %q", resp.VerifyHint)
	}
}

func TestCategorizeEdgesInboundIncludesImportsTests(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/models/user.rb",
		2: "app/concerns/soft_deletable.rb",
		3: "src/utils.ts",
		4: "spec/models/user_spec.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "User", Qualified: "User",
			Kind: model.KindClass, FileID: 1, LineStart: 1, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeIncludes}, Target: model.Symbol{Qualified: "SoftDeletable", FileID: 2, LineStart: 1}},
			{Edge: model.Edge{Kind: model.EdgeImports}, Target: model.Symbol{Qualified: "utils", FileID: 3, LineStart: 1}},
			{Edge: model.Edge{Kind: model.EdgeTests, Confidence: 0.8}, Target: model.Symbol{FileID: 4}},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})

	if len(resp.Edges.Includes) != 1 || resp.Edges.Includes[0].Symbol != "SoftDeletable" {
		t.Errorf("Includes = %+v, want [SoftDeletable]", resp.Edges.Includes)
	}
	if resp.Edges.Includes[0].Ref != "app/concerns/soft_deletable.rb:1" {
		t.Errorf("Includes[0].Ref = %q, want %q", resp.Edges.Includes[0].Ref, "app/concerns/soft_deletable.rb:1")
	}
	if len(resp.Edges.Imports) != 1 || resp.Edges.Imports[0].Symbol != "utils" {
		t.Errorf("Imports = %+v, want [utils]", resp.Edges.Imports)
	}
	if len(resp.Edges.Tests) != 1 || resp.Edges.Tests[0].File != "spec/models/user_spec.rb" {
		t.Errorf("Tests = %+v, want [spec/models/user_spec.rb]", resp.Edges.Tests)
	}
}

func TestBuildGraphResponseMinConfidenceSurfacesHiddenCallers(t *testing.T) {
	// A method whose name is defined in several classes resolves its
	// implicit-receiver callers by bare-name fallback, stamped 0.3
	// (ConfidenceNameCollision) — below the default display floor. The default
	// request hides them and counts them in LowConfidenceHidden; a lowered
	// MinConfidence surfaces the same edges so an empty list is never mistaken
	// for "unused".
	files := func(id int64) (string, bool) {
		paths := map[int64]string{2: "app/controllers/posts_controller.rb"}
		p, ok := paths[id]
		return p, ok
	}
	newSC := func() *model.SymbolContext {
		return &model.SymbolContext{
			Symbol: model.Symbol{ID: 1, Name: "current_user", Qualified: "Authentication#current_user", Kind: model.KindMethod, FileID: 1, LineStart: 5},
			File:   model.File{Path: "app/controllers/concerns/authentication.rb"},
			Inbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.3, FileID: 2}, Target: model.Symbol{ID: 7, Qualified: "PostsController#show", FileID: 2, LineStart: 12}},
			},
		}
	}

	// Default floor (0.5): the 0.3 caller is hidden and counted.
	def := BuildGraphResponse(context.Background(), newSC(), files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(def.Edges.CalledBy) != 0 {
		t.Fatalf("default floor: CalledBy = %d, want 0", len(def.Edges.CalledBy))
	}
	if def.LowConfidenceHidden != 1 {
		t.Fatalf("default floor: LowConfidenceHidden = %d, want 1", def.LowConfidenceHidden)
	}

	// Lowered floor (0.3): the same caller is surfaced and no longer counted hidden.
	low := BuildGraphResponse(context.Background(), newSC(), files, BuildGraphRequest{Direction: model.DirectionCallers, MinConfidence: 0.3})
	if len(low.Edges.CalledBy) != 1 || low.Edges.CalledBy[0].Symbol != "PostsController#show" {
		t.Fatalf("lowered floor: CalledBy = %+v, want [PostsController#show]", low.Edges.CalledBy)
	}
	if low.LowConfidenceHidden != 0 {
		t.Errorf("lowered floor: LowConfidenceHidden = %d, want 0", low.LowConfidenceHidden)
	}
}

func TestBuildGraphRequestEdgeFloorDefaults(t *testing.T) {
	if got := (BuildGraphRequest{}).edgeFloor(); got != graphConfidenceFloor {
		t.Errorf("unset MinConfidence: edgeFloor = %g, want default %g", got, graphConfidenceFloor)
	}
	if got := (BuildGraphRequest{MinConfidence: 0.3}).edgeFloor(); got != 0.3 {
		t.Errorf("explicit MinConfidence: edgeFloor = %g, want 0.3", got)
	}
}

func TestBuildGraphResponseInboundOnlyTemporal(t *testing.T) {
	coChanges := 7
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "A", Qualified: "A", Kind: "class", FileID: 1},
		File:   model.File{Path: "a.rb"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeTemporal, Confidence: 0.6, Line: &coChanges},
				Target: model.Symbol{ID: 3, Qualified: "C", FileID: 3, LineStart: 10},
			},
		},
	}
	filePaths := map[int64]string{3: "c.rb"}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.Temporal) != 1 {
		t.Fatalf("Temporal = %d, want 1", len(resp.Edges.Temporal))
	}
	if resp.Edges.Temporal[0].Symbol != "C" {
		t.Errorf("Temporal[0].Symbol = %q, want C", resp.Edges.Temporal[0].Symbol)
	}
	if resp.Edges.Temporal[0].Ref != "c.rb:10" {
		t.Errorf("Temporal[0].Ref = %q, want c.rb:10", resp.Edges.Temporal[0].Ref)
	}
	if resp.Edges.Temporal[0].CoChanges != 7 {
		t.Errorf("Temporal[0].CoChanges = %d, want 7", resp.Edges.Temporal[0].CoChanges)
	}
}

func TestFormatRef(t *testing.T) {
	tests := []struct {
		file      string
		lineStart int
		want      string
	}{
		{"app/models/user.rb", 42, "app/models/user.rb:42"},
		{"app/models/user.rb", 0, ""},
		{"", 10, ""},
		{"", 0, ""},
	}
	for _, tc := range tests {
		got := FormatRef(tc.file, tc.lineStart)
		if got != tc.want {
			t.Errorf("FormatRef(%q, %d) = %q, want %q", tc.file, tc.lineStart, got, tc.want)
		}
	}
}

func TestFormatRefPtr(t *testing.T) {
	f := "lib/foo.rb"
	if got := FormatRefPtr(&f, 10); got != "lib/foo.rb:10" {
		t.Errorf("FormatRefPtr(&%q, 10) = %q, want %q", f, got, "lib/foo.rb:10")
	}
	if got := FormatRefPtr(nil, 10); got != "" {
		t.Errorf("FormatRefPtr(nil, 10) = %q, want empty", got)
	}
	if got := FormatRefPtr(&f, 0); got != "" {
		t.Errorf("FormatRefPtr(&%q, 0) = %q, want empty", f, got)
	}
}

func TestBuildGraphResponseRefField(t *testing.T) {
	filePaths := map[int64]string{
		1: "app/models/user.rb",
		2: "app/services/auth.rb",
		3: "app/concerns/soft_deletable.rb",
		4: "app/models/order.rb",
		5: "src/utils.ts",
		6: "app/jobs/export_cron.rb",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	coChanges := 5
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "User", Qualified: "User",
			Kind: model.KindClass, FileID: 1, LineStart: 10, LineEnd: 50,
		},
		File: model.File{Path: "app/models/user.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "Auth#login", FileID: 2, LineStart: 20}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.9}, Target: model.Symbol{Qualified: "External.api", FileID: 999}},
			{Edge: model.Edge{Kind: model.EdgeInherits, Confidence: 1.0}, Target: model.Symbol{Qualified: "Base", FileID: 0}},
			{Edge: model.Edge{Kind: model.EdgeComposes, Confidence: 1.0}, Target: model.Symbol{Qualified: "Order", FileID: 4, LineStart: 1}},
			{Edge: model.Edge{Kind: model.EdgeIncludes}, Target: model.Symbol{Qualified: "SoftDeletable", FileID: 3, LineStart: 5}},
			{Edge: model.Edge{Kind: model.EdgeImports}, Target: model.Symbol{Qualified: "utils", FileID: 5, LineStart: 1}},
			{Edge: model.Edge{Kind: model.EdgeTemporal, Confidence: 0.7, Line: &coChanges}, Target: model.Symbol{ID: 6, Qualified: "ExportCron", FileID: 6, LineStart: 3}},
		},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 1.0}, Target: model.Symbol{Qualified: "Controller#index", FileID: 2, LineStart: 55}},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	// Focal symbol ref
	if resp.Symbol.Ref != "app/models/user.rb:10" {
		t.Errorf("Symbol.Ref = %q, want %q", resp.Symbol.Ref, "app/models/user.rb:10")
	}

	// Calls — with file
	if resp.Edges.Calls[0].Ref != "app/services/auth.rb:20" {
		t.Errorf("Calls[0].Ref = %q, want %q", resp.Edges.Calls[0].Ref, "app/services/auth.rb:20")
	}
	// Calls — nil file (external)
	if resp.Edges.Calls[1].Ref != "" {
		t.Errorf("Calls[1].Ref = %q, want empty (nil file)", resp.Edges.Calls[1].Ref)
	}

	// CalledBy
	if resp.Edges.CalledBy[0].Ref != "app/services/auth.rb:55" {
		t.Errorf("CalledBy[0].Ref = %q, want %q", resp.Edges.CalledBy[0].Ref, "app/services/auth.rb:55")
	}

	// Composes
	if resp.Edges.Composes[0].Ref != "app/models/order.rb:1" {
		t.Errorf("Composes[0].Ref = %q, want %q", resp.Edges.Composes[0].Ref, "app/models/order.rb:1")
	}

	// Includes
	if resp.Edges.Includes[0].Ref != "app/concerns/soft_deletable.rb:5" {
		t.Errorf("Includes[0].Ref = %q, want %q", resp.Edges.Includes[0].Ref, "app/concerns/soft_deletable.rb:5")
	}

	// Imports
	if resp.Edges.Imports[0].Ref != "src/utils.ts:1" {
		t.Errorf("Imports[0].Ref = %q, want %q", resp.Edges.Imports[0].Ref, "src/utils.ts:1")
	}

	// Temporal
	if resp.Edges.Temporal[0].Ref != "app/jobs/export_cron.rb:3" {
		t.Errorf("Temporal[0].Ref = %q, want %q", resp.Edges.Temporal[0].Ref, "app/jobs/export_cron.rb:3")
	}
}

func TestGraphVerifyHintNotEmittedWithoutCallees(t *testing.T) {
	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "ORPHAN_CONST", Qualified: "ORPHAN_CONST",
			Kind: model.KindConstant, FileID: 1, LineStart: 1, LineEnd: 1,
		},
		File: model.File{Path: "lib/orphan.rb"},
	}
	files := func(int64) (string, bool) { return "", false }

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if resp.VerifyHint != "" {
		t.Errorf("VerifyHint should be empty for leaf symbol with no callees, got %q", resp.VerifyHint)
	}
}

func TestGraphCallSiteSnippet(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "package main\n\nimport \"fmt\"\n\nfunc caller() {\n\tfmt.Println(target())\n\treturn\n}\n")

	line := 6
	filePaths := map[int64]string{
		1: "target.go",
		2: "caller.go",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "target", Qualified: "target", Kind: "function", FileID: 1, LineStart: 1},
		File:   model.File{Path: "target.go"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 2, Line: &line},
				Target: model.Symbol{Qualified: "caller", FileID: 2, LineStart: 5, LineEnd: 7},
			},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	ref := resp.Edges.CalledBy[0]
	if ref.CallSite == nil {
		t.Fatal("CallSite is nil, want non-nil")
	}
	if ref.CallSite.Line != 6 {
		t.Errorf("CallSite.Line = %d, want 6", ref.CallSite.Line)
	}
	lines := splitLines(ref.CallSite.Snippet)
	if len(lines) != 5 {
		t.Errorf("snippet lines = %d, want 5; snippet:\n%s", len(lines), ref.CallSite.Snippet)
	}
}

func TestGraphCallSiteZeroContextSuppresses(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "package main\n\nfunc caller() {\n\ttarget()\n}\n")

	line := 4
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "caller.go", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "target", Qualified: "target", Kind: "function"},
		File:   model.File{Path: "target.go"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
				Target: model.Symbol{Qualified: "caller", FileID: 1, LineStart: 3},
			},
		},
	}

	snippets := NewSnippetReader(dir, 0)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	if resp.Edges.CalledBy[0].CallSite != nil {
		t.Error("CallSite should be nil when context_lines=0")
	}
}

func TestGraphCallSiteNoSnippetForNonCallEdges(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "parent.go", "package main\n\ntype Parent struct{}\n")

	line := 3
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "parent.go", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "Child", Qualified: "Child", Kind: "class"},
		File:   model.File{Path: "child.go"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 1, Line: &line},
				Target: model.Symbol{Qualified: "Parent", FileID: 1, LineStart: 3},
			},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Snippets: snippets})

	if len(resp.Edges.Inherits) != 1 {
		t.Fatalf("Inherits = %d, want 1", len(resp.Edges.Inherits))
	}
}

// TestGraphInboundImplementorsSurfaceInInheritedBy pins the cross-language
// "who inherits / implements this" path: when a trait or base class is
// the focal symbol, inbound EdgeInherits edges (implementors,
// subclasses) must surface in the InheritedBy bucket (subtypes),
// distinct from Inherits (supertypes). Before this was wired up,
// `sense graph` on a hub trait like axum's Handler returned empty
// inheritance even when impls were correctly indexed.
func TestGraphInboundImplementorsSurfaceInInheritedBy(t *testing.T) {
	files := func(id int64) (string, bool) {
		switch id {
		case 1:
			return "src/handler.rs", true
		case 2:
			return "src/router.rs", true
		case 3:
			return "src/layered.rs", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "Handler", Qualified: "Handler",
			Kind: model.KindInterface, FileID: 1, LineStart: 148, LineEnd: 205,
		},
		File: model.File{Path: "src/handler.rs"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 2},
				Target: model.Symbol{ID: 100, Qualified: "MethodRouter", FileID: 2, LineStart: 1355},
			},
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 3},
				Target: model.Symbol{ID: 101, Qualified: "Layered", FileID: 3, LineStart: 317},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.InheritedBy) != 2 {
		t.Fatalf("InheritedBy = %d, want 2 (MethodRouter, Layered)", len(resp.Edges.InheritedBy))
	}
	if len(resp.Edges.Inherits) != 0 {
		t.Fatalf("Inherits = %d, want 0 (implementors are subtypes, not supertypes)", len(resp.Edges.Inherits))
	}
	want := map[string]string{
		"MethodRouter": "src/router.rs:1355",
		"Layered":      "src/layered.rs:317",
	}
	for _, e := range resp.Edges.InheritedBy {
		ref, ok := want[e.Symbol]
		if !ok {
			t.Errorf("unexpected inherits entry %q", e.Symbol)
			continue
		}
		if e.Ref != ref {
			t.Errorf("%s ref = %q, want %q", e.Symbol, e.Ref, ref)
		}
	}
}

// TestGraphInheritsInboundSkipsUnresolvedSource pins the unresolved-
// source filter: inbound inherits edges whose source didn't resolve
// to an indexed symbol must not leak through as empty stubs. The
// canonical case is Rust blanket impls like `impl Trait for F` where
// the implementor F is a generic type parameter; sqlite/loadEdges
// returns the source-side row via LEFT JOIN, leaving Target.ID == 0
// and Qualified empty.
func TestGraphInheritsInboundSkipsUnresolvedSource(t *testing.T) {
	files := func(id int64) (string, bool) {
		if id == 2 {
			return "src/router.rs", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "Handler", Qualified: "Handler",
			Kind: model.KindInterface, FileID: 1, LineStart: 148,
		},
		File: model.File{Path: "src/handler.rs"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 1},
				Target: model.Symbol{ID: 0}, // unresolved blanket impl
			},
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 2},
				Target: model.Symbol{ID: 100, Qualified: "MethodRouter", FileID: 2, LineStart: 1355},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.InheritedBy) != 1 {
		t.Fatalf("InheritedBy = %d, want 1 (resolved-only, blanket impl dropped)", len(resp.Edges.InheritedBy))
	}
	if resp.Edges.InheritedBy[0].Symbol != "MethodRouter" {
		t.Errorf("InheritedBy[0] = %q, want MethodRouter", resp.Edges.InheritedBy[0].Symbol)
	}
}

// TestGraphInheritsInboundIncludedInCallersDirection checks that
// asking for direction=callers on a trait still surfaces implementors.
// The callers direction is the natural fit for "who depends on this
// symbol," so dropping inherits there would silently hide the answer.
func TestGraphInheritsInboundIncludedInCallersDirection(t *testing.T) {
	files := func(id int64) (string, bool) {
		if id == 2 {
			return "src/router.rs", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{
			Name: "Handler", Qualified: "Handler",
			Kind: model.KindInterface, FileID: 1, LineStart: 148,
		},
		File: model.File{Path: "src/handler.rs"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeInherits, Confidence: 1.0, FileID: 2},
				Target: model.Symbol{ID: 100, Qualified: "MethodRouter", FileID: 2, LineStart: 1355},
			},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})

	if len(resp.Edges.InheritedBy) != 1 {
		t.Fatalf("InheritedBy = %d under DirectionCallers, want 1", len(resp.Edges.InheritedBy))
	}

	raw, err := MarshalGraphCompactDirectional(resp, model.DirectionCallers)
	if err != nil {
		t.Fatalf("MarshalGraphCompactDirectional: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"inherited_by":[`)) {
		t.Errorf("compact callers output should include inherited_by bucket; got:\n%s", raw)
	}
	// inherits (supertypes) is outbound-only, pruned from a callers query.
	if bytes.Contains(raw, []byte(`"inherits":[`)) {
		t.Errorf("compact callers output should omit the outbound inherits bucket; got:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte(`"MethodRouter"`)) {
		t.Errorf("compact callers output should include MethodRouter; got:\n%s", raw)
	}
}

func TestGraphCallSiteMissingFile(t *testing.T) {
	dir := t.TempDir()

	line := 5
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "nonexistent.go", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "target", Qualified: "target", Kind: "function"},
		File:   model.File{Path: "target.go"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
				Target: model.Symbol{Qualified: "caller", FileID: 1, LineStart: 3},
			},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	if resp.Edges.CalledBy[0].CallSite != nil {
		t.Error("CallSite should be nil when source file doesn't exist")
	}
}

func TestGraphCallSiteReferencesEdge(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "ref.go", "package main\n\nvar x = CONSTANT\n")

	line := 3
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "ref.go", true
		}
		return "", false
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "CONSTANT", Qualified: "CONSTANT", Kind: "constant"},
		File:   model.File{Path: "const.go"},
		Inbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeReferences, Confidence: 1.0, FileID: 1, Line: &line},
				Target: model.Symbol{Qualified: "main.init", FileID: 1, LineStart: 1},
			},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	if len(resp.Edges.CalledBy) != 1 {
		t.Fatalf("CalledBy = %d, want 1", len(resp.Edges.CalledBy))
	}
	ref := resp.Edges.CalledBy[0]
	if ref.CallSite == nil {
		t.Fatal("CallSite is nil for references edge, want non-nil")
	}
	if ref.CallSite.Line != 3 {
		t.Errorf("CallSite.Line = %d, want 3", ref.CallSite.Line)
	}
}

func TestGraphSnippetsTruncated(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n")

	files := func(_ int64) (string, bool) {
		return "caller.go", true
	}

	var inbound []model.EdgeRef
	for i := 0; i < 15; i++ {
		line := i + 1
		inbound = append(inbound, model.EdgeRef{
			Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
			Target: model.Symbol{ID: int64(i + 10), Qualified: "caller" + string(rune('A'+i)), FileID: 1, LineStart: line},
		})
	}

	sc := &model.SymbolContext{
		Symbol:  model.Symbol{Name: "target", Qualified: "target", Kind: "function"},
		File:    model.File{Path: "target.go"},
		Inbound: inbound,
	}

	snippets := NewSnippetReader(dir, 1)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	if !resp.SnippetsTruncated {
		t.Error("SnippetsTruncated should be true when more edges than SnippetCap")
	}

	withSnippet := 0
	for _, ref := range resp.Edges.CalledBy {
		if ref.CallSite != nil {
			withSnippet++
		}
	}
	if withSnippet != SnippetCap {
		t.Errorf("snippets with content = %d, want %d", withSnippet, SnippetCap)
	}
}

func TestGraphCallSiteOutboundSnippet(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(hello())\n\treturn\n}\n")

	line := 6
	filePaths := map[int64]string{
		1: "main.go",
		2: "hello.go",
	}
	files := func(id int64) (string, bool) {
		p, ok := filePaths[id]
		return p, ok
	}

	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "main", Qualified: "main", Kind: "function", FileID: 1, LineStart: 5},
		File:   model.File{Path: "main.go"},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
				Target: model.Symbol{Qualified: "hello", FileID: 2, LineStart: 1, LineEnd: 3},
			},
		},
	}

	snippets := NewSnippetReader(dir, 2)
	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{
		Direction: model.DirectionCallees,
		Snippets:  snippets,
	})

	if len(resp.Edges.Calls) != 1 {
		t.Fatalf("Calls = %d, want 1", len(resp.Edges.Calls))
	}
	ref := resp.Edges.Calls[0]
	if ref.CallSite == nil {
		t.Fatal("CallSite is nil on outbound call, want non-nil")
	}
	if ref.CallSite.Line != 6 {
		t.Errorf("CallSite.Line = %d, want 6", ref.CallSite.Line)
	}
}

func TestFullGraphResponseSharedSnippetBudget(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "caller.go", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n13\n14\n15\n16\n17\n18\n19\n20\n")

	files := func(_ int64) (string, bool) {
		return "caller.go", true
	}

	var rootInbound []model.EdgeRef
	for i := 0; i < 8; i++ {
		line := i + 1
		rootInbound = append(rootInbound, model.EdgeRef{
			Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
			Target: model.Symbol{ID: int64(i + 10), Qualified: "caller" + string(rune('A'+i)), FileID: 1, LineStart: line},
		})
	}

	var layerInbound []model.EdgeRef
	for i := 0; i < 8; i++ {
		line := i + 11
		layerInbound = append(layerInbound, model.EdgeRef{
			Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0, FileID: 1, Line: &line},
			Target: model.Symbol{ID: int64(i + 20), Qualified: "layer" + string(rune('A'+i)), FileID: 1, LineStart: line},
		})
	}

	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol:  model.Symbol{Name: "target", Qualified: "target", Kind: "function"},
			File:    model.File{Path: "target.go"},
			Inbound: rootInbound,
		},
		Layers: []model.HopEdges{
			{Inbound: layerInbound},
		},
	}

	snippets := NewSnippetReader(dir, 1)
	resp := BuildFullGraphResponse(context.Background(), gr, files, BuildGraphRequest{
		Direction: model.DirectionCallers,
		Snippets:  snippets,
	})

	rootSnippets := 0
	for _, ref := range resp.Edges.CalledBy {
		if ref.CallSite != nil {
			rootSnippets++
		}
	}
	layerSnippets := 0
	if len(resp.Layers) > 0 {
		for _, ref := range resp.Layers[0].Edges.CalledBy {
			if ref.CallSite != nil {
				layerSnippets++
			}
		}
	}

	total := rootSnippets + layerSnippets
	if total > SnippetCap {
		t.Errorf("total snippets = %d (root=%d + layer=%d), want <= %d (shared budget)",
			total, rootSnippets, layerSnippets, SnippetCap)
	}
	if !resp.SnippetsTruncated {
		t.Error("SnippetsTruncated should be true when total edges exceed cap")
	}
}

// Usage edges (calls / references) below the confidence floor are dropped from
// the graph and tallied in LowConfidenceHidden — the 0.3 ERB/i18n noise the
// real Rails index produced for Listing::Item#price_amount.
func TestCategorizeEdgesHidesLowConfidenceUsageEdges(t *testing.T) {
	files := func(int64) (string, bool) { return "", false }
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "price_amount", Qualified: "Listing::Item#price_amount", Kind: "method"},
		File:   model.File{Path: "app/models/listing/item.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.7}, Target: model.Symbol{Qualified: "PriceValue#amount"}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.3}, Target: model.Symbol{Qualified: "i18n:admin.help.articles.new.breadcrumbs.new"}},
			{Edge: model.Edge{Kind: model.EdgeReferences, Confidence: 0.3}, Target: model.Symbol{Qualified: "i18n:account.wallet.currency"}},
		},
		Inbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.5}, Target: model.Symbol{Qualified: "Admin::ProductsController#generate_csv"}},
			{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.3}, Target: model.Symbol{Qualified: "Noise#caller"}},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.Calls) != 1 || resp.Edges.Calls[0].Symbol != "PriceValue#amount" {
		t.Errorf("Calls = %v, want only PriceValue#amount (0.3 i18n edges floored)", resp.Edges.Calls)
	}
	if len(resp.Edges.CalledBy) != 1 || resp.Edges.CalledBy[0].Symbol != "Admin::ProductsController#generate_csv" {
		t.Errorf("CalledBy = %v, want only the 0.5 caller (0.3 caller floored)", resp.Edges.CalledBy)
	}
	if resp.LowConfidenceHidden != 3 {
		t.Errorf("LowConfidenceHidden = %d, want 3 (2 outbound + 1 inbound below floor)", resp.LowConfidenceHidden)
	}
}

// The floor applies only to usage edges. Structural edges (inherits, composes,
// includes, imports) are syntactically explicit and are never confidence-floored.
func TestCategorizeEdgesDoesNotFloorStructuralEdges(t *testing.T) {
	files := func(int64) (string, bool) { return "", false }
	sc := &model.SymbolContext{
		Symbol: model.Symbol{Name: "User", Qualified: "User", Kind: "class"},
		File:   model.File{Path: "user.rb"},
		Outbound: []model.EdgeRef{
			{Edge: model.Edge{Kind: model.EdgeInherits, Confidence: 0.3}, Target: model.Symbol{Qualified: "Base"}},
			{Edge: model.Edge{Kind: model.EdgeComposes, Confidence: 0.3}, Target: model.Symbol{Qualified: "Order"}},
		},
	}

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

	if len(resp.Edges.Inherits) != 1 {
		t.Errorf("Inherits = %v, want 1 (structural edges are not floored)", resp.Edges.Inherits)
	}
	if len(resp.Edges.Composes) != 1 {
		t.Errorf("Composes = %v, want 1 (structural edges are not floored)", resp.Edges.Composes)
	}
	if resp.LowConfidenceHidden != 0 {
		t.Errorf("LowConfidenceHidden = %d, want 0 (no usage edges below floor)", resp.LowConfidenceHidden)
	}
}

// The floor is applied to every hop's edges, but LowConfidenceHidden tallies
// the root only — deeper hops drop their below-floor edges without counting.
func TestBuildFullGraphResponseFloorsLayersCountsRootOnly(t *testing.T) {
	files := func(int64) (string, bool) { return "", false }
	gr := &model.GraphResult{
		Root: model.SymbolContext{
			Symbol: model.Symbol{Name: "A", Qualified: "A", Kind: "method"},
			File:   model.File{Path: "a.rb"},
			Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.3}, Target: model.Symbol{Qualified: "rootNoise"}},
			},
		},
		Layers: []model.HopEdges{
			{Outbound: []model.EdgeRef{
				{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.3}, Target: model.Symbol{Qualified: "layerNoise"}},
				{Edge: model.Edge{Kind: model.EdgeCalls, Confidence: 0.9}, Target: model.Symbol{Qualified: "layerReal"}},
			}},
		},
	}

	resp := BuildFullGraphResponse(context.Background(), gr, files, BuildGraphRequest{})

	if len(resp.Edges.Calls) != 0 {
		t.Errorf("root Calls = %v, want 0 (0.3 floored)", resp.Edges.Calls)
	}
	if len(resp.Layers) != 1 || len(resp.Layers[0].Edges.Calls) != 1 || resp.Layers[0].Edges.Calls[0].Symbol != "layerReal" {
		t.Errorf("layer Calls = %v, want only layerReal (0.3 floored)", resp.Layers)
	}
	if resp.LowConfidenceHidden != 1 {
		t.Errorf("LowConfidenceHidden = %d, want 1 (root only, layer hides not counted)", resp.LowConfidenceHidden)
	}
}

func TestQualifiedOrName(t *testing.T) {
	if got := qualifiedOrName(model.Symbol{Qualified: "pkg.Foo", Name: "Foo"}); got != "pkg.Foo" {
		t.Errorf("got %q, want %q", got, "pkg.Foo")
	}
	if got := qualifiedOrName(model.Symbol{Name: "Bar"}); got != "Bar" {
		t.Errorf("got %q, want %q", got, "Bar")
	}
}

func TestCountEdgeSymbols(t *testing.T) {
	edges := GraphEdges{
		Calls:    []CallEdgeRef{{Symbol: "a"}, {Symbol: "b"}},
		CalledBy: []CallEdgeRef{{Symbol: "c"}},
		Tests:    []TestEdgeRef{{File: "test.go"}},
	}
	if got := countEdgeSymbols(edges); got != 4 {
		t.Errorf("countEdgeSymbols = %d, want 4", got)
	}
}

func TestCountEdgeSymbolsEmpty(t *testing.T) {
	if got := countEdgeSymbols(GraphEdges{}); got != 0 {
		t.Errorf("countEdgeSymbols(empty) = %d, want 0", got)
	}
}

func TestBuildGraphLayer(t *testing.T) {
	outbound := []model.EdgeRef{
		{
			Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
			Target: model.Symbol{Name: "Target", Qualified: "pkg.Target"},
		},
	}
	hop := model.HopEdges{Outbound: outbound}
	files := func(int64) (string, bool) { return "", false }
	req := BuildGraphRequest{Direction: model.DirectionBoth}

	layer := BuildGraphLayer(context.Background(), hop, 2, files, req)
	if layer.Depth != 2 {
		t.Errorf("Depth = %d, want 2", layer.Depth)
	}
	if len(layer.Edges.Calls) == 0 {
		t.Error("expected call edges in layer")
	}
}

func TestBuildFullGraphResponse(t *testing.T) {
	root := model.SymbolContext{
		Symbol: model.Symbol{
			Name: "Root", Qualified: "pkg.Root",
			Kind: "class", FileID: 1,
			LineStart: 1, LineEnd: 10,
		},
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
				Target: model.Symbol{Name: "Dep", Qualified: "pkg.Dep"},
			},
		},
	}
	layer := model.HopEdges{
		Outbound: []model.EdgeRef{
			{
				Edge:   model.Edge{Kind: model.EdgeCalls, Confidence: 1.0},
				Target: model.Symbol{Name: "Deep", Qualified: "pkg.Deep"},
			},
		},
	}
	gr := &model.GraphResult{
		Root:   root,
		Layers: []model.HopEdges{layer},
	}
	files := func(id int64) (string, bool) {
		if id == 1 {
			return "pkg/root.go", true
		}
		return "", false
	}
	req := BuildGraphRequest{Direction: model.DirectionBoth}

	resp := BuildFullGraphResponse(context.Background(), gr, files, req)
	if len(resp.Layers) != 1 {
		t.Errorf("Layers = %d, want 1", len(resp.Layers))
	}
	if resp.Layers[0].Depth != 2 {
		t.Errorf("Layer depth = %d, want 2", resp.Layers[0].Depth)
	}
}
