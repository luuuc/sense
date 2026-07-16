package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/resolve"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func modHarness(t *testing.T, root string, patterns ...string) *harness {
	t.Helper()
	return &harness{
		ctx:            context.Background(),
		root:           root,
		matcher:        ignore.New(append(ignore.DefaultPatterns(), patterns...)...),
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
	}
}

func TestCollectGoModulesRootAndNested(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":         "module code.gitea.io/gitea\n\ngo 1.22\n",
		"go/go.mod":      "module github.com/dolthub/dolt/go // monorepo submodule\n",
		"README.md":      "not a module",
		"pkg/notmod":     "module fake/inline", // wrong filename: ignored
		".hidden/go.mod": "module hidden/skipped\n",
	})
	mods := h2map(modHarness(t, root).collectGoModules())
	if mods["code.gitea.io/gitea"] != "." {
		t.Errorf("root module = %v", mods)
	}
	if mods["github.com/dolthub/dolt/go"] != "go" {
		t.Errorf("nested module = %v", mods)
	}
	if _, ok := mods["hidden/skipped"]; ok {
		t.Error("dot-dir go.mod must be skipped")
	}
	if len(mods) != 2 {
		t.Errorf("unexpected extra modules: %v", mods)
	}
}

func TestCollectGoModulesRespectsIgnoreMatcher(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                   "module real/app\n",
		"testdata/modfix/go.mod":   "module github.com/fake/fixture\n",
		"vendor/dep/go.mod":        "module github.com/some/dep\n",
		"internal/fixtures/go.mod": "module another/fixture\n",
	})
	// testdata/ and vendor/ ride the default patterns; internal/fixtures via
	// a project ignore rule.
	mods := h2map(modHarness(t, root, "internal/fixtures/").collectGoModules())
	if len(mods) != 1 || mods["real/app"] != "." {
		t.Fatalf("fixture/vendored go.mod leaked into the module table: %v", mods)
	}
}

func TestCollectGoModulesSkipsSymlinksAndUnreadable(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":        "module real/app\n",
		"locked/go.mod": "module locked/out\n",
	})
	writeTree(t, outside, map[string]string{"go.mod": "module outside/linked\n"})
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := os.Chmod(filepath.Join(root, "locked", "go.mod"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked", "go.mod"), 0o644) })

	mods := h2map(modHarness(t, root).collectGoModules())
	if _, ok := mods["outside/linked"]; ok {
		t.Error("symlinked go.mod must be skipped")
	}
	if _, ok := mods["locked/out"]; ok {
		t.Error("unreadable go.mod must shrink the table, not fail")
	}
	if mods["real/app"] != "." || len(mods) != 1 {
		t.Fatalf("table = %v", mods)
	}
}

func TestCollectGoModulesCancelledContext(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"go.mod": "module real/app\n", "sub/keep.go": "package sub\n"})
	h := modHarness(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ctx = ctx
	if mods := h.collectGoModules(); len(mods) != 0 {
		t.Fatalf("cancelled context must abort the walk, got %v", mods)
	}
}

func TestGoModModulePath(t *testing.T) {
	cases := map[string]string{
		"module foo/bar\n":             "foo/bar",
		"// c\n\nmodule\tfoo/tabbed\n": "foo/tabbed",
		"module \"quoted/path\"\n":     "quoted/path",
		"module foo/bar // trailing\n": "foo/bar",
		"modulefoo/nospace\n":          "",
		"go 1.22\n":                    "",
		"":                             "",
		"// module commented/out\nmodule real/x\n": "real/x",
	}
	for in, want := range cases {
		if got := goModModulePath([]byte(in)); got != want {
			t.Errorf("goModModulePath(%q) = %q, want %q", in, got, want)
		}
	}
}

func h2map(mods []resolve.GoModule) map[string]string {
	m := map[string]string{}
	for _, mod := range mods {
		m[mod.Path] = mod.Dir
	}
	return m
}
