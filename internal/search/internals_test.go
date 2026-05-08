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
		{SymbolID: child1, Name: "Method1", Score: 0.9},
		{SymbolID: child2, Name: "Method2", Score: 0.8},
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
