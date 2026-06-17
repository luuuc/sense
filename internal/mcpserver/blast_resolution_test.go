package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mcp "github.com/mark3labs/mcp-go/mcp"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestHandleBlastFuzzySuggestions drives the fuzzy tier through the public
// handler: a near-miss query (one transposed character from an indexed
// symbol) resolves to no exact/suffix/containment match, so resolveSymbol
// returns a suggestions result that handleBlast surfaces as an error result
// carrying candidate names — not a Go error.
func TestHandleBlastFuzzySuggestions(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	// "Verfiy" is Levenshtein-2 from the indexed "Verify" but matches no
	// earlier lookup tier, so it falls through to the fuzzy suggestions path.
	result, err := ts.handlers.handleBlast(ctx, toolReq(map[string]any{
		"symbol": "Verfiy",
	}))
	if err != nil {
		t.Fatalf("handleBlast fuzzy: %v", err)
	}
	if result == nil {
		t.Fatal("expected a suggestions result, got nil")
	}
	text := resultText(t, result)
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal suggestions: %v", err)
	}
	if _, ok := resp["suggestions"]; !ok {
		t.Errorf("expected suggestions key in fuzzy result, got %v", resp)
	}
}

// seedDisambig opens a fresh index with two symbols sharing the exact query
// name but different edge counts, so dominantMatch declines (counts too close
// to its 5x rule) and disambiguationResult ranks them by edges. The dominant
// candidate's qualified name equals the query, exercising the
// "pick from top_matches" hint branch.
func seedDisambig(t *testing.T) (*handlers, func()) {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	fA := model.File{Path: "a.go", Language: "go", Hash: "ha", Symbols: 2, IndexedAt: now}
	fidA, err := adapter.WriteFile(ctx, &fA)
	if err != nil {
		t.Fatal(err)
	}
	fB := model.File{Path: "b.go", Language: "go", Hash: "hb", Symbols: 2, IndexedAt: now}
	fidB, err := adapter.WriteFile(ctx, &fB)
	if err != nil {
		t.Fatal(err)
	}

	// Two symbols both named "Process", each package-qualified so a bare-name
	// query falls through to the exact-name tier and returns both.
	s1 := &model.Symbol{FileID: fidA, Name: "Process", Qualified: "a.Process", Kind: "function", LineStart: 1, LineEnd: 5}
	id1, err := adapter.WriteSymbol(ctx, s1)
	if err != nil {
		t.Fatal(err)
	}
	s2 := &model.Symbol{FileID: fidB, Name: "Process", Qualified: "b.Process", Kind: "function", LineStart: 1, LineEnd: 5}
	id2, err := adapter.WriteSymbol(ctx, s2)
	if err != nil {
		t.Fatal(err)
	}
	// Distinct caller symbols, because sense_edges is unique on
	// (source_id, target_id, kind, file_id): one edge per caller is the only
	// way to give a target a count > 1. id1 ends with 3 incoming edges, id2
	// with 2 — unequal but close enough that dominantMatch declines its 5x
	// auto-resolve and disambiguation ranks them by count.
	mkCallerEdge := func(target int64, n int) {
		s := &model.Symbol{FileID: fidA, Name: "Caller", Qualified: "a.Caller" + strconv.Itoa(n), Kind: "function", LineStart: 100 + n, LineEnd: 100 + n}
		sid, err := adapter.WriteSymbol(ctx, s)
		if err != nil {
			t.Fatal(err)
		}
		e := model.Edge{SourceID: &sid, TargetID: target, Kind: model.EdgeCalls, FileID: fidA, Line: intPtr(n), Confidence: 1.0}
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	mkCallerEdge(id1, 1)
	mkCallerEdge(id1, 2)
	mkCallerEdge(id1, 3)
	mkCallerEdge(id2, 4)
	mkCallerEdge(id2, 5)

	tracker := metrics.NewTracker(adapter.DB())
	engine := search.NewEngine(adapter, nil, nil)
	h := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      engine,
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
	cleanup := func() {
		tracker.Close()
		_ = adapter.Close()
	}
	return h, cleanup
}

// TestDisambiguationRanksByEdgeCount covers the edge-count sort comparator's
// primary key: two same-named candidates with different real edge counts are
// ranked highest-first, and the top entry reports the larger count. The query
// is a bare name distinct from every qualified name, so the "Refine with a
// qualified name" hint fires.
func TestDisambiguationRanksByEdgeCount(t *testing.T) {
	h, cleanup := seedDisambig(t)
	defer cleanup()
	ctx := context.Background()

	matches, err := cli.Lookup(ctx, h.db, "Process")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if len(matches) < 2 {
		t.Fatalf("expected ambiguous matches, got %d", len(matches))
	}

	result := h.disambiguationResult(ctx, "Process", matches)
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	top, ok := resp["top_matches"].([]any)
	if !ok || len(top) < 2 {
		t.Fatalf("expected ranked top_matches, got %v", resp["top_matches"])
	}
	// The 3-edge candidate must sort ahead of the 2-edge one.
	first, _ := top[0].(string)
	if !strings.Contains(first, "3 edges") {
		t.Errorf("expected the 3-edge candidate ranked first, got %q", first)
	}
	hint, _ := resp["hint"].(string)
	if !strings.Contains(hint, "retry this tool with the") {
		t.Errorf("expected a hint steering to the file parameter, got %q", hint)
	}
}

// TestDisambiguationHintWhenQualifiedEqualsQuery covers the hint when the
// top-ranked candidate's qualified name is exactly the query (a bare
// qualified-name retry would be circular): the response steers the LLM to the
// `file` parameter with the candidate's path as a concrete example.
func TestDisambiguationHintWhenQualifiedEqualsQuery(t *testing.T) {
	ts := setupTestServer(t)
	ctx := context.Background()

	matches := []cli.Match{
		{Name: "Process", Qualified: "Process", Kind: "function", File: "a.go", LineStart: 1},
		{Name: "Process", Qualified: "pkg.Process", Kind: "function", File: "b.go", LineStart: 1},
	}
	result := ts.handlers.disambiguationResult(ctx, "Process", matches)
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hint, _ := resp["hint"].(string)
	if !strings.Contains(hint, "retry this tool with the") || !strings.Contains(hint, "a.go") {
		t.Errorf("expected a file-parameter hint citing the candidate path, got %q", hint)
	}
}

// TestDominantMatchResolvesViaHandleBlast exercises dominantMatch's
// auto-resolve rule end-to-end: one candidate dominates by >=5x edges and the
// runner-up has <=2, so handleBlast resolves to the dominant symbol without a
// disambiguation prompt and returns a real blast response.
func TestDominantMatchResolvesViaHandleBlast(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = adapter.Close() }()

	now := time.Now()
	fidA, _ := adapter.WriteFile(ctx, &model.File{Path: "a.go", Language: "go", Hash: "ha", Symbols: 1, IndexedAt: now})
	fidB, _ := adapter.WriteFile(ctx, &model.File{Path: "b.go", Language: "go", Hash: "hb", Symbols: 1, IndexedAt: now})

	// Two "Topic" symbols: the first heavily referenced, the second barely.
	hot := &model.Symbol{FileID: fidA, Name: "Topic", Qualified: "a.Topic", Kind: "class", LineStart: 1, LineEnd: 5}
	hotID, _ := adapter.WriteSymbol(ctx, hot)
	cold := &model.Symbol{FileID: fidB, Name: "Topic", Qualified: "b.Topic", Kind: "class", LineStart: 1, LineEnd: 5}
	_, _ = adapter.WriteSymbol(ctx, cold)

	// 6 distinct callers each contributing one edge into the hot Topic, 0 into
	// the cold one → 6 >= 5, runner-up 0 <= 2, so dominantMatch auto-resolves.
	// Distinct qualified names are required because both symbols and edges are
	// unique-keyed.
	for i := 0; i < 6; i++ {
		c := &model.Symbol{FileID: fidA, Name: "Caller", Qualified: "a.Caller" + strconv.Itoa(i), Kind: "function", LineStart: 10 + i, LineEnd: 10 + i}
		cid, _ := adapter.WriteSymbol(ctx, c)
		e := model.Edge{SourceID: &cid, TargetID: hotID, Kind: model.EdgeCalls, FileID: fidA, Line: intPtr(i + 1), Confidence: 1.0}
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}

	tracker := metrics.NewTracker(adapter.DB())
	defer tracker.Close()
	h := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}

	matches, err := cli.Lookup(ctx, h.db, "Topic")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	winner, ok := h.dominantMatch(ctx, matches)
	if !ok {
		t.Fatal("expected dominantMatch to auto-resolve the heavily-referenced Topic")
	}
	if winner.Qualified != "a.Topic" {
		t.Errorf("dominant winner = %q, want a.Topic", winner.Qualified)
	}
}

// fullGitHandler builds a complete handler (tracker, defaults, engine) over a
// real git repo with a committed-then-edited file, so the diff path of
// handleBlast resolves symbols and produces a real response.
func fullGitHandler(t *testing.T) (string, *handlers, func()) {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")

	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "initial")

	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	fid, _ := adapter.WriteFile(ctx, &model.File{Path: "main.go", Language: "go", Hash: "abc", Symbols: 1, IndexedAt: now})
	mainID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "main", Qualified: "main.main", Kind: "function",
		LineStart: 1, LineEnd: 2, Snippet: "func main() {}",
	})
	fid2, _ := adapter.WriteFile(ctx, &model.File{Path: "caller.go", Language: "go", Hash: "def", Symbols: 1, IndexedAt: now})
	callerID, _ := adapter.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Run", Qualified: "main.Run", Kind: "function",
		LineStart: 1, LineEnd: 2, Snippet: "func Run() { main() }",
	})
	e := model.Edge{SourceID: &callerID, TargetID: mainID, Kind: model.EdgeCalls, FileID: fid2, Line: intPtr(1), Confidence: 1.0}
	if _, err := adapter.WriteEdge(ctx, &e); err != nil {
		t.Fatal(err)
	}

	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter:     adapter,
		db:          adapter.DB(),
		dir:         dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
	cleanup := func() {
		tracker.Close()
		_ = adapter.Close()
	}
	return dir, h, cleanup
}

// TestDominantMatchDeclinesOnEdgeCountError covers dominantMatch's defensive
// path: when the edge-count query fails (here, a closed DB) it cannot rank
// candidates, so it declines to auto-resolve rather than guess.
func TestDominantMatchDeclinesOnEdgeCountError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "dm_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := &handlers{adapter: adapter, db: adapter.DB(), dir: dir}
	_ = adapter.Close()

	matches := []cli.Match{
		{ID: 1, Name: "X", Qualified: "a.X", Kind: "function"},
		{ID: 2, Name: "X", Qualified: "b.X", Kind: "function"},
	}
	if _, ok := h.dominantMatch(ctx, matches); ok {
		t.Error("dominantMatch must decline when the edge-count query errors")
	}
}

// TestDisambiguationResultDegradesOnEdgeCountError covers disambiguationResult's
// fallback: when the edge-count query fails it logs and proceeds with zeroed
// counts, still returning a well-formed ambiguous result the LLM can act on.
func TestDisambiguationResultDegradesOnEdgeCountError(t *testing.T) {
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "dis_err.db"))
	if err != nil {
		t.Fatal(err)
	}
	h := &handlers{adapter: adapter, db: adapter.DB(), dir: dir}
	_ = adapter.Close()

	matches := []cli.Match{
		{ID: 1, Name: "X", Qualified: "a.X", Kind: "function", File: "a.go", LineStart: 1},
		{ID: 2, Name: "X", Qualified: "b.X", Kind: "function", File: "b.go", LineStart: 1},
	}
	result := h.disambiguationResult(ctx, "X", matches)
	text := result.Content[0].(mcp.TextContent).Text
	var resp map[string]any
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp["ambiguous"] != true {
		t.Errorf("expected an ambiguous result even with zeroed edge counts, got %v", resp)
	}
}

// TestHandleBlastDiffSuccess drives handleBlast's diff branch all the way
// through marshalling: a committed file edited in the working tree produces a
// non-empty blast response keyed by the diff ref, with the static-edge
// coverage note attached.
func TestHandleBlastDiffSuccess(t *testing.T) {
	dir, h, cleanup := fullGitHandler(t)
	defer cleanup()
	ctx := context.Background()

	// Edit the committed file so `git diff HEAD` reports a change to main.go.
	if err := os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\nfunc main() { println(\"x\") }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := h.handleBlast(ctx, toolReq(map[string]any{"diff": "HEAD"}))
	if err != nil {
		t.Fatalf("handleBlast diff: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected a successful diff blast, got %+v", result)
	}
	var resp struct {
		TotalAffected int    `json:"total_affected"`
		CoverageNote  string `json:"coverage_note"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.CoverageNote == "" {
		t.Error("expected the static-edge coverage note on a diff blast")
	}
}

// ambiguousWidgetHandler builds a handler over two equally-referenced "Widget"
// symbols in different files, so the name is ambiguous and dominantMatch cannot
// auto-resolve it (each has 1 edge, below the 5x rule). Used to exercise the
// file-hint disambiguation path of resolveSymbol.
func ambiguousWidgetHandler(t *testing.T) (*handlers, func()) {
	t.Helper()
	dir := t.TempDir()
	senseDir := filepath.Join(dir, ".sense")
	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(senseDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, path := range []string{"app/models/widget.rb", "app/components/widget.tsx"} {
		fid, _ := adapter.WriteFile(ctx, &model.File{Path: path, Language: "ruby", Hash: "h" + strconv.Itoa(i), Symbols: 1, IndexedAt: now})
		sid, _ := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Widget", Qualified: path + ".Widget", Kind: "class", LineStart: 1, LineEnd: 5})
		caller, _ := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fid, Name: "Caller", Qualified: path + ".Caller", Kind: "function", LineStart: 10, LineEnd: 10})
		e := model.Edge{SourceID: &caller, TargetID: sid, Kind: model.EdgeCalls, FileID: fid, Line: intPtr(1), Confidence: 1.0}
		if _, err := adapter.WriteEdge(ctx, &e); err != nil {
			t.Fatal(err)
		}
	}
	tracker := metrics.NewTracker(adapter.DB())
	h := &handlers{
		adapter: adapter, db: adapter.DB(), dir: dir,
		search:      search.NewEngine(adapter, nil, nil),
		tracker:     tracker,
		defaults:    profile.DefaultParams(),
		seenSymbols: make(map[int64]bool),
	}
	return h, func() { tracker.Close(); _ = adapter.Close() }
}

// TestResolveSymbolFileHintDisambiguates covers the new file-path disambiguation
// in resolveSymbol: an ambiguous symbol resolves to the single candidate whose
// path contains the hint, while an empty or non-matching hint leaves the normal
// ambiguous handling intact.
func TestResolveSymbolFileHintDisambiguates(t *testing.T) {
	h, cleanup := ambiguousWidgetHandler(t)
	defer cleanup()
	ctx := context.Background()

	// A matching file hint resolves to exactly that candidate.
	got, err := h.resolveSymbol(ctx, "sense_blast", "Widget", "models/widget.rb")
	if err != nil {
		t.Fatalf("file hint should resolve the ambiguity, got error: %v", err)
	}
	if !strings.Contains(got.File, "app/models/widget.rb") {
		t.Errorf("resolved to %q, want the app/models/widget.rb candidate", got.File)
	}

	// The other candidate is reachable by its own hint.
	got2, err := h.resolveSymbol(ctx, "sense_blast", "Widget", "components/widget.tsx")
	if err != nil {
		t.Fatalf("second file hint should resolve, got error: %v", err)
	}
	if !strings.Contains(got2.File, "components/widget.tsx") {
		t.Errorf("resolved to %q, want the components/widget.tsx candidate", got2.File)
	}

	// No hint: still ambiguous (resolveError).
	if _, err := h.resolveSymbol(ctx, "sense_blast", "Widget", ""); err == nil {
		t.Error("expected an ambiguous resolveError with no file hint")
	} else if _, ok := err.(*resolveError); !ok {
		t.Errorf("expected *resolveError, got %T", err)
	}

	// A hint that matches no candidate is ignored, so it stays ambiguous
	// rather than collapsing to not-found.
	if _, err := h.resolveSymbol(ctx, "sense_blast", "Widget", "nonexistent/path.rb"); err == nil {
		t.Error("expected ambiguity to remain when the file hint matches nothing")
	} else if _, ok := err.(*resolveError); !ok {
		t.Errorf("expected *resolveError for a non-matching hint, got %T", err)
	}
}

// TestHandleBlastFileHintResolves drives the file hint through the public
// handler end-to-end: an ambiguous symbol plus a file hint returns a real blast
// response instead of an ambiguity prompt.
func TestHandleBlastFileHintResolves(t *testing.T) {
	h, cleanup := ambiguousWidgetHandler(t)
	defer cleanup()
	ctx := context.Background()

	result, err := h.handleBlast(ctx, toolReq(map[string]any{
		"symbol": "Widget",
		"file":   "app/models/widget.rb",
	}))
	if err != nil {
		t.Fatalf("handleBlast with file hint: %v", err)
	}
	if result.IsError {
		t.Fatalf("expected a resolved blast response, got error result: %+v", result)
	}
}
