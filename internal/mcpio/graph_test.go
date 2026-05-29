package mcpio

import (
	"bytes"
	"context"
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

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{})

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

	resp := BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallees})
	if len(resp.Edges.Composes) != 1 || resp.Edges.Composes[0].Symbol != "Order" {
		t.Errorf("callees direction: want only outbound Order, got %v", resp.Edges.Composes)
	}

	resp = BuildGraphResponse(context.Background(), sc, files, BuildGraphRequest{Direction: model.DirectionCallers})
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

// TestGraphInheritsInboundExposesImplementors pins the cross-language
// "who inherits / implements this" path: when a trait or base class is
// the focal symbol, inbound EdgeInherits edges (implementors,
// subclasses) must surface in the Inherits bucket. Before this was
// wired up, `sense graph` on a hub trait like axum's Handler returned
// empty inherits even when impls were correctly indexed.
func TestGraphInheritsInboundExposesImplementors(t *testing.T) {
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

	if len(resp.Edges.Inherits) != 2 {
		t.Fatalf("Inherits = %d, want 2 (MethodRouter, Layered)", len(resp.Edges.Inherits))
	}
	want := map[string]string{
		"MethodRouter": "src/router.rs:1355",
		"Layered":      "src/layered.rs:317",
	}
	for _, e := range resp.Edges.Inherits {
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

	if len(resp.Edges.Inherits) != 1 {
		t.Fatalf("Inherits = %d, want 1 (resolved-only, blanket impl dropped)", len(resp.Edges.Inherits))
	}
	if resp.Edges.Inherits[0].Symbol != "MethodRouter" {
		t.Errorf("Inherits[0] = %q, want MethodRouter", resp.Edges.Inherits[0].Symbol)
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

	if len(resp.Edges.Inherits) != 1 {
		t.Fatalf("Inherits = %d under DirectionCallers, want 1", len(resp.Edges.Inherits))
	}

	raw, err := MarshalGraphCompactDirectional(resp, model.DirectionCallers)
	if err != nil {
		t.Fatalf("MarshalGraphCompactDirectional: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"inherits":[`)) {
		t.Errorf("compact callers output should include inherits bucket; got:\n%s", raw)
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
		Symbol: model.Symbol{Name: "target", Qualified: "target", Kind: "function"},
		File:   model.File{Path: "target.go"},
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
