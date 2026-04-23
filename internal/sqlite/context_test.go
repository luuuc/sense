package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

func seedContextFixture(t *testing.T, ctx context.Context, a *sqlite.Adapter) (fileID int64) {
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

	fileID := seedContextFixture(t, ctx, a)

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
