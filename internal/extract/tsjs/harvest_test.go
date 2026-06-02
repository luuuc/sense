package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

func TestHarvestsMentionsOptIn(t *testing.T) {
	// All three extractor types must report they harvest mentions, so the scan
	// records each language as harvested and its symbols can earn `dead`.
	for _, ex := range []extract.MentionHarvester{TypeScript{}, TSX{}, JavaScript{}} {
		if !ex.HarvestsMentions() {
			t.Errorf("%T.HarvestsMentions() = false, want true", ex)
		}
	}
}

func TestHarvestMentions(t *testing.T) {
	// Every identifier / property / type token is mentioned EXCEPT a definition's
	// own name. A method invoked as `obj.render()` leaves a `render` property
	// mention, keeping a same-named method open-world.
	src := `function caller() {
  const svc = makeService();
  svc.render();
  return helper(svc);
}
function helper(x) { return x; }
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	mentions := toSet(r.mentions)
	for _, want := range []string{"makeService", "svc", "render", "helper", "x"} {
		if _, ok := mentions[want]; !ok {
			t.Errorf("mentions missing %q; got %v", want, r.mentions)
		}
	}
	// `caller` is a definition's own name and must be excluded — otherwise it
	// would cancel its own dead candidacy.
	if _, ok := mentions["caller"]; ok {
		t.Errorf("mentions should exclude the definition name 'caller'; got %v", r.mentions)
	}
}

func TestHarvestDispatchComputedAccess(t *testing.T) {
	// A literal computed-property key is a reflective dispatch target (the JS/TS
	// analog of Ruby `send`); a non-literal index names nothing statically.
	src := `function f(obj, name) {
  obj["render"]();
  obj["value"];
  obj[name]();
}
`
	r := extractTS(t, TypeScript{}, "mod.ts", src)
	dispatch := toSet(r.dispatch)
	for _, want := range []string{"render", "value"} {
		if _, ok := dispatch[want]; !ok {
			t.Errorf("dispatch missing %q; got %v", want, r.dispatch)
		}
	}
	if len(dispatch) != 2 {
		t.Errorf("dispatch = %v, want exactly {render, value} (the literal keys, not obj[name])", r.dispatch)
	}
}

func TestHarvestDecoratedClass(t *testing.T) {
	// A decorator on an exported class (decorator attaches to the export_statement)
	// and on a bare class (decorator attaches to the class_declaration) both yield
	// the class name in the decorated set.
	src := `@Component({ selector: 'app' })
export class AppComponent {}

@Injectable
class TokenService {}
`
	r := extractTS(t, TypeScript{}, "app.ts", src)
	decorated := toSet(r.decorated)
	for _, want := range []string{"AppComponent", "TokenService"} {
		if _, ok := decorated[want]; !ok {
			t.Errorf("decorated missing %q; got %v", want, r.decorated)
		}
	}
}

func TestHarvestDecoratedMethod(t *testing.T) {
	// A method decorator (`@Get()`) yields the method name in the decorated set.
	src := `class UsersController {
  @Get()
  list() {}

  plain() {}
}
`
	r := extractTS(t, TypeScript{}, "users.controller.ts", src)
	decorated := toSet(r.decorated)
	if _, ok := decorated["list"]; !ok {
		t.Errorf("decorated missing 'list'; got %v", r.decorated)
	}
	if _, ok := decorated["plain"]; ok {
		t.Errorf("decorated should not contain undecorated 'plain'; got %v", r.decorated)
	}
}

func TestHarvestDefaultExportName(t *testing.T) {
	r := extractTS(t, TypeScript{}, "page.tsx", "export default function Page() {}\n")
	if !contains(r.defaultExports, "Page") {
		t.Errorf("default exports = %v, want to contain Page", r.defaultExports)
	}
}

func TestHarvestComputedEmptyStringKey(t *testing.T) {
	// `obj[""]()` has no string fragment, so it contributes no dispatch name.
	r := extractTS(t, TypeScript{}, "mod.ts", "function f(obj) { obj[\"\"](); }\n")
	if len(r.dispatch) != 0 {
		t.Errorf("dispatch = %v, want empty for an empty-string computed key", r.dispatch)
	}
}

func TestHarvestDecoratorNoTargetMethod(t *testing.T) {
	// A decorator in a class body with no following method_definition (here it
	// precedes a field) contributes no decorated name — decoratedName returns "".
	src := `class C {
  @Watch()
  count = 0;
}
`
	r := extractTS(t, TypeScript{}, "c.ts", src)
	// `count` is a field, not a method_definition, so nothing is recorded for it.
	if contains(r.decorated, "count") {
		t.Errorf("decorated should not contain field 'count'; got %v", r.decorated)
	}
}

// failEmitter returns an error from exactly one harvest stream, leaving the rest
// succeeding, so each error-propagation path in emitHarvest is exercised.
type failEmitter struct {
	failMention, failDispatch, failDecorated, failDefault bool
}

func (failEmitter) Symbol(extract.EmittedSymbol) error { return nil }
func (failEmitter) Edge(extract.EmittedEdge) error     { return nil }
func (e failEmitter) MentionName(string) error {
	if e.failMention {
		return errBoom
	}
	return nil
}
func (e failEmitter) DispatchName(string) error {
	if e.failDispatch {
		return errBoom
	}
	return nil
}
func (e failEmitter) TSDecoratedName(string) error {
	if e.failDecorated {
		return errBoom
	}
	return nil
}
func (e failEmitter) TSDefaultExportName(string) error {
	if e.failDefault {
		return errBoom
	}
	return nil
}

var errBoom = errBoomType{}

type errBoomType struct{}

func (errBoomType) Error() string { return "boom" }

func TestEmitHarvestPropagatesEmitterErrors(t *testing.T) {
	src := `@Component({})
export default function Page(obj) { obj["render"](); }
`
	root, source, cleanup := parseRoot(t, TypeScript{}, src)
	defer cleanup()
	for name, em := range map[string]failEmitter{
		"mention":   {failMention: true},
		"dispatch":  {failDispatch: true},
		"decorated": {failDecorated: true},
		"default":   {failDefault: true},
	} {
		if err := emitHarvest(root, source, "page.ts", em); err == nil {
			t.Errorf("%s: emitHarvest returned nil, want the emitter error propagated", name)
		}
	}
}

func parseRoot(t *testing.T, ex extract.Extractor, src string) (*sitter.Node, []byte, func()) {
	t.Helper()
	p := sitter.NewParser()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	if tree == nil {
		t.Fatal("nil tree")
	}
	return tree.RootNode(), []byte(src), func() { tree.Close(); p.Close() }
}

func toSet(xs []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, x := range xs {
		out[x] = struct{}{}
	}
	return out
}
