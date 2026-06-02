package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// harvestRecorder captures emitted symbols plus every optional harvest stream so
// the visibility and harvest tests can assert on raw-byte extraction without the
// scan harness.
type harvestRecorder struct {
	symbols        []extract.EmittedSymbol
	edges          []extract.EmittedEdge
	mentions       []string
	dispatch       []string
	decorated      []string
	defaultExports []string
}

func (r *harvestRecorder) Symbol(s extract.EmittedSymbol) error {
	r.symbols = append(r.symbols, s)
	return nil
}
func (r *harvestRecorder) Edge(e extract.EmittedEdge) error { r.edges = append(r.edges, e); return nil }
func (r *harvestRecorder) MentionName(n string) error       { r.mentions = append(r.mentions, n); return nil }
func (r *harvestRecorder) DispatchName(n string) error {
	r.dispatch = append(r.dispatch, n)
	return nil
}
func (r *harvestRecorder) TSDecoratedName(n string) error {
	r.decorated = append(r.decorated, n)
	return nil
}
func (r *harvestRecorder) TSDefaultExportName(n string) error {
	r.defaultExports = append(r.defaultExports, n)
	return nil
}

// extractTS parses src with the given extractor and returns the recorded output.
func extractTS(t *testing.T, ex extract.Extractor, path, src string) *harvestRecorder {
	t.Helper()
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()
	r := &harvestRecorder{}
	if err := ex.Extract(tree, []byte(src), path, r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

// visibilityOfSym returns the recorded Visibility for the symbol with the given
// name, or "<missing>" when no such symbol was emitted.
func visibilityOfSym(r *harvestRecorder, name string) string {
	for _, s := range r.symbols {
		if s.Name == name {
			return s.Visibility
		}
	}
	return "<missing>"
}

func TestVisibilityExportForms(t *testing.T) {
	src := `export function exportedFn() {}
function privateFn() {}
export class ExportedClass {}
class PrivateClass {}
export const exportedConst = 5;
const privateConst = 5;
export interface ExportedIface {}
interface PrivateIface {}
export type ExportedType = string;
type PrivateType = string;
export enum ExportedEnum { A }
enum PrivateEnum { A }
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	cases := map[string]string{
		"exportedFn":    "public",
		"privateFn":     "private",
		"ExportedClass": "public",
		"PrivateClass":  "private",
		"exportedConst": "public",
		"privateConst":  "private",
		"ExportedIface": "public",
		"PrivateIface":  "private",
		"ExportedType":  "public",
		"PrivateType":   "private",
		"ExportedEnum":  "public",
		"PrivateEnum":   "private",
	}
	for name, want := range cases {
		if got := visibilityOfSym(r, name); got != want {
			t.Errorf("%s visibility = %q, want %q", name, got, want)
		}
	}
}

func TestVisibilityExportClause(t *testing.T) {
	// `export { g }` / `export { h as alias }` mark the LOCAL name public even
	// though the declaration is separate from the export statement.
	src := `function g() {}
function h() {}
function unexported() {}
export { g };
export { h as alias };
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	if got := visibilityOfSym(r, "g"); got != "public" {
		t.Errorf("g visibility = %q, want public (export { g })", got)
	}
	if got := visibilityOfSym(r, "h"); got != "public" {
		t.Errorf("h visibility = %q, want public (export { h as alias })", got)
	}
	if got := visibilityOfSym(r, "unexported"); got != "private" {
		t.Errorf("unexported visibility = %q, want private", got)
	}
}

func TestVisibilityDefaultExportNamed(t *testing.T) {
	src := "export default function Page() {}\n"
	r := extractTS(t, TypeScript{}, "page.ts", src)
	if got := visibilityOfSym(r, "Page"); got != "public" {
		t.Errorf("Page visibility = %q, want public (export default)", got)
	}
	if !contains(r.defaultExports, "Page") {
		t.Errorf("default exports = %v, want to contain Page", r.defaultExports)
	}
}

func TestVisibilityDefaultExportAnonymous(t *testing.T) {
	// An anonymous default export is synthesized under the file-based name and
	// recognized as both public and a default export.
	src := "export default function() {}\n"
	r := extractTS(t, TypeScript{}, "widget.ts", src)
	if got := visibilityOfSym(r, "Widget"); got != "public" {
		t.Errorf("Widget visibility = %q, want public (anonymous default)", got)
	}
	if !contains(r.defaultExports, "Widget") {
		t.Errorf("default exports = %v, want to contain Widget", r.defaultExports)
	}
}

func TestVisibilityDefaultExportIdentifier(t *testing.T) {
	// `export default foo` where foo is declared above marks foo public + default.
	src := `function foo() {}
export default foo;
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	if got := visibilityOfSym(r, "foo"); got != "public" {
		t.Errorf("foo visibility = %q, want public (export default foo)", got)
	}
	if !contains(r.defaultExports, "foo") {
		t.Errorf("default exports = %v, want to contain foo", r.defaultExports)
	}
}

func TestVisibilityDefaultExportAnonymousClass(t *testing.T) {
	// An anonymous default-export class is synthesized under the file-based name.
	src := "export default class { method() {} }\n"
	r := extractTS(t, TypeScript{}, "service.ts", src)
	if got := visibilityOfSym(r, "Service"); got != "public" {
		t.Errorf("Service visibility = %q, want public (anonymous default class)", got)
	}
}

func TestVisibilityDefaultExportExpression(t *testing.T) {
	// `export default <expr>` with a literal value emits no symbol, so it binds
	// no name — no phantom file-based name is recorded as a default export.
	src := "export default 42;\n"
	r := extractTS(t, TypeScript{}, "answer.ts", src)
	if len(r.defaultExports) != 0 {
		t.Errorf("default exports = %v, want empty (a literal default binds no symbol)", r.defaultExports)
	}
	if len(r.symbols) != 0 {
		t.Errorf("symbols = %v, want none for a literal default export", r.symbols)
	}
}

func TestVisibilityDestructuringConstSkipped(t *testing.T) {
	// Destructuring patterns are skipped in Tier-Basic; the export pass must not
	// crash on `export const { a, b } = obj`.
	src := "export const { a, b } = obj;\n"
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	// No named const symbol is emitted for a destructuring declarator.
	if got := visibilityOfSym(r, "a"); got != "<missing>" {
		t.Errorf("destructured 'a' should not be emitted as a symbol; got visibility %q", got)
	}
}

func TestVisibilityReexportIsPublic(t *testing.T) {
	// `export { Button } from './Button'` emits a public symbol — a re-export is
	// definitionally an export, never a module-private dead candidate.
	src := `export { Button } from './Button';
`
	r := extractTS(t, TypeScript{}, "index.ts", src)
	if got := visibilityOfSym(r, "Button"); got != "public" {
		t.Errorf("re-exported Button visibility = %q, want public", got)
	}
}

func TestVisibilityMethodTracksClass(t *testing.T) {
	// A method of an exported class is reachable on that class (public); a method
	// of a module-private class is itself module-private.
	src := `export class Service {
  doWork() {}
}
class Helper {
  assist() {}
}
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	if got := visibilityOfSym(r, "doWork"); got != "public" {
		t.Errorf("doWork visibility = %q, want public (exported class)", got)
	}
	if got := visibilityOfSym(r, "assist"); got != "private" {
		t.Errorf("assist visibility = %q, want private (module-private class)", got)
	}
}

func TestVisibilityJavaScript(t *testing.T) {
	// The same pass runs for JavaScript (one walker, three grammars).
	src := `export function used() {}
function local() {}
`
	r := extractTS(t, JavaScript{}, "mod.js", src)
	if got := visibilityOfSym(r, "used"); got != "public" {
		t.Errorf("used visibility = %q, want public", got)
	}
	if got := visibilityOfSym(r, "local"); got != "private" {
		t.Errorf("local visibility = %q, want private", got)
	}
}

func TestVisibilityScriptFileSymbolsArePublic(t *testing.T) {
	// A file with no top-level import/export is a global *script* (a bundler
	// runtime, a `/// <reference>` ambient file, classic <script> code), not an ES
	// module. Its symbols share the global scope and are reachable by concatenation,
	// so they are "public" — never a closed-world dead candidate.
	src := `function registerChunkList() {}
const ASSET = "x";
`
	r := extractTS(t, TypeScript{}, "runtime-backend.ts", src)
	if got := visibilityOfSym(r, "registerChunkList"); got != "public" {
		t.Errorf("registerChunkList visibility = %q, want public (script file, no import/export)", got)
	}
	if got := visibilityOfSym(r, "ASSET"); got != "public" {
		t.Errorf("ASSET visibility = %q, want public (script file)", got)
	}
}

func TestVisibilityDynamicImportOnlyIsScript(t *testing.T) {
	// A file whose only "import" is a dynamic import() expression has no top-level
	// import/export statement, so it is still a script — its private-looking
	// symbols are global and stay public.
	src := `const mod = import("./x");
function helper() {}
`
	r := extractTS(t, TypeScript{}, "loader.ts", src)
	if got := visibilityOfSym(r, "helper"); got != "public" {
		t.Errorf("helper visibility = %q, want public (dynamic import only = script)", got)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
