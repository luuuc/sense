package mcpio

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/model"
)

func TestWatchStateSetGet(t *testing.T) {
	ws := &WatchState{}

	on, since := ws.Get()
	if on {
		t.Error("initial state should be off")
	}
	if !since.IsZero() {
		t.Error("initial since should be zero")
	}

	now := time.Now().UTC()
	ws.Set(true, now)

	on, since = ws.Get()
	if !on {
		t.Error("expected watching=true after Set")
	}
	if !since.Equal(now) {
		t.Errorf("since = %v, want %v", since, now)
	}

	ws.Set(false, time.Time{})
	on, _ = ws.Get()
	if on {
		t.Error("expected watching=false after Set(false)")
	}
}

func TestBuildDeadCodeResponse(t *testing.T) {
	symbols := []dead.Symbol{
		{Name: "Unused", Qualified: "pkg.Unused", File: "pkg/unused.go", LineStart: 10, LineEnd: 20, Kind: "function", Confidence: dead.ConfidenceDead},
		{Name: "Other", Qualified: "pkg.Other", File: "pkg/other.go", LineStart: 5, LineEnd: 8, Kind: "function", Confidence: dead.ConfidenceDead},
		{Name: "NoConf", Qualified: "pkg.NoConf", File: "pkg/unused.go", LineStart: 30, LineEnd: 35, Kind: "method"},
	}

	resp := BuildDeadCodeResponse(symbols, 100)

	if resp.DeadCount != 3 {
		t.Errorf("DeadCount = %d, want 3", resp.DeadCount)
	}
	if resp.TotalSymbols != 100 {
		t.Errorf("TotalSymbols = %d, want 100", resp.TotalSymbols)
	}
	if len(resp.DeadSymbols) != 3 {
		t.Fatalf("len(DeadSymbols) = %d, want 3", len(resp.DeadSymbols))
	}

	// Symbol with empty confidence should get ConfidenceDead default.
	if resp.DeadSymbols[2].Confidence != dead.ConfidenceDead {
		t.Errorf("empty confidence should default to %q, got %q", dead.ConfidenceDead, resp.DeadSymbols[2].Confidence)
	}

	// Two unique files.
	if resp.SenseMetrics.EstimatedFileReadsAvoided != 2 {
		t.Errorf("EstimatedFileReadsAvoided = %d, want 2", resp.SenseMetrics.EstimatedFileReadsAvoided)
	}
}

func TestBuildDeadCodeResponseEmpty(t *testing.T) {
	resp := BuildDeadCodeResponse(nil, 50)
	if resp.DeadCount != 0 {
		t.Errorf("DeadCount = %d, want 0", resp.DeadCount)
	}
	if resp.TotalSymbols != 50 {
		t.Errorf("TotalSymbols = %d, want 50", resp.TotalSymbols)
	}
}

func TestMarshalStatusNilStructure(t *testing.T) {
	resp := StatusResponse{
		Languages: nil,
		NextSteps: nil,
	}

	raw, err := MarshalStatus(resp)
	if err != nil {
		t.Fatalf("MarshalStatus: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	langs, ok := out["languages"]
	if !ok {
		t.Fatal("missing languages key")
	}
	if langs == nil {
		t.Error("languages should be {} not null")
	}
}

func TestMarshalStatusWithStructure(t *testing.T) {
	resp := StatusResponse{
		Languages: map[string]StatusLanguage{"go": {Files: 10, Symbols: 100}},
		Structure: &StatusStructure{
			TopNamespaces: nil,
			HubSymbols:    nil,
			EntryPoints:   nil,
		},
		NextSteps: nil,
	}

	raw, err := MarshalStatus(resp)
	if err != nil {
		t.Fatalf("MarshalStatus: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	structure, ok := out["structure"].(map[string]any)
	if !ok {
		t.Fatal("missing structure")
	}
	if structure["top_namespaces"] == nil {
		t.Error("top_namespaces should be [] not null")
	}
}

func TestSegmentBlastCallers(t *testing.T) {
	resp := &BlastResponse{
		DirectCallers: []BlastCaller{
			{File: "app/service.go"},
			{File: "app/service_test.go"},
		},
		IndirectCallers: []BlastIndirect{
			{Symbol: "helper", Via: "service", Hops: 2},
		},
		AffectedSubclasses: []BlastCaller{
			{File: "test/sub_test.go"},
		},
		AffectedViaComposition: []BlastCaller{
			{File: "app/composer.go"},
		},
		AffectedViaIncludes: []BlastCaller{
			{File: "test/includes_test.go"},
		},
		AffectedTests: []string{
			"test/something_test.go",
		},
	}

	segmentBlastCallers(resp)

	// Prod: service.go (direct) + 1 indirect + composer.go (composition) = 3
	// Test: service_test.go (direct) + sub_test.go (subclass) + includes_test.go (includes) + something_test.go (tests) = 4
	if resp.ProductionAffected != 3 {
		t.Errorf("ProductionAffected = %d, want 3", resp.ProductionAffected)
	}
	if resp.TestAffected != 4 {
		t.Errorf("TestAffected = %d, want 4", resp.TestAffected)
	}
}

func TestRiskRank(t *testing.T) {
	cases := []struct {
		risk string
		want int
	}{
		{"high", 3},
		{"medium", 2},
		{"low", 1},
		{"unknown", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := riskRank(c.risk); got != c.want {
			t.Errorf("riskRank(%q) = %d, want %d", c.risk, got, c.want)
		}
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

	layer := BuildGraphLayer(hop, 2, files, req)
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

	resp := BuildFullGraphResponse(gr, files, req)
	if len(resp.Layers) != 1 {
		t.Errorf("Layers = %d, want 1", len(resp.Layers))
	}
	if resp.Layers[0].Depth != 2 {
		t.Errorf("Layer depth = %d, want 2", resp.Layers[0].Depth)
	}
}

func TestMarshalDeadCodeNilSlices(t *testing.T) {
	resp := DeadCodeResponse{
		DeadSymbols: nil,
		NextSteps:   nil,
	}
	raw, err := MarshalDeadCode(resp)
	if err != nil {
		t.Fatalf("MarshalDeadCode: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out["dead_symbols"] == nil {
		t.Error("dead_symbols should be [] not null")
	}
}

func TestEstimateJSONTokensEmpty(t *testing.T) {
	if got := estimateJSONTokens([]byte("{}")); got != 1 {
		t.Errorf("estimateJSONTokens({}) = %d, want 1", got)
	}
}
