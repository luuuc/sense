package python

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// harvestRecorder captures emitted symbols plus every optional harvest stream so
// the harvest tests can assert on raw-byte extraction without the scan harness.
type harvestRecorder struct {
	symbols    []extract.EmittedSymbol
	edges      []extract.EmittedEdge
	mentions   []string
	dispatch   []string
	decorated  []string
	routes     []string
	django     []string
	allExports []string
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
func (r *harvestRecorder) PythonDecoratedName(n string) error {
	r.decorated = append(r.decorated, n)
	return nil
}
func (r *harvestRecorder) PythonRouteName(n string) error { r.routes = append(r.routes, n); return nil }
func (r *harvestRecorder) PythonDjangoName(n string) error {
	r.django = append(r.django, n)
	return nil
}
func (r *harvestRecorder) PythonAllExportName(n string) error {
	r.allExports = append(r.allExports, n)
	return nil
}

// extractHarvest parses src and returns the recorded output including harvests.
func extractHarvest(t *testing.T, src string) *harvestRecorder {
	t.Helper()
	ex := Extractor{}
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
	if err := ex.Extract(tree, []byte(src), "test.py", r); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return r
}

func toSet(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func TestHarvestsMentionsOptIn(t *testing.T) {
	if !(Extractor{}).HarvestsMentions() {
		t.Error("Extractor.HarvestsMentions() = false, want true")
	}
}

func TestHarvestMentions(t *testing.T) {
	// Every identifier is mentioned EXCEPT a definition's own name. A method
	// invoked as `obj.render()` leaves a `render` identifier mention, keeping a
	// same-named method open-world.
	src := `
def caller():
    svc = make_service()
    svc.render()
    return helper(svc)

def helper(x):
    return x
`
	mentions := toSet(extractHarvest(t, src).mentions)
	for _, want := range []string{"make_service", "svc", "render", "helper", "x"} {
		if !mentions[want] {
			t.Errorf("mentions missing %q; got %v", want, mentions)
		}
	}
	// `caller` and `helper` are definition names and must be excluded — otherwise
	// each would cancel its own dead candidacy. (`helper` reappears via its call
	// site, so it IS present; `caller` has no call site and must be absent.)
	if mentions["caller"] {
		t.Errorf("mentions should exclude the definition name 'caller'; got %v", mentions)
	}
}

func TestHarvestReflectiveDispatch(t *testing.T) {
	// getattr/setattr/hasattr with a literal name are reflective dispatch targets;
	// a non-literal name argument names nothing statically.
	src := `
def f(obj, name):
    getattr(obj, "render")
    setattr(obj, "value", 1)
    hasattr(obj, "ready")
    getattr(obj, name)
`
	dispatch := toSet(extractHarvest(t, src).dispatch)
	for _, want := range []string{"render", "value", "ready"} {
		if !dispatch[want] {
			t.Errorf("dispatch missing %q; got %v", want, dispatch)
		}
	}
	if len(dispatch) != 3 {
		t.Errorf("dispatch = %v, want exactly {render, value, ready} (literal args only)", dispatch)
	}
}

func TestHarvestDecoratorReach(t *testing.T) {
	src := `
@property
def name(self):
    return self._name

@staticmethod
def build():
    pass

@app.route("/")
def index():
    pass

@router.post("/orders")
def create_order():
    pass

@app.websocket("/ws")
def socket():
    pass

@receiver(post_save, sender=User)
def on_save(sender, **kwargs):
    pass

@admin.register(Article)
class ArticleAdmin:
    pass

@dataclass
class Point:
    pass
`
	r := extractHarvest(t, src)
	decorated, routes, django := toSet(r.decorated), toSet(r.routes), toSet(r.django)

	for _, want := range []string{"name", "build", "index", "create_order", "socket", "on_save", "ArticleAdmin", "Point"} {
		if !decorated[want] {
			t.Errorf("decorated missing %q; got %v", want, decorated)
		}
	}
	for _, want := range []string{"index", "create_order", "socket"} {
		if !routes[want] {
			t.Errorf("routes missing %q; got %v", want, routes)
		}
	}
	if routes["name"] || routes["on_save"] {
		t.Errorf("routes over-captured non-route decorators; got %v", routes)
	}
	for _, want := range []string{"on_save", "ArticleAdmin"} {
		if !django[want] {
			t.Errorf("django missing %q; got %v", want, django)
		}
	}
	if django["index"] {
		t.Errorf("django over-captured a route handler; got %v", django)
	}
}

func TestHarvestDecoratorUnusualForms(t *testing.T) {
	// A subscript decorator (PEP 614 relaxed grammar) has no trailing name, so it
	// classifies as neither route nor Django — but the symbol is still recorded as
	// decorated, keeping it open-world. An empty-bodied decorated def with a name
	// still harvests. This exercises decoratorLastName's non-identifier/attribute
	// fall-through.
	src := `
@registry[0]
def handler():
    pass
`
	r := extractHarvest(t, src)
	if !toSet(r.decorated)["handler"] {
		t.Errorf("decorated missing 'handler'; got %v", r.decorated)
	}
	if toSet(r.routes)["handler"] || toSet(r.django)["handler"] {
		t.Errorf("subscript decorator should classify as neither route nor django; routes=%v django=%v", r.routes, r.django)
	}
}

func TestHarvestAllExports(t *testing.T) {
	// A list, a tuple, and a `+=` augmentation all contribute; a private name in
	// __all__ is captured (the override the underscore convention can't express).
	src := `
__all__ = ["public_api", "_reexported_private"]
__all__ += ("more_api",)

def public_api():
    pass

def _reexported_private():
    pass
`
	all := toSet(extractHarvest(t, src).allExports)
	for _, want := range []string{"public_api", "_reexported_private", "more_api"} {
		if !all[want] {
			t.Errorf("__all__ exports missing %q; got %v", want, all)
		}
	}
}

func TestHarvestAllExportsNonCollectionRHS(t *testing.T) {
	// A non-list/tuple/set RHS (an alias) contributes nothing — the safe
	// direction; the mention gate still backstops non-string references.
	src := `__all__ = OTHER_LIST`
	all := extractHarvest(t, src).allExports
	if len(all) != 0 {
		t.Errorf("__all__ = alias should harvest nothing; got %v", all)
	}
}

// failEmitter returns an error from a chosen harvest stream so the per-stream
// error-propagation branches in emitHarvest are exercised.
type failEmitter struct {
	extract.Emitter
	failOn string
}

func (failEmitter) Symbol(extract.EmittedSymbol) error { return nil }
func (failEmitter) Edge(extract.EmittedEdge) error     { return nil }
func (e failEmitter) MentionName(string) error         { return failIf(e.failOn, "mention") }
func (e failEmitter) DispatchName(string) error        { return failIf(e.failOn, "dispatch") }
func (e failEmitter) PythonDecoratedName(string) error { return failIf(e.failOn, "decorated") }
func (e failEmitter) PythonRouteName(string) error     { return failIf(e.failOn, "route") }
func (e failEmitter) PythonDjangoName(string) error    { return failIf(e.failOn, "django") }
func (e failEmitter) PythonAllExportName(string) error { return failIf(e.failOn, "all") }

func failIf(failOn, stream string) error {
	if failOn == stream {
		return errStream
	}
	return nil
}

var errStream = &harvestError{}

type harvestError struct{}

func (*harvestError) Error() string { return "harvest stream failed" }

func TestHarvestErrorPropagates(t *testing.T) {
	// A source that produces at least one name in every stream, so each
	// failOn target reaches its emit and returns the error.
	src := `
__all__ = ["thing"]

@app.route("/")
@receiver(sig)
def thing(obj):
    getattr(obj, "x")
`
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	defer tree.Close()

	for _, stream := range []string{"mention", "dispatch", "decorated", "route", "django", "all"} {
		err := emitHarvest(tree.RootNode(), []byte(src), failEmitter{failOn: stream})
		if err == nil {
			t.Errorf("emitHarvest with failOn=%q returned nil, want error", stream)
		}
	}
}
