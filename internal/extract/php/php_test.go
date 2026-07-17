package php

import (
	"errors"
	"strings"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
	"github.com/luuuc/sense/internal/model"
)

var errBoom = errors.New("boom")

// rec is the recording emitter, with per-channel countdown fault injection
// (1-based; 0 = never fail). It implements the optional Mention and
// LangspecHarvest extensions.
type rec struct {
	symbols   []extract.EmittedSymbol
	edges     []extract.EmittedEdge
	mentions  []string
	annotated []string

	nSym, nEdge, nMention, nAnnotated                        int
	failSymbolAt, failEdgeAt, failMentionAt, failAnnotatedAt int
}

func (r *rec) Symbol(s extract.EmittedSymbol) error {
	r.nSym++
	if r.failSymbolAt > 0 && r.nSym == r.failSymbolAt {
		return errBoom
	}
	r.symbols = append(r.symbols, s)
	return nil
}

func (r *rec) Edge(e extract.EmittedEdge) error {
	r.nEdge++
	if r.failEdgeAt > 0 && r.nEdge == r.failEdgeAt {
		return errBoom
	}
	r.edges = append(r.edges, e)
	return nil
}

func (r *rec) MentionName(name string) error {
	r.nMention++
	if r.failMentionAt > 0 && r.nMention == r.failMentionAt {
		return errBoom
	}
	r.mentions = append(r.mentions, name)
	return nil
}

func (r *rec) LangspecAnnotatedName(name string) error {
	r.nAnnotated++
	if r.failAnnotatedAt > 0 && r.nAnnotated == r.failAnnotatedAt {
		return errBoom
	}
	r.annotated = append(r.annotated, name)
	return nil
}

// bareEmitter implements only the core Emitter, so the optional-interface
// type assertions take their false branches.
type bareEmitter struct{ symbols int }

func (b *bareEmitter) Symbol(extract.EmittedSymbol) error { b.symbols++; return nil }
func (b *bareEmitter) Edge(extract.EmittedEdge) error     { return nil }

func parse(t *testing.T, src string) *sitter.Tree {
	t.Helper()
	parser := sitter.NewParser()
	t.Cleanup(parser.Close)
	if err := parser.SetLanguage(grammars.PHP()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := parser.Parse([]byte(src), nil)
	t.Cleanup(tree.Close)
	return tree
}

func run(t *testing.T, src string, em extract.Emitter) error {
	t.Helper()
	return Extractor{}.Extract(parse(t, src), []byte(src), "f.php", em)
}

func mustRun(t *testing.T, src string) *rec {
	t.Helper()
	em := &rec{}
	if err := run(t, src, em); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return em
}

func (r *rec) symbol(t *testing.T, qualified string) extract.EmittedSymbol {
	t.Helper()
	for _, s := range r.symbols {
		if s.Qualified == qualified {
			return s
		}
	}
	t.Fatalf("no symbol %q in %v", qualified, r.qualifiedNames())
	return extract.EmittedSymbol{}
}

func (r *rec) qualifiedNames() []string {
	names := make([]string, len(r.symbols))
	for i, s := range r.symbols {
		names[i] = s.Qualified
	}
	return names
}

func (r *rec) edge(t *testing.T, kind model.EdgeKind, source, target string) extract.EmittedEdge {
	t.Helper()
	for _, e := range r.edges {
		if e.Kind == kind && e.SourceQualified == source && e.TargetQualified == target {
			return e
		}
	}
	t.Fatalf("no %s edge %s -> %s in %v", kind, source, target, r.edges)
	return extract.EmittedEdge{}
}

func (r *rec) hasEdgeTarget(target string) bool {
	for _, e := range r.edges {
		if e.TargetQualified == target {
			return true
		}
	}
	return false
}

func TestExtractorContract(t *testing.T) {
	ex := Extractor{}
	if ex.Language() != "php" {
		t.Errorf("Language = %q", ex.Language())
	}
	if got := ex.Extensions(); len(got) != 1 || got[0] != ".php" {
		t.Errorf("Extensions = %v", got)
	}
	if ex.Tier() != extract.TierFull {
		t.Errorf("Tier = %q", ex.Tier())
	}
	if ex.Grammar() == nil {
		t.Error("Grammar returned nil")
	}
	if !ex.HarvestsMentions() {
		t.Error("HarvestsMentions must be true")
	}
	if err := ex.Extract(nil, nil, "f.php", &rec{}); err != nil {
		t.Errorf("nil tree: %v", err)
	}
	if extract.LanguageTier("php") != string(extract.TierFull) {
		t.Errorf("languageTiers[php] = %q, want full", extract.LanguageTier("php"))
	}
}

func TestNamespaceScopesFollowingDeclarations(t *testing.T) {
	em := mustRun(t, `<?php
namespace App\Models;
class Order {}
function helper(): void {}
`)
	order := em.symbol(t, `App\Models\Order`)
	if order.ParentQualified != `App\Models` || order.Kind != model.KindClass {
		t.Errorf("Order = %+v", order)
	}
	if h := em.symbol(t, `App\Models\helper`); h.Kind != model.KindFunction {
		t.Errorf("helper = %+v", h)
	}
	if ns := em.symbol(t, `App\Models`); ns.Kind != model.KindModule {
		t.Errorf("namespace = %+v", ns)
	}
}

func TestCompoundNamespaceRestoresOuterScope(t *testing.T) {
	em := mustRun(t, `<?php
namespace Wrapped { class Inner {} }
class TopLevel {}
`)
	em.symbol(t, `Wrapped\Inner`)
	if s := em.symbol(t, `TopLevel`); s.ParentQualified != "" {
		t.Errorf("TopLevel parent = %q, want top level", s.ParentQualified)
	}
}

func TestUseAliasExpandsInheritanceAndTraits(t *testing.T) {
	em := mustRun(t, `<?php
namespace App;
use Vendor\Base as VendorBase;
use App\Concerns\Billable;
class Order extends VendorBase implements \Core\Chargeable {
    use Billable, Loggable;
}
`)
	em.edge(t, model.EdgeInherits, `App\Order`, `Vendor\Base`)
	em.edge(t, model.EdgeInherits, `App\Order`, `Core\Chargeable`)
	em.edge(t, model.EdgeIncludes, `App\Order`, `App\Concerns\Billable`)
	em.edge(t, model.EdgeIncludes, `App\Order`, `App\Loggable`)
	em.edge(t, model.EdgeImports, `App`, `Vendor\Base`)
	em.edge(t, model.EdgeImports, `App`, `App\Concerns\Billable`)
}

func TestMultiClauseUseEmitsEveryImport(t *testing.T) {
	em := mustRun(t, `<?php
use App\A, App\B;
`)
	em.edge(t, model.EdgeImports, "", `App\A`)
	em.edge(t, model.EdgeImports, "", `App\B`)
}

func TestVisibilityAndReceiver(t *testing.T) {
	em := mustRun(t, `<?php
class Foo {
    public function pub(): void {}
    private function priv(): void {}
    protected function prot(): void {}
    function def(): void {}
    public static function fab(): void {}
}
`)
	cases := map[string]struct{ vis, recv string }{
		`Foo\pub`:  {"public", extract.ReceiverInstance},
		`Foo\priv`: {"private", extract.ReceiverInstance},
		`Foo\prot`: {"protected", extract.ReceiverInstance},
		`Foo\def`:  {"public", extract.ReceiverInstance},
		`Foo\fab`:  {"public", extract.ReceiverSingleton},
	}
	for qualified, want := range cases {
		s := em.symbol(t, qualified)
		if s.Visibility != want.vis || s.Receiver != want.recv {
			t.Errorf("%s = vis %q recv %q, want %+v", qualified, s.Visibility, s.Receiver, want)
		}
	}
}

func TestTypeKinds(t *testing.T) {
	em := mustRun(t, `<?php
interface Payable { public function pay(): void; }
trait Billable {}
enum Status {}
`)
	if s := em.symbol(t, `Payable`); s.Kind != model.KindInterface {
		t.Errorf("interface kind = %q", s.Kind)
	}
	if s := em.symbol(t, `Billable`); s.Kind != model.KindClass {
		t.Errorf("trait kind = %q", s.Kind)
	}
	if s := em.symbol(t, `Status`); s.Kind != model.KindType {
		t.Errorf("enum kind = %q", s.Kind)
	}
	if s := em.symbol(t, `Payable\pay`); s.Visibility != "public" {
		t.Errorf("interface method = %+v", s)
	}
}

func TestGuardedDeclarationStillExtracted(t *testing.T) {
	em := mustRun(t, `<?php
if (!class_exists('Late')) {
    class Late {}
}
`)
	em.symbol(t, `Late`)
}

func TestAttributeHarvest(t *testing.T) {
	em := mustRun(t, `<?php
#[Injectable]
class Ctrl {
    #[Route("/x")] public function handle(): void {}
    public function plain(): void {}
}
#[Command]
function cli(): void {}
`)
	for _, want := range []string{"Ctrl", "handle", "cli"} {
		found := false
		for _, got := range em.annotated {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected %q annotated, got %v", want, em.annotated)
		}
	}
	for _, got := range em.annotated {
		if got == "plain" {
			t.Errorf("plain must not be annotated: %v", em.annotated)
		}
	}
}

func TestMentionsExcludeDefinitionNames(t *testing.T) {
	em := mustRun(t, `<?php
class Order {
    public function caller(): void {
        $this->helper();
        $svc->process();
    }
    private function helper(): void {}
}
`)
	has := func(name string) bool {
		for _, m := range em.mentions {
			if m == name {
				return true
			}
		}
		return false
	}
	if !has("helper") || !has("process") {
		t.Errorf("expected call targets mentioned, got %v", em.mentions)
	}
	if has("caller") || has("Order") {
		t.Errorf("definition names must be excluded, got %v", em.mentions)
	}
}

func TestBareEmitterSkipsOptionalHarvests(t *testing.T) {
	em := &bareEmitter{}
	err := run(t, `<?php
#[Attr]
class Foo { public function m(): void { $x->uncommonCall(); } }
`, em)
	if err != nil {
		t.Fatalf("Extract with bare emitter: %v", err)
	}
	if em.symbols == 0 {
		t.Error("bare emitter received no symbols")
	}
}

// TestEmitterErrorsPropagate drives every emit site's error return with
// countdown fault injection.
func TestEmitterErrorsPropagate(t *testing.T) {
	const full = `<?php
namespace App;
use App\Concerns\Billable;
#[Injectable]
class Order extends Base {
    use Billable;
    #[Route] public function m(Logger $log): void { $log->info("x"); }
}
function f(): void {}
`
	cases := map[string]*rec{
		"namespace symbol": {failSymbolAt: 1},
		"class symbol":     {failSymbolAt: 2},
		"method symbol":    {failSymbolAt: 3},
		"function symbol":  {failSymbolAt: 4},
		"import edge":      {failEdgeAt: 1},
		"inherit edge":     {failEdgeAt: 2},
		"trait-use edge":   {failEdgeAt: 3},
		"call edge":        {failEdgeAt: 4},
		"class annotated":  {failAnnotatedAt: 1},
		"method annotated": {failAnnotatedAt: 2},
		"mention":          {failMentionAt: 1},
	}
	for name, em := range cases {
		t.Run(name, func(t *testing.T) {
			if err := run(t, full, em); !errors.Is(err, errBoom) {
				t.Errorf("want injected error, got %v", err)
			}
		})
	}
}

func TestNamelessDeclarationsSkipped(t *testing.T) {
	// A broken class header and an anonymous function must not emit symbols.
	em := mustRun(t, `<?php
$fn = function () { return 1; };
`)
	for _, s := range em.symbols {
		if s.Kind == model.KindFunction || s.Kind == model.KindClass {
			t.Errorf("unexpected symbol %+v", s)
		}
	}
}

func TestResolveName(t *testing.T) {
	w := &walker{ns: "App", uses: map[string]string{"Alias": `Vendor\Pkg`}}
	cases := map[string]string{
		`\Global\Thing`: `Global\Thing`,
		`Alias`:         `Vendor\Pkg`,
		`Alias\Sub`:     `Vendor\Pkg\Sub`,
		`Local`:         `App\Local`,
		`Nested\Local`:  `App\Nested\Local`,
		``:              ``,
		`  `:            ``,
	}
	for in, want := range cases {
		if got := w.resolveName(in); got != want {
			t.Errorf("resolveName(%q) = %q, want %q", in, got, want)
		}
	}
	bare := &walker{uses: map[string]string{}}
	if got := bare.resolveName("Thing"); got != "Thing" {
		t.Errorf("no-namespace resolveName = %q", got)
	}
}

func TestParityWithLangspecBaseline(t *testing.T) {
	// The retired langspec extractor's golden (checked in as
	// testdata/langspec_parity_baseline.json, captured before the promotion)
	// is the permanent regression floor on symbols / visibility / imports /
	// inheritance. Call edges are deliberately excluded: dropping langspec's
	// unresolved-receiver guesses is the point of the promotion. Qualified
	// names may gain a namespace prefix (the scoping fix), so matching is
	// exact-or-suffix on the baseline name.
	baseline := loadParityBaseline(t)
	em := mustRunFile(t, "../testdata/php/basic.php")

	for _, want := range baseline.Symbols {
		if !hasParitySymbol(em, want) {
			t.Errorf("baseline symbol %q (%s) missing from promoted output %v",
				want.Qualified, want.Kind, em.qualifiedNames())
		}
	}
	for _, want := range baseline.Edges {
		if want.Kind != "imports" && want.Kind != "inherits" {
			continue
		}
		if !hasParityEdge(em, want) {
			t.Errorf("baseline %s edge -> %q missing from promoted output %v",
				want.Kind, want.Target, em.edges)
		}
	}
}

func hasParitySymbol(em *rec, want paritySymbol) bool {
	for _, s := range em.symbols {
		if s.Name == want.Name && string(s.Kind) == want.Kind &&
			s.Visibility == want.Visibility && qualifiedSuffix(s.Qualified, want.Qualified) {
			return true
		}
	}
	return false
}

func hasParityEdge(em *rec, want parityEdge) bool {
	for _, e := range em.edges {
		if string(e.Kind) == want.Kind && qualifiedSuffix(e.TargetQualified, want.Target) {
			return true
		}
	}
	return false
}

// qualifiedSuffix reports whether got equals base or ends with `\`+base -
// the namespace-prefix upgrade is the one sanctioned difference.
func qualifiedSuffix(got, base string) bool {
	return got == base || strings.HasSuffix(got, `\`+base)
}
