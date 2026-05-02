package mcpio

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
)

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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{})

	if len(resp.Edges.Composes) != 3 {
		t.Fatalf("Composes = %d, want 3 (2 outbound + 1 inbound)", len(resp.Edges.Composes))
	}
	if resp.Edges.Composes[0].Symbol != "Order" {
		t.Errorf("Composes[0].Symbol = %q, want %q", resp.Edges.Composes[0].Symbol, "Order")
	}
	if resp.Edges.Composes[0].File == nil {
		t.Error("Composes[0].File = nil, want non-nil")
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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{Direction: model.DirectionCallees})
	if len(resp.Edges.Composes) != 1 || resp.Edges.Composes[0].Symbol != "Order" {
		t.Errorf("callees direction: want only outbound Order, got %v", resp.Edges.Composes)
	}

	resp = BuildGraphResponse(sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
	if len(resp.Edges.Composes) != 1 || resp.Edges.Composes[0].Symbol != "Profile" {
		t.Errorf("callers direction: want only inbound Profile, got %v", resp.Edges.Composes)
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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{})

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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{})

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
	resp := BuildGraphResponse(sc, files, BuildGraphRequest{})
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
		resp := BuildGraphResponse(sc, files, BuildGraphRequest{Direction: dir})
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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: true})

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
	resp = BuildGraphResponse(sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: false})
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

	resp := BuildGraphResponse(sc, files, BuildGraphRequest{Direction: model.DirectionCallers, SegmentCallers: true})

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
	resp := BuildGraphResponse(sc, files, BuildGraphRequest{})
	if resp.SenseMetrics.SymbolsReturned != 1 {
		t.Errorf("SymbolsReturned = %d, want 1", resp.SenseMetrics.SymbolsReturned)
	}
	if resp.SenseMetrics.EstimatedFileReadsAvoided != 1 {
		t.Errorf("EstimatedFileReadsAvoided = %d, want 1", resp.SenseMetrics.EstimatedFileReadsAvoided)
	}
}
