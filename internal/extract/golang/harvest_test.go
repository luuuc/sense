package golang

import (
	"errors"
	"sort"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// captureEmitter records emitted symbols plus the streamed dispatch, mention,
// and cgo-export names. It implements extract.Emitter, DispatchEmitter,
// MentionEmitter, and CgoExportEmitter so a single Extract call exercises symbol
// extraction and every harvest together.
type captureEmitter struct {
	symbols    []extract.EmittedSymbol
	edges      []extract.EmittedEdge
	dispatch   []string
	mentions   []string
	cgoExports []string
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
func (c *captureEmitter) MentionName(name string) error {
	c.mentions = append(c.mentions, name)
	return nil
}
func (c *captureEmitter) CgoExportName(name string) error {
	c.cgoExports = append(c.cgoExports, name)
	return nil
}

func has(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// harvest parses Go source and runs the extractor into a fresh captureEmitter.
func harvest(t *testing.T, src string) *captureEmitter {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()
	c := &captureEmitter{}
	if err := ex.Extract(tree, source, "test.go", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return c
}

func TestHarvestsMentionsTrue(t *testing.T) {
	if !(Extractor{}).HarvestsMentions() {
		t.Error("HarvestsMentions() = false, want true (Go streams the mention set)")
	}
}

func TestMentionsCaptureUses(t *testing.T) {
	c := harvest(t, `package p

type T struct{}

func (t *T) Run() {}

func use() {
	var x T
	x.Run()
	helper()
}

func helper() {}
`)
	// A call target, a selector method name, and a type reference are all
	// mentions a hidden caller could leave.
	for _, want := range []string{"helper", "Run", "T"} {
		if !has(c.mentions, want) {
			t.Errorf("mention %q missing; got %v", want, c.mentions)
		}
	}
}

func TestMentionsExcludeDefinitionNames(t *testing.T) {
	// `lonely` is defined and never referenced: its only occurrence is the
	// definition's own name, which the harvest skips. So it must NOT appear in
	// the mention set — that absence is what lets the arbiter earn it `dead`.
	c := harvest(t, `package p

func lonely() {}
`)
	if has(c.mentions, "lonely") {
		t.Errorf("definition name %q leaked into mentions: %v", "lonely", c.mentions)
	}
}

func TestMentionsExcludeConstAndVarNames(t *testing.T) {
	c := harvest(t, `package p

const onlyConst = 1

var onlyVar int
`)
	for _, name := range []string{"onlyConst", "onlyVar"} {
		if has(c.mentions, name) {
			t.Errorf("declaration name %q leaked into mentions: %v", name, c.mentions)
		}
	}
}

func TestMentionsIncludeInterfaceMethodName(t *testing.T) {
	// An interface method declaration is deliberately left in the mention set so
	// a concrete implementor of the same name stays open-world even when the
	// resolver never drew the implicit satisfaction edge.
	c := harvest(t, `package p

type Runner interface {
	Execute()
}
`)
	if !has(c.mentions, "Execute") {
		t.Errorf("interface method %q missing from mentions: %v", "Execute", c.mentions)
	}
}

func TestDispatchMethodByName(t *testing.T) {
	c := harvest(t, `package p

func use(v reflectValue) {
	v.MethodByName("Run")
}
`)
	if !has(c.dispatch, "Run") {
		t.Errorf("MethodByName arg %q missing from dispatch: %v", "Run", c.dispatch)
	}
}

func TestDispatchFieldByName(t *testing.T) {
	c := harvest(t, `package p

func use(v reflectValue) {
	v.FieldByName("Name")
}
`)
	if !has(c.dispatch, "Name") {
		t.Errorf("FieldByName arg %q missing from dispatch: %v", "Name", c.dispatch)
	}
}

func TestDispatchRawStringArg(t *testing.T) {
	c := harvest(t, "package p\n\nfunc use(v reflectValue) {\n\tv.MethodByName(`Raw`)\n}\n")
	if !has(c.dispatch, "Raw") {
		t.Errorf("raw-string MethodByName arg %q missing from dispatch: %v", "Raw", c.dispatch)
	}
}

func TestDispatchStructTagFieldName(t *testing.T) {
	c := harvest(t, "package p\n\ntype T struct {\n\tName string `json:\"name\"`\n\tPlain int\n}\n")
	if !has(c.dispatch, "Name") {
		t.Errorf("tagged field %q missing from dispatch: %v", "Name", c.dispatch)
	}
	if has(c.dispatch, "Plain") {
		t.Errorf("untagged field %q must not enter dispatch: %v", "Plain", c.dispatch)
	}
}

func TestDispatchEmptyWhenNone(t *testing.T) {
	c := harvest(t, `package p

func use() {
	plain()
}

func plain() {}
`)
	if len(c.dispatch) != 0 {
		t.Errorf("dispatch should be empty with no reflection/tags; got %v", c.dispatch)
	}
}

func TestDispatchNonStringArgIgnored(t *testing.T) {
	// A non-literal first argument (a variable) names nothing statically, so it
	// must not enter the dispatch set.
	c := harvest(t, `package p

func use(v reflectValue, m string) {
	v.MethodByName(m)
	v.FieldByName()
}
`)
	if len(c.dispatch) != 0 {
		t.Errorf("non-literal/empty MethodByName args must not enter dispatch; got %v", c.dispatch)
	}
}

func TestDispatchNonSelectorCallIgnored(t *testing.T) {
	// A bare call named MethodByName (not a selector) is not a reflect call.
	c := harvest(t, `package p

func use() {
	MethodByName("Run")
}
`)
	if has(c.dispatch, "Run") {
		t.Errorf("bare MethodByName call must not enter dispatch; got %v", c.dispatch)
	}
}

// plainEmitter implements only extract.Emitter, proving the harvest is
// best-effort: extraction still succeeds when the Emitter accepts no names.
type plainEmitter struct{ symbols int }

func (p *plainEmitter) Symbol(extract.EmittedSymbol) error { p.symbols++; return nil }
func (p *plainEmitter) Edge(extract.EmittedEdge) error     { return nil }

func TestHarvestOptionalEmitter(t *testing.T) {
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte("package p\n\nfunc use(v reflectValue) { v.MethodByName(\"Run\"); helper() }\n\nfunc helper() {}\n")
	tree := p.Parse(src, nil)
	defer tree.Close()
	pe := &plainEmitter{}
	if err := ex.Extract(tree, src, "test.go", pe); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if pe.symbols == 0 {
		t.Error("expected symbols emitted even without Dispatch/Mention emitter")
	}
}

// failEmitter fails the chosen harvest stream so emit-error propagation is
// exercised on both branches.
type failEmitter struct {
	failDispatch bool
	failMention  bool
	failCgo      bool
}

var errEmit = errors.New("emit failed")

func (failEmitter) Symbol(extract.EmittedSymbol) error { return nil }
func (failEmitter) Edge(extract.EmittedEdge) error     { return nil }
func (f failEmitter) DispatchName(string) error {
	if f.failDispatch {
		return errEmit
	}
	return nil
}
func (f failEmitter) MentionName(string) error {
	if f.failMention {
		return errEmit
	}
	return nil
}
func (f failEmitter) CgoExportName(string) error {
	if f.failCgo {
		return errEmit
	}
	return nil
}

func extractInto(t *testing.T, src string, emit extract.Emitter) error {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	defer tree.Close()
	return ex.Extract(tree, source, "test.go", emit)
}

func TestDispatchEmitErrorPropagates(t *testing.T) {
	err := extractInto(t, `package p

func use(v reflectValue) { v.MethodByName("Run") }
`, failEmitter{failDispatch: true})
	if !errors.Is(err, errEmit) {
		t.Errorf("DispatchName emit error not propagated; got %v", err)
	}
}

func TestCgoEmitErrorPropagates(t *testing.T) {
	err := extractInto(t, `package p

//export GoCallback
func GoCallback() {}
`, failEmitter{failCgo: true})
	if !errors.Is(err, errEmit) {
		t.Errorf("CgoExportName emit error not propagated; got %v", err)
	}
}

func TestMentionEmitErrorPropagates(t *testing.T) {
	err := extractInto(t, `package p

func use() { helper() }

func helper() {}
`, failEmitter{failMention: true})
	if !errors.Is(err, errEmit) {
		t.Errorf("MentionName emit error not propagated; got %v", err)
	}
}

func TestCollectMentionedNamesDedup(t *testing.T) {
	// `helper` is called twice but harvested once.
	names := collectMentionedNamesFor(t, `package p

func use() {
	helper()
	helper()
}

func helper() {}
`)
	count := 0
	for _, n := range names {
		if n == "helper" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("helper appears %d times in mentions, want 1 (deduped): %v", count, names)
	}
}

func TestDispatchEmptyStringArgIgnored(t *testing.T) {
	// An empty string literal names nothing: the literal node has no content
	// child, so stringLiteralContent returns "" and nothing enters dispatch.
	c := harvest(t, `package p

func use(v reflectValue) {
	v.MethodByName("")
}
`)
	if len(c.dispatch) != 0 {
		t.Errorf("empty MethodByName arg must not enter dispatch; got %v", c.dispatch)
	}
}

func TestCgoExportCaptured(t *testing.T) {
	c := harvest(t, `package p

//export GoCallback
func GoCallback() {}

//export goHelper
func goHelper() {}
`)
	for _, want := range []string{"GoCallback", "goHelper"} {
		if !has(c.cgoExports, want) {
			t.Errorf("cgo export %q missing; got %v", want, c.cgoExports)
		}
	}
}

func TestCgoExportIgnoresNonDirectives(t *testing.T) {
	c := harvest(t, `package p

// exported is just prose, not a directive
//exporter typo, no space
//export
func helper() {}
`)
	if len(c.cgoExports) != 0 {
		t.Errorf("non-directive comments must not enter cgo exports; got %v", c.cgoExports)
	}
}

func TestCgoExportTabSeparator(t *testing.T) {
	c := harvest(t, "package p\n\n//export\tTabbed\nfunc Tabbed() {}\n")
	if !has(c.cgoExports, "Tabbed") {
		t.Errorf("tab-separated //export name missing; got %v", c.cgoExports)
	}
}

func TestStringLiteralContentNil(t *testing.T) {
	if got := stringLiteralContent(nil, nil); got != "" {
		t.Errorf("stringLiteralContent(nil) = %q, want \"\"", got)
	}
}

func TestWalkNamedNil(t *testing.T) {
	// A nil root is a no-op, not a panic.
	walkNamed(nil, func(*sitter.Node) { t.Fatal("visit called on nil tree") })
}

func TestIsGoDefinitionNameRoot(t *testing.T) {
	// The root node has no parent, so it is never a definition name.
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte("package p\n")
	tree := p.Parse(src, nil)
	defer tree.Close()
	if isGoDefinitionName(tree.RootNode()) {
		t.Error("root node should not be a definition name")
	}
}

func collectMentionedNamesFor(t *testing.T, src string) []string {
	t.Helper()
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	source := []byte(src)
	tree := p.Parse(source, nil)
	defer tree.Close()
	mentions := collectGoHarvest(tree.RootNode(), source).mentions
	out := make([]string, 0, len(mentions))
	for n := range mentions {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
