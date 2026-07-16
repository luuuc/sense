package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
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

// The three fabrication doors: a qualified type reaching an edge as literal
// text lets byQualified bind stdlib/third-party names to same-named local
// packages. Emission keeps the legacy text (TargetQualified) and adds the
// import-path annotation the resolver's path lane decides on.

func TestComposeQualifiedTypeCarriesImportPath(t *testing.T) {
	r := parse(t, `package storage

import (
	"context"
	sctx "code.gitea.io/gitea/services/context"
)

type LocalStorage struct {
	ctx  context.Context
	base sctx.Base
}
`)
	e := findEdge(r, "storage.LocalStorage", "context.Context", "composes")
	if e == nil {
		t.Fatal("missing composes edge for stdlib-typed field")
	}
	if e.TargetImportPath != "context" || e.TargetInPackage != "Context" {
		t.Errorf("stdlib field annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
	e = findEdge(r, "storage.LocalStorage", "sctx.Base", "composes")
	if e == nil {
		t.Fatal("missing composes edge for aliased local field")
	}
	if e.TargetImportPath != "code.gitea.io/gitea/services/context" || e.TargetInPackage != "Base" {
		t.Errorf("aliased field annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
}

func TestComposeUnknownQualifierStaysLegacy(t *testing.T) {
	// A qualifier with no import-table entry (partial parse, missing import)
	// must keep today's behavior exactly: literal text, no annotation.
	r := parse(t, `package p

type X struct {
	f zzz.T
}
`)
	e := findEdge(r, "p.X", "zzz.T", "composes")
	if e == nil {
		t.Fatal("missing composes edge for unknown qualifier")
	}
	if e.TargetImportPath != "" || e.TargetInPackage != "" {
		t.Errorf("unknown qualifier must carry no annotation, got %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
}

func TestEmbeddedQualifiedTypesCarryImportPath(t *testing.T) {
	r := parse(t, `package p

import (
	"context"
	"io"
)

type Wrapped struct {
	context.Context
}

type Iface interface {
	io.Reader
}
`)
	e := findEdge(r, "p.Wrapped", "context.Context", "includes")
	if e == nil {
		t.Fatal("missing includes edge for embedded stdlib type")
	}
	if e.TargetImportPath != "context" || e.TargetInPackage != "Context" {
		t.Errorf("embedded struct annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
	e = findEdge(r, "p.Iface", "io.Reader", "includes")
	if e == nil {
		t.Fatal("missing includes edge for embedded interface")
	}
	if e.TargetImportPath != "io" || e.TargetInPackage != "Reader" {
		t.Errorf("embedded interface annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
	// In-package embeds stay un-annotated: the legacy qualified name is
	// already exact.
	r = parse(t, `package p

type Base struct{}

type Sub struct {
	Base
}
`)
	e = findEdge(r, "p.Sub", "p.Base", "includes")
	if e == nil {
		t.Fatal("missing in-package includes edge")
	}
	if e.TargetImportPath != "" {
		t.Errorf("in-package embed must carry no annotation, got %q", e.TargetImportPath)
	}
}

func TestGenericQualifiedBaseCarriesImportPath(t *testing.T) {
	r := parse(t, `package p

import reg "example.com/mod/registry"

type Holder struct {
	r reg.Registry[int]
}

type Embedder struct {
	reg.Registry[int]
}
`)
	e := findEdge(r, "p.Holder", "reg.Registry", "composes")
	if e == nil {
		t.Fatal("missing composes edge for generic qualified base")
	}
	if e.TargetImportPath != "example.com/mod/registry" || e.TargetInPackage != "Registry" {
		t.Errorf("generic compose annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
	e = findEdge(r, "p.Embedder", "reg.Registry", "includes")
	if e == nil {
		t.Fatal("missing includes edge for generic qualified embed")
	}
	if e.TargetImportPath != "example.com/mod/registry" || e.TargetInPackage != "Registry" {
		t.Errorf("generic embed annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
}

func TestQualifierCallsCarryImportPath(t *testing.T) {
	r := parse(t, `package cmd

import (
	"fmt"
	repo_model "code.gitea.io/gitea/models/repo"
)

func run() {
	fmt.Println("x")
	repo_model.Search()
}
`)
	e := findEdge(r, "cmd.run", "fmt.Println", "calls")
	if e == nil {
		t.Fatal("missing stdlib qualifier call edge")
	}
	if e.TargetImportPath != "fmt" || e.TargetInPackage != "Println" {
		t.Errorf("stdlib qualifier annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
	e = findEdge(r, "cmd.run", "repo_model.Search", "calls")
	if e == nil {
		t.Fatal("missing aliased qualifier call edge")
	}
	if e.TargetImportPath != "code.gitea.io/gitea/models/repo" || e.TargetInPackage != "Search" {
		t.Errorf("aliased qualifier annotation = %q/%q", e.TargetImportPath, e.TargetInPackage)
	}
}

func TestQualifierConsultationOrderIsGoScopeNesting(t *testing.T) {
	// Function scope shadows the file block: an untyped local named like an
	// import must take the local branch (ambiguous, no annotation), never
	// the import table.
	r := parse(t, `package p

import "fmt"

func f() {
	fmt := helper()
	fmt.Print()
}
`)
	e := findEdge(r, "p.f", "fmt.Print", "calls")
	if e == nil {
		t.Fatal("missing shadowed-local call edge")
	}
	if e.TargetImportPath != "" {
		t.Errorf("local shadow must not consult the import table, got %q", e.TargetImportPath)
	}
	if e.Confidence != 0.8 {
		t.Errorf("known local with unknown type keeps the ambiguous confidence, got %v", e.Confidence)
	}
}

func TestUnknownOperandIsNotAPackageQualifier(t *testing.T) {
	// An operand that is neither a local nor an import-table name is, by
	// Go's scope law, a package-level identifier, never a package
	// qualifier. The old @1.0 emission let its dotted text exact-bind into
	// a same-named indexed package; it now rides ConfidenceUnresolved so
	// the resolver's gated fallback demotes it.
	r := parse(t, `package p

func f() {
	log.Error("boom")
}
`)
	e := findEdge(r, "p.f", "log.Error", "calls")
	if e == nil {
		t.Fatal("missing unknown-operand call edge")
	}
	if e.TargetImportPath != "" {
		t.Errorf("unknown operand must carry no annotation, got %q", e.TargetImportPath)
	}
	if e.Confidence != 0.5 {
		t.Errorf("unknown operand must ride ConfidenceUnresolved, got %v", e.Confidence)
	}
}

func TestQualifiedTypeTargetTolerantShapes(t *testing.T) {
	// A node without package/name fields (parser-tolerance territory) keeps
	// the literal text and claims no path.
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(Extractor{}.Grammar()); err != nil {
		t.Fatal(err)
	}
	src := []byte("package p\n\ntype X struct{ f T }\n")
	tree := parser.Parse(src, nil)
	defer tree.Close()
	w := &walker{source: src, imports: map[string]string{"T": "never/used"}}
	var ident *sitter.Node
	_ = extractWalk(tree.RootNode(), "type_identifier", func(n *sitter.Node) { ident = n })
	if ident == nil {
		t.Fatal("no type_identifier found")
	}
	got := w.qualifiedTypeTarget(ident)
	if got.qualified == "" || got.importPath != "" {
		t.Errorf("tolerant shape = %+v, want literal text with no annotation", got)
	}
}

func extractWalk(n *sitter.Node, kind string, fn func(*sitter.Node)) error {
	return extract.WalkNamedDescendants(n, kind, func(m *sitter.Node) error {
		fn(m)
		return nil
	})
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
