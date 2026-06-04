package search

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestExpandQueryIdenticalSplit(t *testing.T) {
	// "hello world" splits into "hello world" (identical) -> no second sub-query
	got := expandQuery("hello world")
	if len(got) != 1 {
		t.Errorf("expandQuery(\"hello world\") = %v, want 1 sub-query (no duplicate)", got)
	}
}

func TestExpandQueryLong5Words(t *testing.T) {
	got := expandQuery("one two three four five")
	// Should have original + ident split (same) + short 3-word variant
	foundShort := false
	for _, q := range got {
		if q == "one two three" {
			foundShort = true
		}
	}
	if !foundShort {
		t.Errorf("expected short variant 'one two three' in %v", got)
	}
}

func TestExpandQueryShortMatchesIdent(t *testing.T) {
	// "UserAuth something else more" -> identTokens="user auth something else more"
	// short = "UserAuth something else" (first 3 words)
	got := expandQuery("UserAuth something else more")
	if len(got) > 3 {
		t.Errorf("expandQuery should cap at 3, got %d", len(got))
	}
}

func TestSplitIdentifiersEmpty(t *testing.T) {
	got := splitIdentifiers("")
	if got != "" {
		t.Errorf("splitIdentifiers(\"\") = %q, want \"\"", got)
	}
}

func TestSplitIdentifiersHyphenSeparator(t *testing.T) {
	got := splitIdentifiers("user-profile-handler")
	if got != "user profile handler" {
		t.Errorf("splitIdentifiers(\"user-profile-handler\") = %q, want \"user profile handler\"", got)
	}
}

func TestSplitIdentifiersDotSeparator(t *testing.T) {
	got := splitIdentifiers("com.example.service")
	if got != "com example service" {
		t.Errorf("splitIdentifiers(\"com.example.service\") = %q, want \"com example service\"", got)
	}
}

func TestSplitCamelCaseEmpty(t *testing.T) {
	got := splitCamelCase("")
	if got != nil {
		t.Errorf("splitCamelCase(\"\") = %v, want nil", got)
	}
}

func TestSplitCamelCaseAllLower(t *testing.T) {
	got := splitCamelCase("simple")
	if len(got) != 1 || got[0] != "simple" {
		t.Errorf("splitCamelCase(\"simple\") = %v, want [\"simple\"]", got)
	}
}

func TestMergeMultiQueryOverlappingCoverage(t *testing.T) {
	r1 := []Result{
		{SymbolID: 1, Name: "A", Score: 0.9},
		{SymbolID: 2, Name: "B", Score: 0.8},
	}
	r2 := []Result{
		{SymbolID: 2, Name: "B", Score: 0.7},
		{SymbolID: 3, Name: "C", Score: 0.6},
	}
	got := mergeMultiQuery([][]Result{r1, r2})
	if len(got) != 3 {
		t.Errorf("mergeMultiQuery returned %d results, want 3", len(got))
	}
	// Symbol 2 (B) should have boosted score from appearing in both
	for _, r := range got {
		if r.SymbolID == 2 && r.Score <= 0 {
			t.Error("symbol B should have positive score from merge")
		}
	}
}

func TestMergeMultiQueryEmpty(t *testing.T) {
	got := mergeMultiQuery(nil)
	if len(got) != 0 {
		t.Errorf("mergeMultiQuery(nil) = %d results, want 0", len(got))
	}
}

func TestMergeMultiQuerySingle(t *testing.T) {
	r := []Result{
		{SymbolID: 1, Name: "A", Score: 0.9},
		{SymbolID: 2, Name: "B", Score: 0.8},
	}
	got := mergeMultiQuery([][]Result{r})
	if len(got) != 2 {
		t.Errorf("mergeMultiQuery single list = %d results, want 2", len(got))
	}
}

func openTestDB(t *testing.T) *sqlite.Adapter {
	t.Helper()
	a, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func TestEnrichFromGraphInjectsGraphSource(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	fid, err := a.WriteFile(ctx, &model.File{Path: "h.go", Language: "go", Hash: "h1", Symbols: 2, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	hub, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Hub", Qualified: "pkg.Hub", Kind: "function", LineStart: 1, LineEnd: 5})
	if err != nil {
		t.Fatal(err)
	}
	callee, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Leaf", Qualified: "pkg.Leaf", Kind: "function", LineStart: 6, LineEnd: 9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteEdge(ctx, &model.Edge{SourceID: &hub, TargetID: callee, Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0}); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	// Only the hub is a search hit; the callee is absent and must be
	// injected by graph enrichment with SourceGraph provenance.
	results := []Result{{SymbolID: hub, Name: "Hub", Qualified: "pkg.Hub", Score: 1.0, Source: SourceKeyword}}
	out, err := e.enrichFromGraph(ctx, results)
	if err != nil {
		t.Fatalf("enrichFromGraph: %v", err)
	}
	var found bool
	for _, r := range out {
		if r.SymbolID == callee {
			found = true
			if r.Source != SourceGraph {
				t.Errorf("injected callee source = %q, want %q", r.Source, SourceGraph)
			}
		}
	}
	if !found {
		t.Fatal("expected callee to be injected by enrichment")
	}
}

func TestSearchPropagatesAdapterError(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close() // closed adapter → the prepare phase's first query fails

	e := NewEngine(a, nil, nil)
	if _, _, err := e.Search(ctx, Options{Query: "anything", Limit: 5}); err == nil {
		t.Fatal("Search on closed adapter: want error, got nil")
	}
}

// Each pipeline phase surfaces an adapter error rather than swallowing it.
// Exercised against a closed adapter so the first DB call inside each phase
// fails, covering the error-propagation branch the decomposition isolated.
func TestSearchPhasesPropagateErrors(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Close()
	e := NewEngine(a, nil, nil)

	t.Run("fuseQueries", func(t *testing.T) {
		sc := &searchContext{queries: []string{"x"}, candidateLimit: 50}
		if _, _, _, err := e.fuseQueries(ctx, Options{}, sc); err == nil {
			t.Fatal("want error from keyword retrieval on closed adapter")
		}
	})
	t.Run("rankResults", func(t *testing.T) {
		// A result with no Qualified forces hydrateResults to hit the adapter.
		fused := []Result{{SymbolID: 1}}
		if err := e.rankResults(ctx, Options{}, &searchContext{}, fused); err == nil {
			t.Fatal("want error from hydrate on closed adapter")
		}
	})
	t.Run("enrichResults", func(t *testing.T) {
		fused := []Result{{SymbolID: 1, Qualified: "pkg.A"}}
		if _, err := e.enrichResults(ctx, Options{Limit: 5}, fused); err == nil {
			t.Fatal("want error from graph enrichment on closed adapter")
		}
	})
}

func TestSubstringFallbackEarlyReturn(t *testing.T) {
	// At or above the threshold, keyword results are returned untouched
	// without consulting the substring index.
	e := NewEngine(nil, nil, nil)
	kw := make([]sqlite.SearchResult, substringFallbackThreshold)
	for i := range kw {
		kw[i] = sqlite.SearchResult{SymbolID: int64(i + 1)}
	}
	got := e.substringFallback(context.Background(), kw, "anything", "", 10)
	if len(got) != substringFallbackThreshold {
		t.Errorf("len = %d, want %d (unchanged)", len(got), substringFallbackThreshold)
	}
}

func TestSubstringFallbackMergesMatches(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)
	fid, err := a.WriteFile(ctx, &model.File{Path: "w.go", Language: "go", Hash: "h1", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	sid, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "ZebraWidget", Qualified: "pkg.ZebraWidget", Kind: "function", LineStart: 1, LineEnd: 2})
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	// Below threshold and empty → substring search supplies the match.
	got := e.substringFallback(ctx, nil, "Zebra", "", 10)
	var found bool
	for _, r := range got {
		if r.SymbolID == sid {
			found = true
		}
	}
	if !found {
		t.Errorf("substring fallback did not surface ZebraWidget; got %+v", got)
	}
}

func TestSearchRanksImplementationAboveItsTest(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	implFile, err := a.WriteFile(ctx, &model.File{Path: "internal/parse/parse.go", Language: "go", Hash: "h1", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	testFile, err := a.WriteFile(ctx, &model.File{Path: "internal/parse/parse_test.go", Language: "go", Hash: "h2", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{FileID: implFile, Name: "ParseConfig", Qualified: "parse.ParseConfig", Kind: "function", LineStart: 1, LineEnd: 5, Snippet: "func ParseConfig(path string) (*Config, error)"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{FileID: testFile, Name: "TestParseConfig", Qualified: "parse_test.TestParseConfig", Kind: "function", LineStart: 1, LineEnd: 5, Snippet: "func TestParseConfig(t *testing.T)"}); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	results, _, err := e.Search(ctx, Options{Query: "parse config", Limit: 10})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	implPos, testPos := -1, -1
	for i, r := range results {
		switch r.Name {
		case "ParseConfig":
			implPos = i
		case "TestParseConfig":
			testPos = i
		}
	}
	if implPos == -1 {
		t.Fatal("implementation ParseConfig not in results")
	}
	if testPos != -1 && implPos > testPos {
		t.Errorf("implementation ranked %d, below its test at %d — demotion failed", implPos, testPos)
	}
}

func TestPromoteParentsEmpty(t *testing.T) {
	e := NewEngine(nil, nil, nil)
	out, err := e.promoteParents(context.Background(), nil, 10)
	if err != nil {
		t.Fatalf("promoteParents empty: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 results, got %d", len(out))
	}
}

func TestPromoteParentsNoParents(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	fid, err := a.WriteFile(ctx, &model.File{Path: "test.go", Language: "go", Hash: "h1", Symbols: 1, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	sid, err := a.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Foo", Qualified: "pkg.Foo", Kind: "function", LineStart: 1, LineEnd: 5})
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	results := []Result{{SymbolID: sid, Name: "Foo", Score: 0.9}}
	out, err := e.promoteParents(ctx, results, 10)
	if err != nil {
		t.Fatalf("promoteParents: %v", err)
	}
	if len(out) != 1 || out[0].SymbolID != sid {
		t.Errorf("expected unchanged results, got %+v", out)
	}
}

func TestPromoteParentsPromotes(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	fid, err := a.WriteFile(ctx, &model.File{Path: "test.go", Language: "go", Hash: "h1", Symbols: 3, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	parentID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "MyClass", Qualified: "pkg.MyClass", Kind: "class", LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	child1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Method1", Qualified: "pkg.MyClass.Method1", Kind: "method",
		LineStart: 2, LineEnd: 3, ParentID: &parentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child2, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Method2", Qualified: "pkg.MyClass.Method2", Kind: "method",
		LineStart: 4, LineEnd: 5, ParentID: &parentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	results := []Result{
		{SymbolID: child1, Name: "Method1", Score: 0.9, Source: SourceVector},
		{SymbolID: child2, Name: "Method2", Score: 0.8, Source: SourceKeyword},
	}
	out, err := e.promoteParents(ctx, results, 10)
	if err != nil {
		t.Fatalf("promoteParents: %v", err)
	}

	if len(out) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out))
	}
	if out[0].SymbolID != parentID {
		t.Errorf("expected parent %d, got %d", parentID, out[0].SymbolID)
	}
	if out[0].Name != "MyClass" {
		t.Errorf("expected name MyClass, got %q", out[0].Name)
	}
	// The promoted parent inherits the provenance of the highest-scoring
	// child (Method1, vector-sourced) whose score it takes.
	if out[0].Source != SourceVector {
		t.Errorf("promoted parent source = %q, want %q", out[0].Source, SourceVector)
	}
}

func TestPromoteParentsThresholdNotMet(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	fid, err := a.WriteFile(ctx, &model.File{Path: "test.go", Language: "go", Hash: "h1", Symbols: 2, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	parentID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "MyClass", Qualified: "pkg.MyClass", Kind: "class",
		LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	child1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Method1", Qualified: "pkg.MyClass.Method1", Kind: "method",
		LineStart: 2, LineEnd: 3, ParentID: &parentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	results := []Result{{SymbolID: child1, Name: "Method1", Score: 0.9}}
	out, err := e.promoteParents(ctx, results, 10)
	if err != nil {
		t.Fatalf("promoteParents: %v", err)
	}

	if len(out) != 1 || out[0].SymbolID != child1 {
		t.Errorf("expected unchanged results, got %+v", out)
	}
}

func TestPromoteParentsSkipsExistingParent(t *testing.T) {
	ctx := context.Background()
	a := openTestDB(t)

	fid, err := a.WriteFile(ctx, &model.File{Path: "test.go", Language: "go", Hash: "h1", Symbols: 4, IndexedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}

	parentID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "MyClass", Qualified: "pkg.MyClass", Kind: "class",
		LineStart: 1, LineEnd: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	child1, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Method1", Qualified: "pkg.MyClass.Method1", Kind: "method",
		LineStart: 2, LineEnd: 3, ParentID: &parentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	child2, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Method2", Qualified: "pkg.MyClass.Method2", Kind: "method",
		LineStart: 4, LineEnd: 5, ParentID: &parentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	e := NewEngine(a, nil, nil)
	results := []Result{
		{SymbolID: parentID, Name: "MyClass", Score: 0.95},
		{SymbolID: child1, Name: "Method1", Score: 0.9},
		{SymbolID: child2, Name: "Method2", Score: 0.8},
	}
	out, err := e.promoteParents(ctx, results, 10)
	if err != nil {
		t.Fatalf("promoteParents: %v", err)
	}

	// Parent already in results — children should not be replaced
	if len(out) != 3 {
		t.Fatalf("expected 3 results (parent already present), got %d", len(out))
	}
}
