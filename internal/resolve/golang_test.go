package resolve_test

import (
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
)

// goRefs models a gitea-shaped corpus: two module-local packages sharing the
// basename "context" (the shadow pair the path lane exists to tell apart), a
// same-dir external test package, and a same-named symbol in an unrelated
// package (the leaf-fallback bait).
func goRefs() []model.SymbolRef {
	return []model.SymbolRef{
		{ID: 1, Qualified: "context.Context", FileID: 10, Language: "go", Path: "services/context/context.go"},
		{ID: 2, Qualified: "context.Base.FormString", FileID: 11, Language: "go", Path: "services/context/base_form.go"},
		{ID: 3, Qualified: "context.Context", FileID: 20, Language: "go", Path: "modules/context/ctx.go"},
		{ID: 4, Qualified: "context_test.Helper", FileID: 12, Language: "go", Path: "services/context/main_test.go"},
		{ID: 5, Qualified: "log.PrintfLogger.Printf", FileID: 30, Language: "go", Path: "modules/log/misc.go"},
		{ID: 6, Qualified: "repo.SearchRepositoryByName", FileID: 40, Language: "go", Path: "models/repo/repo_list.go"},
		{ID: 7, Qualified: "caller.DeletePackage", FileID: 50, Language: "go", Path: "routers/api/packages/caller/caller.go"},
	}
}

func giteaModules() []resolve.GoModule {
	return []resolve.GoModule{{Path: "code.gitea.io/gitea", Dir: "."}}
}

func goReq(inPkg, importPath string) resolve.Request {
	return resolve.Request{
		Target:           "ignored.by.path.lane",
		TargetInPackage:  inPkg,
		TargetImportPath: importPath,
		Kind:             model.EdgeCalls,
		SourceFileID:     50,
		BaseConfidence:   1.0,
	}
}

func TestGoPathLaneBindsModuleLocalByDirectory(t *testing.T) {
	ix := resolve.NewIndex(goRefs()).WithGoModules(giteaModules())

	// services/context vs modules/context share a basename; the directory
	// derived from the import path must pick the right one.
	r, ok := ix.Resolve(goReq("Context", "code.gitea.io/gitea/services/context"))
	if !ok || r.SymbolID != 1 {
		t.Fatalf("services/context bind = %+v ok=%v, want ID 1", r, ok)
	}
	if r.Confidence != 1.0 || r.Ambiguous {
		t.Fatalf("unique dir-scoped match must keep BaseConfidence, got %+v", r)
	}

	r, ok = ix.Resolve(goReq("Context", "code.gitea.io/gitea/modules/context"))
	if !ok || r.SymbolID != 3 {
		t.Fatalf("modules/context bind = %+v ok=%v, want ID 3", r, ok)
	}

	// A Type.Method target binds the same way.
	r, ok = ix.Resolve(goReq("Base.FormString", "code.gitea.io/gitea/services/context"))
	if !ok || r.SymbolID != 2 {
		t.Fatalf("Base.FormString bind = %+v ok=%v, want ID 2", r, ok)
	}
}

func TestGoPathLaneDropsExternalPaths(t *testing.T) {
	ix := resolve.NewIndex(goRefs()).WithGoModules(giteaModules())

	// Stdlib: a local shadow package exists and holds the name; the path
	// lane must refuse the bind (mutant M1's kill case).
	r, ok := ix.Resolve(goReq("Context", "context"))
	if ok {
		t.Fatalf("stdlib import path bound to %+v, want drop", r)
	}
	if !r.External {
		t.Fatal("stdlib drop must be flagged External")
	}

	// Third-party: same refusal.
	r, ok = ix.Resolve(goReq("Command", "github.com/urfave/cli/v2"))
	if ok || !r.External {
		t.Fatalf("third-party drop = %+v ok=%v, want External drop", r, ok)
	}

	// TERMINAL: the drop must not fall through to the leaf fallback even
	// though byName holds a same-named candidate.
	r, ok = ix.Resolve(goReq("Printf", "fmt"))
	if ok {
		t.Fatalf("fmt.Printf leaked past the path lane to %+v", r)
	}
}

func TestGoPathLaneSegmentAnchoredLongestPrefix(t *testing.T) {
	refs := []model.SymbolRef{
		{ID: 1, Qualified: "util.Do", FileID: 1, Language: "go", Path: "util/do.go"},
		{ID: 2, Qualified: "util.Do", FileID: 2, Language: "go", Path: "tools/util/do.go"},
	}
	mods := []resolve.GoModule{
		{Path: "corp/app", Dir: "."},
		{Path: "corp/app/tools", Dir: "tools"},
	}
	ix := resolve.NewIndex(refs).WithGoModules(mods)

	// The source must be a Go file the index knows (the lane gates on the
	// source file's language).
	src := func(inPkg, importPath string) resolve.Request {
		req := goReq(inPkg, importPath)
		req.SourceFileID = 1
		return req
	}

	// Longest prefix wins: corp/app/tools/util → tools/util, not util.
	r, ok := ix.Resolve(src("Do", "corp/app/tools/util"))
	if !ok || r.SymbolID != 2 {
		t.Fatalf("nested module bind = %+v ok=%v, want ID 2", r, ok)
	}
	// Dotless module path is module-local, not stdlib (the inversion bug).
	r, ok = ix.Resolve(src("Do", "corp/app/util"))
	if !ok || r.SymbolID != 1 {
		t.Fatalf("dotless module-local bind = %+v ok=%v, want ID 1", r, ok)
	}
	// Segment boundary: corp/app2 must not prefix-match corp/app.
	r, ok = ix.Resolve(src("Do", "corp/app2/util"))
	if ok || !r.External {
		t.Fatalf("corp/app2 = %+v ok=%v, want External drop", r, ok)
	}
}

func TestGoPathLaneInertWithoutModules(t *testing.T) {
	// No module table → the lane never fires; the request degrades to the
	// legacy lane on Target, exactly today's behavior.
	ix := resolve.NewIndex(goRefs())
	req := goReq("Context", "code.gitea.io/gitea/services/context")
	req.Target = "context.Context"
	r, ok := ix.Resolve(req)
	if !ok {
		t.Fatal("legacy lane must still resolve the raw target text")
	}
	// Today's behavior IS the fabrication-prone byQualified match; the
	// pinned point is inertness, not correctness.
	if r.SymbolID != 1 && r.SymbolID != 3 {
		t.Fatalf("legacy lane bound %+v, want a byQualified candidate", r)
	}
}

func TestGoPathLaneAmbiguousModulePathFallsThrough(t *testing.T) {
	refs := goRefs()
	mods := []resolve.GoModule{
		{Path: "corp/dup", Dir: "a"},
		{Path: "corp/dup", Dir: "b"},
		{Path: "code.gitea.io/gitea", Dir: "."},
	}
	ix := resolve.NewIndex(refs).WithGoModules(mods)

	// A duplicated module path is ambiguity: never silently pick a dir.
	// The request falls through to the legacy lane (Target text).
	req := goReq("Context", "corp/dup/context")
	req.Target = "context.Context"
	r, ok := ix.Resolve(req)
	if !ok {
		t.Fatal("ambiguous module path must fall through to the legacy lane")
	}
	if r.External {
		t.Fatalf("fall-through must not be flagged External, got %+v", r)
	}
	// Non-duplicated modules in the same table still work.
	r, ok = ix.Resolve(goReq("Context", "code.gitea.io/gitea/services/context"))
	if !ok || r.SymbolID != 1 {
		t.Fatalf("healthy module beside a duplicate = %+v ok=%v", r, ok)
	}
}

func TestGoPathLaneTestDirectionAndLanguageGates(t *testing.T) {
	ix := resolve.NewIndex(goRefs()).WithGoModules(giteaModules())

	// A production source must not bind into the same-dir external test
	// package even though the directory matches.
	r, ok := ix.Resolve(goReq("Helper", "code.gitea.io/gitea/services/context"))
	if ok {
		t.Fatalf("production source bound into x_test package: %+v", r)
	}
	if r.External {
		t.Fatal("an in-tree miss is not External")
	}

	// The lane only fires for Go sources: a non-Go source file with a
	// path-annotated request uses the legacy lane.
	refs := append(goRefs(), model.SymbolRef{ID: 8, Qualified: "context.Context", FileID: 60, Language: "ruby", Path: "app/models/context.rb"})
	ix = resolve.NewIndex(refs).WithGoModules(giteaModules())
	req := goReq("Context", "context")
	req.SourceFileID = 60
	req.Target = "context.Context"
	if _, ok := ix.Resolve(req); !ok {
		t.Fatal("non-Go source must take the legacy lane, which resolves the text")
	}
}

func TestDottedGoTargetAtUnresolvedSkipsExactMatch(t *testing.T) {
	// A Go extractor emits `pkgvar.Method` at ConfidenceUnresolved when the
	// operand is provably not a package qualifier (neither local nor
	// imported). Its dotted text must NOT exact-bind into a same-named
	// indexed package at face value; it flows to the gated fallback and
	// lands demoted below blast's floor.
	refs := []model.SymbolRef{
		{ID: 1, Qualified: "log.Error", FileID: 1, Language: "go", Path: "modules/log/log.go"},
		{ID: 2, Qualified: "caller.f", FileID: 2, Language: "go", Path: "cmd/caller/main.go"},
	}
	ix := resolve.NewIndex(refs)
	r, ok := ix.Resolve(resolve.Request{
		Target:         "log.Error",
		Kind:           model.EdgeCalls,
		SourceFileID:   2,
		BaseConfidence: 0.5,
	})
	if !ok {
		t.Fatal("the demoted guess should still resolve for dead-code liveness")
	}
	if r.Confidence > 0.3 {
		t.Fatalf("provable non-qualifier bound at %v, want demotion to <= 0.3", r.Confidence)
	}

	// Control: the same shape at full confidence (a real qualifier the
	// import table vouched for never rides 0.5) still exact-binds.
	r, ok = ix.Resolve(resolve.Request{
		Target:         "log.Error",
		Kind:           model.EdgeCalls,
		SourceFileID:   2,
		BaseConfidence: 1.0,
	})
	if !ok || r.Confidence != 1.0 {
		t.Fatalf("full-confidence dotted target must keep exact match, got %+v ok=%v", r, ok)
	}

	// Control: a non-Go source with a dotted 0.5 target keeps today's
	// exact-match behavior (the skip is Go-gated).
	refs = append(refs, model.SymbolRef{ID: 3, Qualified: "log.Error", FileID: 3, Language: "ruby", Path: "app/log.rb"},
		model.SymbolRef{ID: 4, Qualified: "app.caller", FileID: 4, Language: "ruby", Path: "app/caller.rb"})
	ix = resolve.NewIndex(refs)
	r, ok = ix.Resolve(resolve.Request{
		Target:         "log.Error",
		Kind:           model.EdgeCalls,
		SourceFileID:   4,
		BaseConfidence: 0.5,
	})
	if !ok || r.SymbolID != 3 || r.Confidence != 0.5 {
		t.Fatalf("ruby dotted target must keep exact match, got %+v ok=%v", r, ok)
	}
}

func TestWithGoModulesHygiene(t *testing.T) {
	// Empty paths are skipped; the same (path, dir) listed twice (nested
	// walks can revisit) collapses to one entry and still binds; an exact
	// duplicate must NOT count as ambiguity.
	refs := []model.SymbolRef{
		{ID: 1, Qualified: "util.Do", FileID: 1, Language: "go", Path: "util/do.go"},
	}
	mods := []resolve.GoModule{
		{Path: "", Dir: "x"},
		{Path: "corp/app", Dir: "."},
		{Path: "corp/app", Dir: "."},
	}
	ix := resolve.NewIndex(refs).WithGoModules(mods)
	req := goReq("Do", "corp/app/util")
	req.SourceFileID = 1
	r, ok := ix.Resolve(req)
	if !ok || r.SymbolID != 1 {
		t.Fatalf("deduped module must bind, got %+v ok=%v", r, ok)
	}
}

func TestGoPathLaneMultiCandidateKeepsAmbiguousClamp(t *testing.T) {
	refs := []model.SymbolRef{
		{ID: 1, Qualified: "p.Do", FileID: 1, Language: "go", Path: "lib/p/a.go"},
		{ID: 2, Qualified: "p.Do", FileID: 2, Language: "go", Path: "lib/p/b.go"},
	}
	ix := resolve.NewIndex(refs).WithGoModules([]resolve.GoModule{{Path: "m", Dir: "."}})
	req := goReq("Do", "m/lib/p")
	req.SourceFileID = 1
	r, ok := ix.Resolve(req)
	if !ok {
		t.Fatal("multi-candidate dir must still bind")
	}
	// The clamp is the shared ambiguousConfidence (0.8), below blast's
	// verified band and flagged Ambiguous, the same policy as every other lane.
	if !r.Ambiguous || r.Confidence >= 1.0 {
		t.Fatalf("multi-candidate must ride the ambiguous clamp, got %+v", r)
	}
}
