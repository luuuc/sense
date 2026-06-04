package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// parseEx parses src under the given extractor and returns the tree's
// root, the source bytes, and a cleanup func. Branch tests that drive
// individual handlers directly use it to reach the scope-qualified and
// guard paths the top-level walk never builds on its own (only class
// bodies grow scope, and a well-formed parse never hands a handler a nil
// node).
func parseEx(t *testing.T, ex extract.Extractor, src string) (*sitter.Node, []byte, func()) {
	t.Helper()
	p := sitter.NewParser()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		p.Close()
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		p.Close()
		t.Fatal("Parse returned nil tree")
	}
	return tree.RootNode(), source, func() { tree.Close(); p.Close() }
}

// firstNamed returns the first node of the given kind in a pre-order
// walk of n (n itself included), or nil if none.
func firstNamed(n *sitter.Node, kind string) *sitter.Node {
	if n == nil {
		return nil
	}
	if n.Kind() == kind {
		return n
	}
	for i := uint(0); i < n.NamedChildCount(); i++ {
		if found := firstNamed(n.NamedChild(i), kind); found != nil {
			return found
		}
	}
	return nil
}

// newWalker builds a walker wired to a fresh recorder over source, with
// the export sets pre-collected from root (so visibility resolves the
// same way the full Extract would).
func newWalker(root *sitter.Node, source []byte, path string) (*walker, *recorder) {
	r := &recorder{}
	w := &walker{
		source:       source,
		emit:         r,
		filePath:     path,
		stimulusName: inferStimulusController(path),
		pkgBindings:  map[string]string{},
	}
	w.collectExports(root)
	return w, r
}

// errorSweepSources exercises every emitting handler. The companion
// test runs each through failAfter at climbing budgets, so each handler's
// `if err := emit(...); err != nil { return err }` is hit when its own
// emission is the one that fails.
var errorSweepSources = map[string]struct {
	ex   extract.Extractor
	path string
	src  string
}{
	"typescript": {TypeScript{}, "widget.ts", `
import { thing } from "./thing";

export interface Shape extends Base {
  area(): number;
}

export type Mix = A & B & C<T>;

export enum Color { Red, Green }

export function compute(x: number): number {
  helper();
  return x;
}

export const factory = (): Shape => {
  build();
  return makeShape();
};

export const LIMIT = 100;

export const useLimit = () => {
  return LIMIT;
};

export class Widget extends Base implements Shape {
  area(): number {
    return this.compute();
  }
}

export { Button } from "./button";
export * from "./util";

async function lazyLoad() {
  await import("./mod");
}
`},
	"javascript": {JavaScript{}, "widget.js", `
import { thing } from "./thing";

export function compute(x) {
  helper();
  return x;
}

export const factory = () => {
  build();
  return makeShape();
};

export const LIMIT = 100;

export class Widget extends Base {
  area() {
    return this.compute();
  }
}

export { Button } from "./button";
export * from "./util";

async function lazyLoad() {
  await import("./mod");
}
`},
	"tsx": {TSX{}, "view.tsx", `
export function View() {
  return <Panel><Row.Cell /></Panel>;
}

export const Card = () => <Header />;
`},
	"stimulus": {JavaScript{}, "app/javascript/controllers/widget_controller.js", `
import { Controller } from "@hotwired/stimulus";

export default class extends Controller {
  static targets = ["name", "output"];
  static outlets = ["result"];

  connect() {
    this.refresh();
  }
}
`},
}

// TestExtractPropagatesEmitterErrors drives each handler family through a
// failing emitter at every budget below the total emission count, then at
// a budget above it. Every emission point must surface the first emitter
// error (faithful propagation, no swallowed writes), and a generous
// budget must succeed. The sweep is what reaches each handler's
// error-return branch.
func TestExtractPropagatesEmitterErrors(t *testing.T) {
	for name, tc := range errorSweepSources {
		t.Run(name, func(t *testing.T) {
			root, source, done := parseEx(t, tc.ex, tc.src)
			defer done()

			// Count total symbols and edges with a permissive run.
			rec := &recorder{}
			w, _ := newWalker(root, source, tc.path)
			w.emit = rec
			if err := drive(t, w, root); err != nil {
				t.Fatalf("permissive run errored: %v", err)
			}
			nSym, nEdg := len(rec.symbols), len(rec.edges)
			if nSym == 0 && nEdg == 0 {
				t.Fatalf("%s emitted nothing; sweep would be vacuous", name)
			}

			// Fail at each symbol budget below the total: an error must surface.
			for budget := 0; budget < nSym; budget++ {
				w, _ := newWalker(root, source, tc.path)
				w.emit = &failAfter{symbolsLeft: budget, edgesLeft: 1 << 20}
				if err := drive(t, w, root); err == nil {
					t.Errorf("symbol budget %d: expected error, got nil", budget)
				}
			}
			// Fail at each edge budget below the total: an error must surface.
			for budget := 0; budget < nEdg; budget++ {
				w, _ := newWalker(root, source, tc.path)
				w.emit = &failAfter{symbolsLeft: 1 << 20, edgesLeft: budget}
				if err := drive(t, w, root); err == nil {
					t.Errorf("edge budget %d: expected error, got nil", budget)
				}
			}
			// A generous budget must succeed.
			w2, _ := newWalker(root, source, tc.path)
			w2.emit = &failAfter{symbolsLeft: 1 << 20, edgesLeft: 1 << 20}
			if err := drive(t, w2, root); err != nil {
				t.Errorf("generous budget: unexpected error %v", err)
			}
		})
	}
}

// drive runs the full walker pipeline (the same order extractAll uses)
// against an already-collected walker.
func drive(t *testing.T, w *walker, root *sitter.Node) error {
	t.Helper()
	w.collectModuleConstants(root)
	if err := w.walk(root, nil); err != nil {
		return err
	}
	if err := w.walkDynamicImports(root); err != nil {
		return err
	}
	return nil
}

// TestScopedDeclarationsQualify drives the symbol handlers with a
// non-empty scope. The top-level walk only grows scope for class methods,
// so these branches (qualified = parent + "." + name) are reached by
// calling the handlers directly with an enclosing scope, asserting the
// qualified name and parent are composed correctly.
func TestScopedDeclarationsQualify(t *testing.T) {
	cases := []struct {
		name string
		kind string // node kind to locate
		src  string
		call func(w *walker, n *sitter.Node) error
		want string // expected qualified name
	}{
		{
			name: "class",
			kind: "class_declaration",
			src:  `class Inner extends Base {}`,
			call: func(w *walker, n *sitter.Node) error { return w.emitClassWithBody(n, "Inner", []string{"Outer"}) },
			want: "Outer.Inner",
		},
		{
			name: "interface",
			kind: "interface_declaration",
			src:  `interface Inner extends Base {}`,
			call: func(w *walker, n *sitter.Node) error { return w.handleInterface(n, []string{"Outer"}) },
			want: "Outer.Inner",
		},
		{
			name: "type_alias",
			kind: "type_alias_declaration",
			src:  `type Inner = A & B;`,
			call: func(w *walker, n *sitter.Node) error { return w.handleTypeAlias(n, []string{"Outer"}) },
			want: "Outer.Inner",
		},
		{
			name: "enum",
			kind: "enum_declaration",
			src:  `enum Inner { A, B }`,
			call: func(w *walker, n *sitter.Node) error { return w.handleEnum(n, []string{"Outer"}) },
			want: "Outer.Inner",
		},
		{
			name: "function",
			kind: "function_declaration",
			src:  `function inner() {}`,
			call: func(w *walker, n *sitter.Node) error { return w.emitFunctionWithBody(n, "inner", []string{"Outer"}) },
			want: "Outer.inner",
		},
		{
			name: "const",
			kind: "variable_declarator",
			src:  `const inner = 1;`,
			call: func(w *walker, n *sitter.Node) error { return w.handleVariableDeclarator(n, []string{"Outer"}) },
			want: "Outer.inner",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, source, done := parseEx(t, TypeScript{}, tc.src)
			defer done()
			node := firstNamed(root, tc.kind)
			if node == nil {
				t.Fatalf("no %s node in %q", tc.kind, tc.src)
			}
			w, r := newWalker(root, source, "scoped.ts")
			if err := tc.call(w, node); err != nil {
				t.Fatalf("handler returned %v", err)
			}
			if findSym(r, tc.want) == nil {
				t.Fatalf("missing scoped symbol %q; got %+v", tc.want, r.symbols)
			}
		})
	}
}

// TestWalkerNilGuards calls the walk entry points with a nil node. They
// must no-op rather than panic — defensive guards that keep one malformed
// CST subtree from taking down extraction.
func TestWalkerNilGuards(t *testing.T) {
	w := &walker{source: []byte(""), emit: &recorder{}, pkgBindings: map[string]string{}}

	// collectModuleConstants tolerates a nil root.
	w.collectModuleConstants(nil)

	// walk tolerates a nil node.
	if err := w.walk(nil, nil); err != nil {
		t.Errorf("walk(nil) = %v, want nil", err)
	}
}

// TestDynamicImportShapes drives the dynamic-import scanner across the
// argument shapes it must tolerate: a non-string callee argument, an
// empty string literal, and a well-formed module path. Only the last
// emits an imports edge.
func TestDynamicImportShapes(t *testing.T) {
	r := parseTS(t, `
const a = import(dep);
const b = import("");
const c = import("./real");
`, "imports.ts")
	if e := findEdg(r, "", "./real", "imports"); e == nil {
		t.Error("missing imports edge for ./real")
	}
	for _, bad := range []string{"dep", ""} {
		if findEdg(r, "", bad, "imports") != nil {
			t.Errorf("unexpected imports edge for %q", bad)
		}
	}
}

// TestModuleConstantDestructuringSkipped confirms a destructuring const
// binding is not tracked as a module constant (no identifier name field)
// and emits no symbol — Tier-Basic skips patterns.
func TestModuleConstantDestructuringSkipped(t *testing.T) {
	r := parseTS(t, `const { a, b } = makeConfig();`, "config.ts")
	if len(r.symbols) != 0 {
		t.Errorf("destructuring const emitted %d symbols, want 0: %+v", len(r.symbols), r.symbols)
	}
}

// TestAnonymousDefaultClassInIndexFile exercises the fall-through where a
// default-export class is anonymous AND no file-based name can be
// synthesized (index file): handleDefaultExport declines, the walk reaches
// handleClass with an empty name and no Stimulus context, and descends
// into the body without emitting a class symbol.
func TestAnonymousDefaultClassInIndexFile(t *testing.T) {
	r := parseTS(t, `export default class extends Base {
  run() { this.go(); }
}`, "index.ts")
	for _, s := range r.symbols {
		if s.Kind == "class" {
			t.Errorf("unexpected class symbol %q in anonymous index default", s.Qualified)
		}
	}
}

// TestHandlerGuardContracts verifies the small defensive guards that a
// well-formed parse never reaches but that keep extraction crash-safe and
// honest: empty composes targets, malformed re-export specifiers, nameless
// declarations, valueless type aliases, and non-target Stimulus fields all
// no-op rather than emitting a phantom or panicking.
func TestHandlerGuardContracts(t *testing.T) {
	t.Run("composes_empty_target", func(t *testing.T) {
		root, source, done := parseEx(t, TypeScript{}, `type X = A & B;`)
		defer done()
		w, r := newWalker(root, source, "x.ts")
		node := firstNamed(root, "type_alias_declaration")
		if err := w.emitComposesEdge("X", "", node); err != nil {
			t.Fatalf("emitComposesEdge: %v", err)
		}
		if len(r.edges) != 0 {
			t.Errorf("empty target emitted %d edges, want 0", len(r.edges))
		}
	})

	t.Run("reexport_name_no_children", func(t *testing.T) {
		root, source, done := parseEx(t, TypeScript{}, `const x = 1;`)
		defer done()
		w, _ := newWalker(root, source, "x.ts")
		// An identifier node has no named children, so reexportName returns "".
		leaf := firstNamed(root, "identifier")
		if leaf == nil {
			t.Fatal("no identifier node")
		}
		if got := w.reexportName(leaf); got != "" {
			t.Errorf("reexportName(leaf) = %q, want \"\"", got)
		}
	})

	t.Run("nameless_function", func(t *testing.T) {
		root, source, done := parseEx(t, TypeScript{}, `const f = function() {};`)
		defer done()
		w, r := newWalker(root, source, "x.ts")
		fn := firstNamed(root, "function_expression")
		if fn == nil {
			t.Fatal("no function_expression node")
		}
		if err := w.handleFunction(fn, nil); err != nil {
			t.Fatalf("handleFunction: %v", err)
		}
		if len(r.symbols) != 0 {
			t.Errorf("nameless function emitted %d symbols, want 0", len(r.symbols))
		}
	})

	t.Run("nameless_enum_typealias", func(t *testing.T) {
		// Drive handleEnum / handleTypeAlias with a node whose name field
		// is absent by reusing an unrelated node kind: both read the "name"
		// field, find none, and return without emitting.
		root, source, done := parseEx(t, TypeScript{}, `const x = 1;`)
		defer done()
		w, r := newWalker(root, source, "x.ts")
		leaf := firstNamed(root, "number")
		if leaf == nil {
			t.Fatal("no number node")
		}
		if err := w.handleEnum(leaf, nil); err != nil {
			t.Fatalf("handleEnum: %v", err)
		}
		if err := w.handleTypeAlias(leaf, nil); err != nil {
			t.Fatalf("handleTypeAlias: %v", err)
		}
		if len(r.symbols) != 0 {
			t.Errorf("nameless decls emitted %d symbols, want 0", len(r.symbols))
		}
	})

	t.Run("type_alias_no_value", func(t *testing.T) {
		// An interface_declaration has a "name" but no "value" field, so it
		// drives handleTypeAlias past the symbol emit and through the
		// no-value return.
		root, source, done := parseEx(t, TypeScript{}, `interface Iface {}`)
		defer done()
		w, r := newWalker(root, source, "x.ts")
		iface := firstNamed(root, "interface_declaration")
		if err := w.handleTypeAlias(iface, nil); err != nil {
			t.Fatalf("handleTypeAlias: %v", err)
		}
		if findSym(r, "Iface") == nil {
			t.Error("expected a type symbol for the valueless declaration")
		}
	})

	t.Run("stimulus_field_non_target", func(t *testing.T) {
		root, source, done := parseEx(t, JavaScript{},
			`class C { static values = ["x"]; }`)
		defer done()
		w, r := newWalker(root, source, "app/javascript/controllers/c_controller.js")
		field := firstNamed(root, "field_definition")
		if field == nil {
			t.Fatal("no field_definition node")
		}
		if err := w.handleStimulusField(field, "C"); err != nil {
			t.Fatalf("handleStimulusField: %v", err)
		}
		if len(r.symbols) != 0 || len(r.edges) != 0 {
			t.Errorf("non-target field emitted %d symbols / %d edges, want 0/0", len(r.symbols), len(r.edges))
		}
	})
}

// TestConstReferenceSkipsCallee confirms a module-constant identifier used
// as a call target does not emit a references edge — only value reads
// count, not invocations (the call already emits a calls edge).
func TestConstReferenceSkipsCallee(t *testing.T) {
	r := parseTS(t, `
const handler = makeHandler;
export function run() {
  handler();
  return handler;
}
`, "run.ts")
	// The bare read `return handler` is a reference; the call `handler()` is not.
	refs := 0
	for _, e := range r.edges {
		if e.TargetQualified == "handler" && string(e.Kind) == "references" {
			refs++
		}
	}
	if refs != 1 {
		t.Errorf("got %d references edges to handler, want 1 (read only, not call)", refs)
	}
}

// TestDefaultExportForms exercises the export_statement walk branches for
// the two default-export shapes the dispatch handles specially: an
// anonymous default class (synthesized under the file name) and a
// `export default <identifier>` re-binding (no declaration field).
func TestDefaultExportForms(t *testing.T) {
	t.Run("anonymous_class", func(t *testing.T) {
		r := parseTS(t, `export default class extends Base {
  go() { this.run(); }
}`, "widget.ts")
		if findSym(r, "Widget") == nil {
			t.Errorf("anonymous default class not synthesized under file name; got %+v", r.symbols)
		}
	})
	t.Run("identifier_rebind", func(t *testing.T) {
		// `export default foo` names no new symbol but must walk without error.
		r := parseTS(t, `function foo() {}
export default foo;`, "mod.ts")
		if findSym(r, "foo") == nil {
			t.Error("missing foo function symbol")
		}
	})
}

// TestDecoratedFieldNoName confirms a decorator preceding a non-method
// class member (a field) resolves to no decorated name — only decorated
// classes and methods feed the open-world dead-code gate.
func TestDecoratedFieldNoName(t *testing.T) {
	// Drives collectDecoratedNames via the full harvest; a decorated field
	// must not register a decorated symbol name.
	r := parseTS(t, `class C {
  @logged accessor x = 1;
  method() {}
}`, "c.ts")
	// No assertion on decorated set (internal); the test exists to drive
	// the class-body decorator branch that stops at a non-method sibling.
	if findSym(r, "C") == nil {
		t.Fatal("missing class C")
	}
}

// TestStimulusTargetArrayShapes drives the static-array reader past its two
// tolerated malformations: a non-array value (no targets emitted) and an
// array holding a non-string element (skipped, string siblings kept).
func TestStimulusTargetArrayShapes(t *testing.T) {
	r := parseJS(t, `import { Controller } from "@hotwired/stimulus";
export default class extends Controller {
  static targets = notAnArray;
  static outlets = [dynamic, "result"];
}`, "app/javascript/controllers/shape_controller.js")
	// notAnArray yields no target symbols.
	for _, s := range r.symbols {
		if len(s.Name) > 7 && s.Name[:7] == "target:" {
			t.Errorf("unexpected target symbol %q from non-array value", s.Name)
		}
	}
	// The string outlet "result" still resolves to a calls edge; the
	// identifier element is skipped, not fatal.
	outlets := 0
	for _, e := range r.edges {
		if string(e.Kind) == "calls" {
			outlets++
		}
	}
	if outlets != 1 {
		t.Errorf("got %d outlet calls edges, want 1 (string element only)", outlets)
	}
}

// TestIsConstDeclarationNonLexical confirms the const-keyword scan returns
// false for a node that carries no leading declaration keyword (every
// child named) — the contract handleLexicalDeclaration relies on to skip
// let/var and non-declarations.
func TestIsConstDeclarationNonLexical(t *testing.T) {
	root, source, done := parseEx(t, TypeScript{}, `const x = 1;`)
	defer done()
	// The program root's children are all named statements, so the scan
	// finds no leading keyword token and reports false.
	if isConstDeclaration(root, source) {
		t.Error("isConstDeclaration(program) = true, want false")
	}
}

// TestInferStimulusControllerBadExtension confirms a controllers/ path with
// an unrecognized extension is not treated as a Stimulus controller.
func TestInferStimulusControllerBadExtension(t *testing.T) {
	if got := inferStimulusController("app/javascript/controllers/foo_controller.rb"); got != "" {
		t.Errorf("inferStimulusController(.rb) = %q, want \"\"", got)
	}
}

// TestClassBodyGuards drives walkClassBody and handleStimulusField with
// nodes lacking the body/property fields a real class member always has —
// the defensive returns that keep one malformed subtree from panicking.
func TestClassBodyGuards(t *testing.T) {
	root, source, done := parseEx(t, TypeScript{}, `const x = 1;`)
	defer done()
	w, r := newWalker(root, source, "x.ts")
	leaf := firstNamed(root, "number")
	if leaf == nil {
		t.Fatal("no number node")
	}
	// walkClassBody on a node with no "body" field is a no-op.
	if err := w.walkClassBody(leaf, []string{"C"}, "C"); err != nil {
		t.Errorf("walkClassBody(no body) = %v, want nil", err)
	}
	// handleStimulusField on a node with no "property" field is a no-op.
	if err := w.handleStimulusField(leaf, "C"); err != nil {
		t.Errorf("handleStimulusField(no property) = %v, want nil", err)
	}
	if len(r.symbols) != 0 || len(r.edges) != 0 {
		t.Errorf("guards emitted %d symbols / %d edges, want 0/0", len(r.symbols), len(r.edges))
	}
}

// TestNamelessMemberGuards drives handleMethod and handleInterface with a
// node whose name field is absent: handleMethod no-ops, handleInterface
// falls back to walking children.
func TestNamelessMemberGuards(t *testing.T) {
	root, source, done := parseEx(t, TypeScript{}, `const x = 1;`)
	defer done()
	w, r := newWalker(root, source, "x.ts")
	leaf := firstNamed(root, "number")
	if err := w.handleMethod(leaf, []string{"C"}); err != nil {
		t.Errorf("handleMethod(nameless) = %v, want nil", err)
	}
	if err := w.handleInterface(leaf, nil); err != nil {
		t.Errorf("handleInterface(nameless) = %v, want nil", err)
	}
	if len(r.symbols) != 0 {
		t.Errorf("nameless members emitted %d symbols, want 0", len(r.symbols))
	}
}

// TestIsFunctionOrClassValue checks the value-kind classifier that keeps
// function/class-expression consts out of the module-constant binding set:
// a bare declarator (no initializer) and a plain value are not functions,
// an arrow initializer is.
func TestIsFunctionOrClassValue(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{`let x;`, false},             // no value field
		{`const n = 1;`, false},       // plain value
		{`const f = () => 1;`, true},  // arrow function
		{`const C = class {};`, true}, // class expression
	}
	for _, tc := range cases {
		root, _, done := parseEx(t, TypeScript{}, tc.src)
		decl := firstNamed(root, "variable_declarator")
		if decl == nil {
			done()
			t.Fatalf("no variable_declarator in %q", tc.src)
		}
		if got := isFunctionOrClassValue(decl); got != tc.want {
			t.Errorf("isFunctionOrClassValue(%q) = %v, want %v", tc.src, got, tc.want)
		}
		done()
	}
}
