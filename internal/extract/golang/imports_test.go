package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

func parseImports(t *testing.T, src string) map[string]string {
	t.Helper()
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatalf("set language: %v", err)
	}
	tree := parser.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()
	return collectImports(tree.RootNode(), []byte(src))
}

func TestCollectImportsPlainAndGrouped(t *testing.T) {
	src := `package p

import "fmt"

import (
	"strings"
	"code.gitea.io/gitea/services/context"
)
`
	got := parseImports(t, src)
	want := map[string]string{
		"fmt":     "fmt",
		"strings": "strings",
		"context": "code.gitea.io/gitea/services/context",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d entries %v, want %d", len(got), got, len(want))
	}
	for name, path := range want {
		if got[name] != path {
			t.Errorf("entry %q = %q, want %q", name, got[name], path)
		}
	}
}

func TestCollectImportsAlias(t *testing.T) {
	src := `package p

import (
	repo_model "code.gitea.io/gitea/models/repo"
	fmt "github.com/x/fmtlib"
)
`
	got := parseImports(t, src)
	if got["repo_model"] != "code.gitea.io/gitea/models/repo" {
		t.Errorf("alias entry = %q", got["repo_model"])
	}
	// The in-file name decides the key; the PATH decides classification later.
	// An alias reusing a stdlib name must map to its real (non-stdlib) path.
	if got["fmt"] != "github.com/x/fmtlib" {
		t.Errorf("stdlib-shadowing alias = %q, want the aliased path", got["fmt"])
	}
	if _, ok := got["repo"]; ok {
		t.Error("basename of an aliased import must not appear as a key")
	}
}

func TestCollectImportsDotAndBlankExcluded(t *testing.T) {
	src := `package p

import (
	. "fmt"
	_ "net/http/pprof"
	"strings"
)
`
	got := parseImports(t, src)
	if len(got) != 1 || got["strings"] != "strings" {
		t.Fatalf("dot/blank imports must produce no entry, got %v", got)
	}
}

func TestCollectImportsVersionSuffixes(t *testing.T) {
	src := `package p

import (
	"github.com/x/mod/v2"
	"gopkg.in/yaml.v3"
)
`
	got := parseImports(t, src)
	// Module-major and gopkg.in conventions: the inferred in-file name strips
	// the version suffix. A wrong guess is a table MISS (today's behavior),
	// never a wrong bind; the resolver verifies paths independently.
	if got["mod"] != "github.com/x/mod/v2" {
		t.Errorf("mod/v2 entry = %v", got)
	}
	if got["yaml"] != "gopkg.in/yaml.v3" {
		t.Errorf("yaml.v3 entry = %v", got)
	}
}

func TestCollectImportsDuplicateInferredNameDropsBoth(t *testing.T) {
	// Two imports whose INFERRED names collide (real Go would alias one; our
	// heuristic can collide where the package clauses differ). A guess that
	// cannot be unique is no evidence: both keys vanish, operands fall through
	// to today's behavior.
	src := `package p

import (
	"github.com/a/util/v2"
	"github.com/b/util"
)
`
	got := parseImports(t, src)
	if _, ok := got["util"]; ok {
		t.Fatalf("colliding inferred names must drop the key, got %v", got)
	}
}

func TestCollectImportsEdgeShapes(t *testing.T) {
	// A bare version-segment path must not panic or strip below its only
	// segment; a dotted suffix that is not a version stays untouched; a
	// THIRD collision on an already-dropped name stays dropped; an empty
	// grouped list contributes nothing.
	src := `package p

import (
	"v2"
	"example.com/tool.vx"
	"github.com/a/util/v2"
	"github.com/b/util"
	"github.com/c/util"
)

import ()
`
	got := parseImports(t, src)
	if got["v2"] != "v2" {
		t.Errorf("bare version-segment path = %v", got)
	}
	if got["tool.vx"] != "example.com/tool.vx" {
		t.Errorf("non-version dotted suffix must keep its name, got %v", got)
	}
	if _, ok := got["util"]; ok {
		t.Errorf("three-way collision must stay dropped, got %v", got)
	}
	if len(got) != 2 {
		t.Errorf("unexpected table: %v", got)
	}
}

func TestCollectImportsMalformedSpecs(t *testing.T) {
	// Tree-sitter is error-tolerant: a spec without a path string and an
	// unparseable path literal must produce no entry, never a panic.
	src := "package p\n\nimport (\n\tfoo\n\t\"\"\n\t\"x/\"\n)\n"
	if got := parseImports(t, src); len(got) != 0 {
		t.Errorf("malformed specs must contribute nothing, got %v", got)
	}
}

func TestCollectImportsCgoAndEmpty(t *testing.T) {
	if got := parseImports(t, `package p

import "C"
`); got["C"] != "C" {
		t.Errorf("cgo import = %v", got)
	}
	if got := parseImports(t, `package p
`); len(got) != 0 {
		t.Errorf("no imports: got %v", got)
	}
}
