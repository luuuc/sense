package langspec

import (
	"slices"
	"testing"

	"github.com/luuuc/sense/internal/extract"
)

// harvestEmitter captures symbols plus the dead-code harvest streams (annotated
// names, mentions) so a test can assert what the langspec extractor produces.
type harvestEmitter struct {
	symbols   []extract.EmittedSymbol
	annotated []string
	mentions  []string
}

func (e *harvestEmitter) Symbol(s extract.EmittedSymbol) error {
	e.symbols = append(e.symbols, s)
	return nil
}
func (e *harvestEmitter) Edge(extract.EmittedEdge) error { return nil }
func (e *harvestEmitter) LangspecAnnotatedName(n string) error {
	e.annotated = append(e.annotated, n)
	return nil
}
func (e *harvestEmitter) MentionName(n string) error { e.mentions = append(e.mentions, n); return nil }

// runJava parses src and runs the real registered Java extractor against em.
func runReal(t *testing.T, lang, ext, src string, em extract.Emitter) {
	t.Helper()
	ex := extract.ForExtension(ext)
	if ex == nil {
		t.Fatalf("no registered extractor for %q", ext)
	}
	if ex.Language() != lang {
		t.Fatalf("extractor for %q is %q, want %q", ext, ex.Language(), lang)
	}
	tree := parse(t, ex.Grammar(), src)
	if err := ex.Extract(tree, []byte(src), "f"+ext, em); err != nil {
		t.Fatalf("Extract: %v", err)
	}
}

func TestJavaAnnotationHarvest(t *testing.T) {
	src := `@Service
public class Svc {
    @Autowired private Repo repo;
    @Test public void shouldWork() {}
    public void plain() {}
}`
	em := &harvestEmitter{}
	runReal(t, "java", ".java", src, em)
	// The annotated class and the annotated method are harvested; the plain
	// method is not. (Field annotations are not symbols, so @Autowired on a field
	// produces no harvested name.)
	if !slices.Contains(em.annotated, "Svc") {
		t.Errorf("expected annotated class Svc, got %v", em.annotated)
	}
	if !slices.Contains(em.annotated, "shouldWork") {
		t.Errorf("expected annotated method shouldWork, got %v", em.annotated)
	}
	if slices.Contains(em.annotated, "plain") {
		t.Errorf("plain (un-annotated) method must not be harvested, got %v", em.annotated)
	}
}

func TestCSharpAttributeHarvest(t *testing.T) {
	src := `[ApiController]
public class C {
    [HttpGet] public void Get() {}
    public void Plain() {}
}`
	em := &harvestEmitter{}
	runReal(t, "csharp", ".cs", src, em)
	if !slices.Contains(em.annotated, "C") || !slices.Contains(em.annotated, "Get") {
		t.Errorf("expected attributed C and Get, got %v", em.annotated)
	}
	if slices.Contains(em.annotated, "Plain") {
		t.Errorf("plain method must not be harvested, got %v", em.annotated)
	}
}

func TestKotlinAnnotationHarvest(t *testing.T) {
	src := `@Component class Bean {
    @Test fun checks() {}
    fun plain() {}
}`
	em := &harvestEmitter{}
	runReal(t, "kotlin", ".kt", src, em)
	if !slices.Contains(em.annotated, "Bean") || !slices.Contains(em.annotated, "checks") {
		t.Errorf("expected annotated Bean and checks, got %v", em.annotated)
	}
	if slices.Contains(em.annotated, "plain") {
		t.Errorf("plain method must not be harvested, got %v", em.annotated)
	}
}

func TestPHPAttributeHarvest(t *testing.T) {
	src := `<?php class Ctrl {
    #[Route("/x")] public function handle() {}
    public function plain() {}
}`
	em := &harvestEmitter{}
	runReal(t, "php", ".php", src, em)
	if !slices.Contains(em.annotated, "handle") {
		t.Errorf("expected attributed handle, got %v", em.annotated)
	}
	if slices.Contains(em.annotated, "plain") {
		t.Errorf("plain method must not be harvested, got %v", em.annotated)
	}
}

func TestJavaMentionHarvest(t *testing.T) {
	src := `public class App {
    private void caller() {
        helper();
        service.process();
    }
    private void helper() {}
}`
	em := &harvestEmitter{}
	runReal(t, "java", ".java", src, em)
	// A call target leaves a mention so a same-named symbol stays open-world.
	if !slices.Contains(em.mentions, "helper") {
		t.Errorf("expected 'helper' mentioned (it is called), got %v", em.mentions)
	}
	if !slices.Contains(em.mentions, "process") {
		t.Errorf("expected 'process' mentioned, got %v", em.mentions)
	}
	// 'caller' is only ever a definition name here, never a mention — so it can
	// fall through to the soundness gate. (App is the class name, also excluded.)
	if slices.Contains(em.mentions, "caller") {
		t.Errorf("definition name 'caller' must be excluded from mentions, got %v", em.mentions)
	}
}

func TestHarvestsMentionsPerGrammar(t *testing.T) {
	cases := map[string]bool{
		".java":  true,  // the validated benchmark language harvests
		".cs":    false, // no benchmark repo → no harvest → fails closed (core_no_harvest)
		".kt":    false,
		".scala": false,
		".php":   false,
		".c":     false,
		".cpp":   false,
	}
	for ext, want := range cases {
		ex := extract.ForExtension(ext)
		if ex == nil {
			t.Fatalf("no extractor for %q", ext)
		}
		mh, ok := ex.(extract.MentionHarvester)
		got := ok && mh.HarvestsMentions()
		if got != want {
			t.Errorf("HarvestsMentions(%s) = %v, want %v", ext, got, want)
		}
	}
}

func TestNonHarvestingGrammarEmitsNoMentions(t *testing.T) {
	// C# does not opt into mention harvest, so even with a MentionEmitter present
	// it streams nothing — its symbols fail closed at the soundness gate.
	em := &harvestEmitter{}
	runReal(t, "csharp", ".cs", `class C { void m() { other(); } }`, em)
	if len(em.mentions) != 0 {
		t.Errorf("non-harvesting C# emitted mentions: %v", em.mentions)
	}
}

// TestExtractWithoutHarvestEmitter proves the harvest streams are best-effort: an
// Emitter that implements neither MentionEmitter nor LangspecHarvestEmitter still
// extracts symbols without error (the type assertions fall through to no-ops).
func TestExtractWithoutHarvestEmitter(t *testing.T) {
	em := &testEmitter{} // implements only Symbol + Edge
	runReal(t, "java", ".java", `@Service public class S {
    private void m() { helper(); }
}`, em)
	if len(em.symbols) == 0 {
		t.Fatal("expected symbols even with a no-harvest emitter")
	}
}

// errEmitter errors on a chosen harvest stream to exercise the error-propagation
// paths in emitMentions / emitAnnotation.
type errEmitter struct {
	failMention   bool
	failAnnotated bool
}

func (errEmitter) Symbol(extract.EmittedSymbol) error { return nil }
func (errEmitter) Edge(extract.EmittedEdge) error     { return nil }
func (e errEmitter) MentionName(string) error {
	if e.failMention {
		return errMentionForced
	}
	return nil
}
func (e errEmitter) LangspecAnnotatedName(string) error {
	if e.failAnnotated {
		return errMentionForced
	}
	return nil
}

var errMentionForced = &harvestErr{}

type harvestErr struct{}

func (*harvestErr) Error() string { return "forced harvest error" }

func TestMentionEmitErrorPropagates(t *testing.T) {
	ex := extract.ForExtension(".java")
	tree := parse(t, ex.Grammar(), `class A { void m() { go(); } }`)
	if err := ex.Extract(tree, []byte(`class A { void m() { go(); } }`), "A.java", errEmitter{failMention: true}); err == nil {
		t.Error("expected error from MentionName to propagate")
	}
}

func TestAnnotationEmitErrorPropagates(t *testing.T) {
	ex := extract.ForExtension(".java")
	src := `@Service class A {}`
	tree := parse(t, ex.Grammar(), src)
	if err := ex.Extract(tree, []byte(src), "A.java", errEmitter{failAnnotated: true}); err == nil {
		t.Error("expected error from LangspecAnnotatedName to propagate")
	}
}

// TestMethodAnnotationEmitErrorPropagates covers the func-side emitAnnotation
// error branch: a class with no annotation (so its own symbol emits cleanly)
// but an annotated method, whose LangspecAnnotatedName failure must surface.
func TestMethodAnnotationEmitErrorPropagates(t *testing.T) {
	ex := extract.ForExtension(".java")
	src := `class Plain {
    @Test public void shouldRun() {}
}`
	tree := parse(t, ex.Grammar(), src)
	if err := ex.Extract(tree, []byte(src), "Plain.java", errEmitter{failAnnotated: true}); err == nil {
		t.Error("expected error from annotated method LangspecAnnotatedName to propagate")
	}
}

func TestVisibilityFlowsThroughExtract(t *testing.T) {
	// End-to-end: the emitted symbol carries the visibility the fn computes.
	em := &harvestEmitter{}
	runReal(t, "java", ".java", `public class K {
    private void hidden() {}
    public void shown() {}
}`, em)
	vis := map[string]string{}
	for _, s := range em.symbols {
		vis[s.Name] = s.Visibility
	}
	if vis["hidden"] != "private" {
		t.Errorf("hidden visibility = %q, want private", vis["hidden"])
	}
	if vis["shown"] != "public" {
		t.Errorf("shown visibility = %q, want public", vis["shown"])
	}
	if vis["K"] != "public" {
		t.Errorf("class K visibility = %q, want public", vis["K"])
	}
}
