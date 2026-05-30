package ruby

import (
	"sort"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/grammars"
)

// captureEmitter records emitted symbols and the streamed dispatch names. It
// implements both extract.Emitter and extract.DispatchEmitter so a single
// Extract call exercises visibility extraction and reflective-dispatch capture
// together.
type captureEmitter struct {
	symbols  []extract.EmittedSymbol
	edges    []extract.EmittedEdge
	dispatch []string
}

func (c *captureEmitter) Symbol(s extract.EmittedSymbol) error {
	c.symbols = append(c.symbols, s)
	return nil
}
func (c *captureEmitter) Edge(e extract.EmittedEdge) error { c.edges = append(c.edges, e); return nil }
func (c *captureEmitter) DispatchName(name string) error {
	c.dispatch = append(c.dispatch, name)
	return nil
}

func (c *captureEmitter) visibilityOf(qualified string) (string, bool) {
	for _, s := range c.symbols {
		if s.Qualified == qualified {
			return s.Visibility, true
		}
	}
	return "", false
}

// extractRubySource parses source and runs the Ruby extractor into a fresh
// captureEmitter.
func extractRubySource(t *testing.T, source string) *captureEmitter {
	t.Helper()
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(grammars.Ruby()); err != nil {
		t.Fatalf("set language: %v", err)
	}
	tree := p.Parse([]byte(source), nil)
	if tree == nil {
		t.Fatal("nil parse tree")
	}
	defer tree.Close()

	ce := &captureEmitter{}
	if err := (Extractor{}).Extract(tree, []byte(source), "test.rb", ce); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return ce
}

func TestVisibilitySectionFlip(t *testing.T) {
	src := "class Foo\n" +
		"  def a; end\n" +
		"  private\n" +
		"  def b; end\n" +
		"  def c; end\n" +
		"end\n"
	ce := extractRubySource(t, src)

	cases := map[string]string{
		"Foo#a": "public",  // before the bare `private`
		"Foo#b": "private", // after it
		"Foo#c": "private", // still after it
	}
	for q, want := range cases {
		got, ok := ce.visibilityOf(q)
		if !ok {
			t.Errorf("%s not emitted", q)
			continue
		}
		if got != want {
			t.Errorf("%s visibility = %q, want %q", q, got, want)
		}
	}
}

func TestVisibilityProtectedSection(t *testing.T) {
	src := "class Foo\n" +
		"  protected\n" +
		"  def secret; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#secret"); got != "protected" {
		t.Errorf("Foo#secret visibility = %q, want protected", got)
	}
}

func TestVisibilityPublicResetsSection(t *testing.T) {
	src := "class Foo\n" +
		"  private\n" +
		"  def a; end\n" +
		"  public\n" +
		"  def b; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#a"); got != "private" {
		t.Errorf("Foo#a = %q, want private", got)
	}
	if got, _ := ce.visibilityOf("Foo#b"); got != "public" {
		t.Errorf("Foo#b = %q, want public (public resets the section)", got)
	}
}

func TestVisibilityRetroactiveSymbolList(t *testing.T) {
	src := "class Foo\n" +
		"  def a; end\n" +
		"  def b; end\n" +
		"  def c; end\n" +
		"  private :b, :c\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#a"); got != "public" {
		t.Errorf("Foo#a = %q, want public", got)
	}
	if got, _ := ce.visibilityOf("Foo#b"); got != "private" {
		t.Errorf("Foo#b = %q, want private (private :b, :c)", got)
	}
	if got, _ := ce.visibilityOf("Foo#c"); got != "private" {
		t.Errorf("Foo#c = %q, want private (private :b, :c)", got)
	}
}

func TestVisibilityInlineDef(t *testing.T) {
	src := "class Foo\n" +
		"  public def d; end\n" +
		"  private def e; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#d"); got != "public" {
		t.Errorf("Foo#d = %q, want public", got)
	}
	if got, _ := ce.visibilityOf("Foo#e"); got != "private" {
		t.Errorf("Foo#e = %q, want private (private def e)", got)
	}
}

func TestVisibilityStringFormList(t *testing.T) {
	// `private "a"` (string literal name) is a valid, if rare, form.
	src := "class Foo\n" +
		"  def a; end\n" +
		"  private \"a\"\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#a"); got != "private" {
		t.Errorf("Foo#a = %q, want private (private \"a\")", got)
	}
}

func TestVisibilityNonLiteralArgIgnored(t *testing.T) {
	// `private SOME_CONST` and an empty-string name must not crash or
	// mis-assign; the method keeps its default visibility.
	src := "class Foo\n" +
		"  def a; end\n" +
		"  private SOME_DYNAMIC\n" +
		"  private \"\"\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#a"); got != "public" {
		t.Errorf("Foo#a = %q, want public (non-literal visibility arg ignored)", got)
	}
}

func TestVisibilityDefaultPublic(t *testing.T) {
	src := "class Foo\n" +
		"  def a; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo#a"); got != "public" {
		t.Errorf("Foo#a = %q, want public (no modifier)", got)
	}
}

func TestVisibilitySingletonStaysPublic(t *testing.T) {
	// A `private` section does not affect singleton (def self.x) methods.
	src := "class Foo\n" +
		"  private\n" +
		"  def self.build; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Foo.build"); got != "public" {
		t.Errorf("Foo.build = %q, want public (singleton unaffected by private)", got)
	}
}

func TestVisibilityNestedClassIsolated(t *testing.T) {
	// A `private` in an inner class must not leak to the outer class's later
	// methods — each body gets its own pre-pass.
	src := "class Outer\n" +
		"  def a; end\n" +
		"  class Inner\n" +
		"    private\n" +
		"    def secret; end\n" +
		"  end\n" +
		"  def b; end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if got, _ := ce.visibilityOf("Outer#a"); got != "public" {
		t.Errorf("Outer#a = %q, want public", got)
	}
	if got, _ := ce.visibilityOf("Outer#b"); got != "public" {
		t.Errorf("Outer#b = %q, want public (inner private must not leak)", got)
	}
	if got, _ := ce.visibilityOf("Outer::Inner#secret"); got != "private" {
		t.Errorf("Outer::Inner#secret = %q, want private", got)
	}
}

func TestDispatchNamesCaptured(t *testing.T) {
	src := "class Bar\n" +
		"  def go\n" +
		"    send(:alpha)\n" +
		"    public_send(\"beta\")\n" +
		"    __send__(:gamma)\n" +
		"    define_method(:delta) { }\n" +
		"    respond_to?(:epsilon)\n" +
		"    method(:zeta)\n" +
		"    obj.const_get(\"Eta\")\n" +
		"    \"Theta\".constantize\n" +
		"    send(some_var)\n" + // non-literal: must NOT be captured
		"  end\n" +
		"end\n"
	ce := extractRubySource(t, src)

	got := append([]string(nil), ce.dispatch...)
	sort.Strings(got)
	want := []string{"Eta", "Theta", "alpha", "beta", "delta", "epsilon", "gamma", "zeta"}

	if len(got) != len(want) {
		t.Fatalf("dispatch names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("dispatch[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
}

// TestDispatchNamesNonLiteralForms exercises the negative branches of the
// argument/receiver extractors: a dispatch call with an empty argument list,
// one with no argument node at all, and a `constantize` on a non-string
// receiver. None should capture a name, and none should crash.
func TestDispatchNamesNonLiteralForms(t *testing.T) {
	src := "class Edge\n" +
		"  def go\n" +
		"    method()\n" + // first-arg dispatcher with empty args → no name
		"    obj.send\n" + // first-arg dispatcher with no arguments node → no name
		"    x.constantize\n" + // constantize on a non-string receiver → no name
		"  end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if len(ce.dispatch) != 0 {
		t.Errorf("non-literal dispatch forms should capture nothing, got %v", ce.dispatch)
	}
}

// TestVisibilityIgnoresDegenerateNodes pins the pre-pass's defensive contract:
// a receiver-form visibility call (`self.private`, which has no argument list),
// a nil body, an empty class name, and a node with no `name` field are all
// safely ignored rather than recorded or panicked on. This matters because a
// wrong record here could only ever mark a method private — and a private
// method is the one kind that can earn `dead`.
func TestVisibilityIgnoresDegenerateNodes(t *testing.T) {
	// Receiver-form `self.private` has no arguments node; it must be a no-op,
	// leaving the following method public.
	ce := extractRubySource(t, "class Foo\n  self.private\n  def a; end\nend\n")
	if got, _ := ce.visibilityOf("Foo#a"); got != "public" {
		t.Errorf("Foo#a = %q, want public (`self.private` receiver form is a no-op)", got)
	}

	// Direct-call guards on the pre-pass and the name extractor.
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(grammars.Ruby()); err != nil {
		t.Fatalf("set language: %v", err)
	}
	src := []byte("class Foo\n  def a; end\nend\n")
	tree := p.Parse(src, nil)
	defer tree.Close()

	w := &walker{source: src, methodVisibility: map[string]string{}}
	w.recordBodyVisibility(nil, "Foo") // nil body → no-op

	classNode := tree.RootNode().NamedChild(0)
	body := classNode.ChildByFieldName("body")
	w.recordBodyVisibility(body, "") // empty class name → no-op
	if len(w.methodVisibility) != 0 {
		t.Errorf("degenerate inputs should record nothing, got %v", w.methodVisibility)
	}

	// methodName on a node with no `name` field (the body) returns "".
	if got := methodName(body, src); got != "" {
		t.Errorf("methodName(body) = %q, want \"\" (no name field)", got)
	}
}

func TestDispatchNamesEmptyWhenNone(t *testing.T) {
	src := "class Plain\n" +
		"  def go\n" +
		"    regular_call\n" +
		"    obj.method_call\n" +
		"  end\n" +
		"end\n"
	ce := extractRubySource(t, src)
	if len(ce.dispatch) != 0 {
		t.Errorf("dispatch names = %v, want none", ce.dispatch)
	}
}

// TestDispatchEmitterOptional proves an Emitter that does NOT implement
// DispatchEmitter still extracts cleanly — the dispatch capture is best-effort.
func TestDispatchEmitterOptional(t *testing.T) {
	src := "class Bar\n  def go\n    send(:alpha)\n  end\nend\n"
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(grammars.Ruby()); err != nil {
		t.Fatalf("set language: %v", err)
	}
	tree := p.Parse([]byte(src), nil)
	defer tree.Close()

	// plainEmitter implements only Emitter, not DispatchEmitter.
	pe := &plainEmitter{}
	if err := (Extractor{}).Extract(tree, []byte(src), "test.rb", pe); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(pe.symbols) == 0 {
		t.Error("expected symbols to be emitted even without DispatchEmitter")
	}
}

type plainEmitter struct {
	symbols []extract.EmittedSymbol
}

func (p *plainEmitter) Symbol(s extract.EmittedSymbol) error {
	p.symbols = append(p.symbols, s)
	return nil
}
func (p *plainEmitter) Edge(extract.EmittedEdge) error { return nil }
