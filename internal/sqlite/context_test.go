package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func seedContextFixture(ctx context.Context, t *testing.T, a *sqlite.Adapter) (fileID int64) {
	t.Helper()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/work_package.rb", Language: "ruby",
		Hash: "ctx1", Symbols: 5, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// WorkPackage class (parent of methods below)
	wpID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "WorkPackage", Qualified: "WorkPackage",
		Kind: model.KindClass, LineStart: 1, LineEnd: 200,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Methods defined by WorkPackage
	sdID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "set_dates", Qualified: "WorkPackage#set_dates",
		Kind: model.KindMethod, LineStart: 50, LineEnd: 60, ParentID: &wpID,
	})
	if err != nil {
		t.Fatal(err)
	}
	closeID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "close_duplicates", Qualified: "WorkPackage#close_duplicates",
		Kind: model.KindMethod, LineStart: 70, LineEnd: 80, ParentID: &wpID,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Composable targets (separate file so they're distinct symbols)
	fid2, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/project.rb", Language: "ruby",
		Hash: "ctx2", Symbols: 3, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Project", Qualified: "Project",
		Kind: model.KindClass, LineStart: 1, LineEnd: 50,
	})
	userID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "User", Qualified: "User",
		Kind: model.KindClass, LineStart: 51, LineEnd: 100,
	})
	validationsID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid2, Name: "Validations", Qualified: "Validations",
		Kind: model.KindModule, LineStart: 101, LineEnd: 120,
	})

	// Edges: WorkPackage composes Project, User
	for _, e := range []*model.Edge{
		{SourceID: model.Int64Ptr(wpID), TargetID: projID, Kind: model.EdgeComposes, FileID: fid, Confidence: 1.0},
		{SourceID: model.Int64Ptr(wpID), TargetID: userID, Kind: model.EdgeComposes, FileID: fid, Confidence: 1.0},
		{SourceID: model.Int64Ptr(wpID), TargetID: validationsID, Kind: model.EdgeIncludes, FileID: fid, Confidence: 1.0},
	} {
		if _, err := a.WriteEdge(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	// Edge: set_dates calls close_duplicates
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(sdID), TargetID: closeID,
		Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	return fid
}

func TestContextForFile(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fileID := seedContextFixture(ctx, t, a)

	result, err := a.ContextForFile(ctx, fileID)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("expected non-empty context map")
	}

	// Find the WorkPackage class context by checking all entries.
	var wpContext string
	for _, v := range result {
		if strings.Contains(v, "class WorkPackage") {
			wpContext = v
			break
		}
	}
	if wpContext == "" {
		t.Fatal("no context found for WorkPackage class")
	}

	t.Logf("WorkPackage context:\n%s", wpContext)

	if !strings.Contains(wpContext, "File: app/models/work_package.rb") {
		t.Error("missing file path")
	}
	if !strings.Contains(wpContext, "composes: Project, User") {
		t.Error("missing composes relationship")
	}
	if !strings.Contains(wpContext, "includes: Validations") {
		t.Error("missing includes relationship")
	}
	if !strings.Contains(wpContext, "defines: close_duplicates, set_dates") {
		t.Error("missing defines relationship")
	}

	// Check method context has calls.
	var methodContext string
	for _, v := range result {
		if strings.Contains(v, "method WorkPackage#set_dates") {
			methodContext = v
			break
		}
	}
	if methodContext == "" {
		t.Fatal("no context found for set_dates method")
	}

	t.Logf("set_dates context:\n%s", methodContext)

	if !strings.Contains(methodContext, "calls: close_duplicates") {
		t.Error("missing calls relationship for method")
	}
}

func TestContextForFileReverseIncludes(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Create a module and two classes that include it.
	modFile, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/concerns/schedulable.rb", Language: "ruby",
		Hash: "m1", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	modID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: modFile, Name: "Schedulable", Qualified: "Schedulable",
		Kind: model.KindModule, LineStart: 1, LineEnd: 30,
	})
	if err != nil {
		t.Fatal(err)
	}

	classFile, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/classes.rb", Language: "ruby",
		Hash: "c1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	issueID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: classFile, Name: "Issue", Qualified: "Issue",
		Kind: model.KindClass, LineStart: 1, LineEnd: 50,
	})
	mrID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: classFile, Name: "MergeRequest", Qualified: "MergeRequest",
		Kind: model.KindClass, LineStart: 51, LineEnd: 100,
	})

	// Both classes include the module.
	for _, srcID := range []int64{issueID, mrID} {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(srcID), TargetID: modID,
			Kind: model.EdgeIncludes, FileID: classFile, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := a.ContextForFile(ctx, modFile)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}

	modContext := result[modID]
	t.Logf("Schedulable context:\n%s", modContext)

	if !strings.Contains(modContext, "included by:") {
		t.Error("missing 'included by' for module")
	}
	if !strings.Contains(modContext, "Issue") || !strings.Contains(modContext, "MergeRequest") {
		t.Error("missing includers in 'included by' line")
	}
}

// An inherits edge populates the "inherits" relationship line, exercising the
// inherits arm of the contextEdges switch.
func TestContextForFileInherits(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/admin.rb", Language: "ruby",
		Hash: "inh", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	baseFile, _ := a.WriteFile(ctx, &model.File{
		Path: "app/models/user.rb", Language: "ruby",
		Hash: "base", Symbols: 1, IndexedAt: time.Now(),
	})
	baseID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: baseFile, Name: "User", Qualified: "User",
		Kind: model.KindClass, LineStart: 1, LineEnd: 20,
	})
	adminID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Admin", Qualified: "Admin",
		Kind: model.KindClass, LineStart: 1, LineEnd: 20,
	})
	if _, err := a.WriteEdge(ctx, &model.Edge{
		SourceID: model.Int64Ptr(adminID), TargetID: baseID,
		Kind: model.EdgeInherits, FileID: fid, Confidence: 1.0,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.ContextForFile(ctx, fid)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}
	if !strings.Contains(result[adminID], "inherits: User") {
		t.Errorf("expected inherits line, got: %q", result[adminID])
	}
}

// When the header alone is already at the budget, even a relationship line's
// label prefix overflows, so fitGroups appends nothing and breaks. The result
// is exactly the header.
func TestContextForFileHeaderFillsBudget(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Tune the path so the header ("File: <path>\nclass Thing") lands just
	// under the 800-char budget — close enough that even the shortest relation
	// prefix ("\ndefines: ") overflows, so fitGroups appends nothing.
	// header = len("File: ") + len(path) + len("\nclass Thing") = 6 + len(path) + 12.
	// Target header length 795: len(path) = 777.
	longPath := strings.Repeat("a", 777)
	fid, err := a.WriteFile(ctx, &model.File{
		Path: longPath, Language: "ruby", Hash: "long", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	classID, _ := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Thing", Qualified: "Thing",
		Kind: model.KindClass, LineStart: 1, LineEnd: 100,
	})
	// Give it a child so a "defines" group exists to be dropped.
	childID := classID
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "do_it", Qualified: "Thing#do_it",
		Kind: model.KindMethod, LineStart: 5, LineEnd: 9, ParentID: &childID,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.ContextForFile(ctx, fid)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}
	got := result[classID]
	if len(got) > 800 {
		t.Errorf("context exceeds budget: %d chars", len(got))
	}
	if strings.Contains(got, "defines:") {
		t.Errorf("defines line should be dropped when prefix overflows: %q", got)
	}
}

func TestContextForFileTruncation(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/models/huge_class.rb", Language: "ruby",
		Hash: "big", Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	classID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "HugeClass", Qualified: "HugeClass",
		Kind: model.KindClass, LineStart: 1, LineEnd: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create many child symbols to fill the defines list.
	for i := range 50 {
		name := fmt.Sprintf("method_%02d_with_a_very_long_name_to_fill_budget", i)
		if _, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: name, Qualified: "HugeClass#" + name,
			Kind: model.KindMethod, LineStart: i*10 + 10, LineEnd: i*10 + 19,
			ParentID: &classID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	result, err := a.ContextForFile(ctx, fid)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}

	classContext := result[classID]
	t.Logf("HugeClass context length: %d chars", len(classContext))

	if len(classContext) > 800 {
		t.Errorf("context exceeds budget: %d chars (max 800)", len(classContext))
	}

	// Header should always be present.
	if !strings.Contains(classContext, "File: app/models/huge_class.rb") {
		t.Error("header truncated — file path missing")
	}
	if !strings.Contains(classContext, "class HugeClass") {
		t.Error("header truncated — class name missing")
	}
}

func TestContextForFileEmpty(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "empty.rb", Language: "ruby",
		Hash: "e", Symbols: 0, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := a.ContextForFile(ctx, fid)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for file with no symbols, got %d entries", len(result))
	}
}

func TestContextForFileNonexistentID(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	_, err = a.ContextForFile(ctx, 99999)
	if err == nil {
		t.Fatal("expected error for nonexistent file ID, got nil")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("error should wrap sql.ErrNoRows, got: %v", err)
	}
}

func TestContextForFileSymbolsNoEdges(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	fid, err := a.WriteFile(ctx, &model.File{
		Path: "standalone.go", Language: "go",
		Hash: "sa1", Symbols: 2, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Alpha", Qualified: "pkg.Alpha",
		Kind: model.KindClass, LineStart: 1, LineEnd: 20,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: "Beta", Qualified: "pkg.Beta",
		Kind: "function", LineStart: 25, LineEnd: 35,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := a.ContextForFile(ctx, fid)
	if err != nil {
		t.Fatalf("ContextForFile: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	for _, ctx := range result {
		if !strings.Contains(ctx, "File: standalone.go") {
			t.Errorf("missing file path in context: %q", ctx)
		}
		if strings.Contains(ctx, "calls:") || strings.Contains(ctx, "composes:") {
			t.Errorf("symbols with no edges should have no relationship lines: %q", ctx)
		}
	}
}

// seedChain builds a linear call chain of length n:
//
//	sym[n-1] → sym[n-2] → … → sym[1] → sym[0]
//
// Returns the symbol IDs in order sym[0]…sym[n-1].
func seedChain(ctx context.Context, t *testing.T, a *sqlite.Adapter, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range n {
		fid, err := a.WriteFile(ctx, &model.File{
			Path: fmt.Sprintf("chain_%d.rb", i), Language: "ruby",
			Hash: fmt.Sprintf("ch%d", i), IndexedAt: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		sid, err := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: fmt.Sprintf("S%d", i),
			Qualified: fmt.Sprintf("S%d", i),
			Kind:      model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = sid
	}
	for i := 1; i < n; i++ {
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(ids[i]), TargetID: ids[i-1],
			Kind: model.EdgeCalls, FileID: 1, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func TestReadSymbolGraphDepth(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Chain: S3 → S2 → S1 → S0
	ids := seedChain(ctx, t, a, 4)

	t.Run("depth=1 returns root only", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, ids[0], 1, "both", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(gr.Layers) != 0 {
			t.Errorf("layers = %d, want 0", len(gr.Layers))
		}
		if len(gr.Root.Inbound) != 1 {
			t.Errorf("root inbound = %d, want 1 (S1)", len(gr.Root.Inbound))
		}
	})

	t.Run("depth=2 adds one layer", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, ids[0], 2, "callers", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(gr.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(gr.Layers))
		}
		if len(gr.Layers[0].Inbound) != 1 {
			t.Errorf("layer[0] inbound = %d, want 1 (S2)", len(gr.Layers[0].Inbound))
		}
	})

	t.Run("depth=3 adds two layers", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, ids[0], 3, "callers", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(gr.Layers) != 2 {
			t.Fatalf("layers = %d, want 2", len(gr.Layers))
		}
		if gr.Root.Inbound[0].Target.Qualified != "S1" {
			t.Errorf("root caller = %s, want S1", gr.Root.Inbound[0].Target.Qualified)
		}
		if gr.Layers[0].Inbound[0].Target.Qualified != "S2" {
			t.Errorf("layer[0] caller = %s, want S2", gr.Layers[0].Inbound[0].Target.Qualified)
		}
		if gr.Layers[1].Inbound[0].Target.Qualified != "S3" {
			t.Errorf("layer[1] caller = %s, want S3", gr.Layers[1].Inbound[0].Target.Qualified)
		}
	})

	t.Run("callees direction", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, ids[3], 2, "callees", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(gr.Root.Outbound) != 1 || gr.Root.Outbound[0].Target.Qualified != "S2" {
			t.Errorf("root callee = %v, want [S2]", gr.Root.Outbound)
		}
		if len(gr.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(gr.Layers))
		}
		if len(gr.Layers[0].Outbound) != 1 || gr.Layers[0].Outbound[0].Target.Qualified != "S1" {
			t.Errorf("layer callee = %v, want [S1]", gr.Layers[0].Outbound)
		}
	})

	t.Run("dedup prevents cycles", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, ids[0], 3, "both", 200)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, e := range gr.Root.Inbound {
			seen[e.Target.Qualified] = true
		}
		for _, e := range gr.Root.Outbound {
			seen[e.Target.Qualified] = true
		}
		for _, layer := range gr.Layers {
			for _, e := range layer.Inbound {
				if seen[e.Target.Qualified] {
					t.Errorf("duplicate across layers: %s", e.Target.Qualified)
				}
				seen[e.Target.Qualified] = true
			}
			for _, e := range layer.Outbound {
				if seen[e.Target.Qualified] {
					t.Errorf("duplicate across layers: %s", e.Target.Qualified)
				}
				seen[e.Target.Qualified] = true
			}
		}
	})
}

func TestReadSymbolGraphDepthExceedsChain(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Chain: S3 → S2 → S1 → S0. Asking for more depth than the chain has
	// must stop cleanly when the frontier empties, not loop or truncate.
	ids := seedChain(ctx, t, a, 4)

	gr, err := a.ReadSymbolGraph(ctx, ids[0], 6, "callers", 200)
	if err != nil {
		t.Fatal(err)
	}
	if gr.Truncated {
		t.Error("expected Truncated=false when the chain exhausts before max depth")
	}
	// Root caller S1, then layers for S2 and S3 — two layers, then the
	// frontier is empty and the hop loop breaks before depth 6.
	if len(gr.Layers) != 2 {
		t.Fatalf("layers = %d, want 2 (chain exhausts at S3)", len(gr.Layers))
	}
}

func TestReadSymbolGraphTruncationCallees(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Callee star: a root method calls 10 leaves. maxPerHop must cap the
	// outbound expansion at hop 2, exercising the callees side of the hop.
	rootFile, _ := a.WriteFile(ctx, &model.File{
		Path: "root.rb", Language: "ruby", Hash: "r", IndexedAt: time.Now(),
	})
	rootID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: rootFile, Name: "Root", Qualified: "Root",
		Kind: model.KindMethod, LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		fid, _ := a.WriteFile(ctx, &model.File{
			Path: fmt.Sprintf("leaf_%d.rb", i), Language: "ruby",
			Hash: fmt.Sprintf("l%d", i), IndexedAt: time.Now(),
		})
		leafID, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: fmt.Sprintf("Leaf%d", i),
			Qualified: fmt.Sprintf("Leaf%d", i),
			Kind:      model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		// Root calls Leaf; each Leaf calls its own grandchild so hop 2 has
		// outbound edges to admit.
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(rootID), TargetID: leafID,
			Kind: model.EdgeCalls, FileID: rootFile, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
		gfid, _ := a.WriteFile(ctx, &model.File{
			Path: fmt.Sprintf("grandleaf_%d.rb", i), Language: "ruby",
			Hash: fmt.Sprintf("gl%d", i), IndexedAt: time.Now(),
		})
		grandID, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: gfid, Name: fmt.Sprintf("GrandLeaf%d", i),
			Qualified: fmt.Sprintf("GrandLeaf%d", i),
			Kind:      model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(leafID), TargetID: grandID,
			Kind: model.EdgeCalls, FileID: gfid, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	gr, err := a.ReadSymbolGraph(ctx, rootID, 2, "callees", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !gr.Truncated {
		t.Error("expected Truncated=true on capped callee expansion")
	}
	if len(gr.Layers) != 1 {
		t.Fatalf("layers = %d, want 1", len(gr.Layers))
	}
	if len(gr.Layers[0].Outbound) != 3 {
		t.Errorf("layer outbound = %d, want 3 (maxPerHop)", len(gr.Layers[0].Outbound))
	}
}

func TestReadSymbolGraphTruncation(t *testing.T) {
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()

	// Star topology: hub has 10 callers, each caller has its own caller.
	hubFile, err := a.WriteFile(ctx, &model.File{
		Path: "hub.rb", Language: "ruby",
		Hash: "hub", IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	hubID, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: hubFile, Name: "Hub", Qualified: "Hub",
		Kind: model.KindMethod, LineStart: 1, LineEnd: 5,
	})
	if err != nil {
		t.Fatal(err)
	}

	for i := range 10 {
		fid, _ := a.WriteFile(ctx, &model.File{
			Path: fmt.Sprintf("caller_%d.rb", i), Language: "ruby",
			Hash: fmt.Sprintf("c%d", i), IndexedAt: time.Now(),
		})
		callerID, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: fid, Name: fmt.Sprintf("Caller%d", i),
			Qualified: fmt.Sprintf("Caller%d", i),
			Kind:      model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(callerID), TargetID: hubID,
			Kind: model.EdgeCalls, FileID: fid, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
		// Each caller has its own grandparent caller
		gfid, _ := a.WriteFile(ctx, &model.File{
			Path: fmt.Sprintf("grand_%d.rb", i), Language: "ruby",
			Hash: fmt.Sprintf("g%d", i), IndexedAt: time.Now(),
		})
		grandID, _ := a.WriteSymbol(ctx, &model.Symbol{
			FileID: gfid, Name: fmt.Sprintf("Grand%d", i),
			Qualified: fmt.Sprintf("Grand%d", i),
			Kind:      model.KindMethod, LineStart: 1, LineEnd: 5,
		})
		if _, err := a.WriteEdge(ctx, &model.Edge{
			SourceID: model.Int64Ptr(grandID), TargetID: callerID,
			Kind: model.EdgeCalls, FileID: gfid, Confidence: 1.0,
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("maxPerHop=3 truncates at hop 2", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, hubID, 2, "callers", 3)
		if err != nil {
			t.Fatal(err)
		}
		if !gr.Truncated {
			t.Error("expected Truncated=true")
		}
		// Root should have all 10 callers (depth-1 is not capped by maxPerHop)
		if len(gr.Root.Inbound) != 10 {
			t.Errorf("root inbound = %d, want 10", len(gr.Root.Inbound))
		}
		// Layer should exist with partial results
		if len(gr.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(gr.Layers))
		}
		if len(gr.Layers[0].Inbound) != 3 {
			t.Errorf("layer inbound = %d, want 3 (maxPerHop)", len(gr.Layers[0].Inbound))
		}
	})

	t.Run("maxPerHop=0 means unlimited", func(t *testing.T) {
		gr, err := a.ReadSymbolGraph(ctx, hubID, 2, "callers", 0)
		if err != nil {
			t.Fatal(err)
		}
		if gr.Truncated {
			t.Error("expected Truncated=false with unlimited cap")
		}
		if len(gr.Layers) != 1 {
			t.Fatalf("layers = %d, want 1", len(gr.Layers))
		}
		if len(gr.Layers[0].Inbound) != 10 {
			t.Errorf("layer inbound = %d, want 10", len(gr.Layers[0].Inbound))
		}
	})
}
