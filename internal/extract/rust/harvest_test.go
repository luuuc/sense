package rust

import (
	"errors"
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// captureEmitter records emitted symbols plus the streamed dispatch, mention,
// export, and test-symbol names. It implements extract.Emitter, DispatchEmitter,
// MentionEmitter, and RustHarvestEmitter so a single Extract call exercises symbol
// extraction and every harvest together.
type captureEmitter struct {
	symbols      []extract.EmittedSymbol
	edges        []extract.EmittedEdge
	dispatch     []string
	mentions     []string
	exports      []string
	testSymbols  []string
	traitMethods []string
	allowDead    []string
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
func (c *captureEmitter) RustExportName(name string) error {
	c.exports = append(c.exports, name)
	return nil
}
func (c *captureEmitter) RustTestSymbol(name string) error {
	c.testSymbols = append(c.testSymbols, name)
	return nil
}
func (c *captureEmitter) RustTraitImplMethod(name string) error {
	c.traitMethods = append(c.traitMethods, name)
	return nil
}
func (c *captureEmitter) RustAllowDeadName(name string) error {
	c.allowDead = append(c.allowDead, name)
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

// harvest parses Rust source and runs the extractor into a fresh captureEmitter.
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
	if err := ex.Extract(tree, source, "test.rs", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	return c
}

func TestHarvestsMentionsTrue(t *testing.T) {
	if !(Extractor{}).HarvestsMentions() {
		t.Error("HarvestsMentions() = false, want true (Rust streams the mention set)")
	}
}

func TestMentionsCaptureUses(t *testing.T) {
	c := harvest(t, `
struct Widget;

impl Widget {
    fn run(&self) {}
}

fn use_it() {
    let w = Widget;
    w.run();
    helper();
}

fn helper() {}
`)
	// A type reference, a method-call field name, and a bare call target are all
	// mentioned; the project-global set proves them reachable.
	for _, want := range []string{"Widget", "run", "helper"} {
		if !has(c.mentions, want) {
			t.Errorf("mention %q not captured; got %v", want, c.mentions)
		}
	}
}

func TestMentionsExcludeDefinitionNames(t *testing.T) {
	// A definition that is never referenced must NOT mention its own name —
	// otherwise it could never earn `dead`.
	c := harvest(t, `
fn orphan() {}

struct Lonely;
`)
	if has(c.mentions, "orphan") {
		t.Errorf("orphan (its own definition name) must not be a mention; got %v", c.mentions)
	}
	if has(c.mentions, "Lonely") {
		t.Errorf("Lonely (its own definition name) must not be a mention; got %v", c.mentions)
	}
}

func TestMentionsRetainTraitMethodDeclaration(t *testing.T) {
	// A trait method DECLARATION's name stays in the mention set so a same-named
	// concrete impl is kept open-world even when satisfaction was not resolved.
	c := harvest(t, `
trait Greeter {
    fn greet(&self);
}
`)
	if !has(c.mentions, "greet") {
		t.Errorf("trait method declaration name 'greet' must remain a mention; got %v", c.mentions)
	}
}

func TestDispatchCapturesDowncast(t *testing.T) {
	c := harvest(t, `
use std::any::Any;

fn inspect(value: &dyn Any) {
    if let Some(w) = value.downcast_ref::<Widget>() {
        let _ = w;
    }
}

struct Widget;
`)
	if !has(c.dispatch, "Widget") {
		t.Errorf("downcast_ref::<Widget> should record Widget as a dispatch target; got %v", c.dispatch)
	}
}

func TestExportsCaptureFFIFunction(t *testing.T) {
	c := harvest(t, `
#[no_mangle]
pub extern "C" fn ffi_entry() {}

#[export_name = "renamed"]
fn exported() {}

fn ordinary() {}
`)
	for _, want := range []string{"ffi_entry", "exported"} {
		if !has(c.exports, want) {
			t.Errorf("export %q not captured; got %v", want, c.exports)
		}
	}
	if has(c.exports, "ordinary") {
		t.Errorf("ordinary function must not be an export; got %v", c.exports)
	}
}

func TestExportsCaptureUsedStatic(t *testing.T) {
	c := harvest(t, `
#[used]
static KEEP: u8 = 0;

#[no_mangle]
static EXPORTED: u8 = 1;

static PLAIN: u8 = 2;
`)
	for _, want := range []string{"KEEP", "EXPORTED"} {
		if !has(c.exports, want) {
			t.Errorf("static export %q not captured; got %v", want, c.exports)
		}
	}
	if has(c.exports, "PLAIN") {
		t.Errorf("plain static must not be an export; got %v", c.exports)
	}
}

func TestTestSymbolsCaptureTestAttribute(t *testing.T) {
	c := harvest(t, `
#[test]
fn it_works() {}

#[tokio::test]
async fn async_works() {}

fn plain() {}
`)
	for _, want := range []string{"it_works", "async_works"} {
		if !has(c.testSymbols, want) {
			t.Errorf("#[test]/#[tokio::test] function %q should be a test symbol; got %v", want, c.testSymbols)
		}
	}
	if has(c.testSymbols, "plain") {
		t.Errorf("plain function must not be a test symbol; got %v", c.testSymbols)
	}
}

func TestAllowDeadCaptured(t *testing.T) {
	// Items annotated #[allow(dead_code)] / #[allow(unused)] are intentionally
	// retained; an unannotated item is not.
	c := harvest(t, `
#[allow(dead_code)]
fn kept_fn() {}

#[allow(unused)]
struct KeptStruct;

#[allow(dead_code, clippy::all)]
const KEPT_CONST: u8 = 0;

fn plain_fn() {}
`)
	for _, want := range []string{"kept_fn", "KeptStruct", "KEPT_CONST"} {
		if !has(c.allowDead, want) {
			t.Errorf("#[allow(dead_code)] item %q should be captured; got %v", want, c.allowDead)
		}
	}
	if has(c.allowDead, "plain_fn") {
		t.Errorf("unannotated function must not be in the allow-dead set; got %v", c.allowDead)
	}
}

func TestTraitImplMethodsCaptured(t *testing.T) {
	// Methods in an `impl Trait for Type` block are trait-impl methods regardless of
	// the trait's origin; an inherent `impl Type` method is not.
	c := harvest(t, `
trait Greeter {
    fn greet(&self);
}

struct Robot;

impl Greeter for Robot {
    fn greet(&self) {}
}

impl Robot {
    fn inherent_only(&self) {}
}
`)
	if !has(c.traitMethods, "greet") {
		t.Errorf("trait-impl method 'greet' should be captured; got %v", c.traitMethods)
	}
	if has(c.traitMethods, "inherent_only") {
		t.Errorf("inherent method 'inherent_only' must not be a trait-impl method; got %v", c.traitMethods)
	}
}

func TestTestSymbolsCaptureCfgTestTypesAndStatics(t *testing.T) {
	// Every kind of definition nested under #[cfg(test)] is test-only, including
	// statics and type definitions, not just functions.
	c := harvest(t, `
#[cfg(test)]
mod tests {
    static FIXTURE_DATA: u8 = 0;

    struct Helper;

    enum Mode { A, B }
}
`)
	for _, want := range []string{"FIXTURE_DATA", "Helper", "Mode"} {
		if !has(c.testSymbols, want) {
			t.Errorf("symbol %q under #[cfg(test)] should be a test symbol; got %v", want, c.testSymbols)
		}
	}
}

func TestDispatchIgnoresNonDowncastGenerics(t *testing.T) {
	// Generic calls that are not Any downcasts contribute no dispatch names — only
	// the downcast family names a type the static graph cannot see.
	c := harvest(t, `
fn run() {
    let _ = identity::<u8>(0);
    let _: Vec<u8> = it.collect::<Vec<u8>>();
    let _ = Foo::bar::<u8>();
}
`)
	if len(c.dispatch) != 0 {
		t.Errorf("non-downcast generic calls must not record dispatch names; got %v", c.dispatch)
	}
}

// errEmitter returns the configured error from whichever harvest method matches
// failOn, exercising emitHarvest's error-propagation paths.
type errEmitter struct {
	captureEmitter
	failOn string
	err    error
}

func (e *errEmitter) DispatchName(name string) error {
	if e.failOn == "dispatch" {
		return e.err
	}
	return e.captureEmitter.DispatchName(name)
}
func (e *errEmitter) MentionName(name string) error {
	if e.failOn == "mention" {
		return e.err
	}
	return e.captureEmitter.MentionName(name)
}
func (e *errEmitter) RustExportName(name string) error {
	if e.failOn == "export" {
		return e.err
	}
	return e.captureEmitter.RustExportName(name)
}
func (e *errEmitter) RustTestSymbol(name string) error {
	if e.failOn == "test" {
		return e.err
	}
	return e.captureEmitter.RustTestSymbol(name)
}
func (e *errEmitter) RustTraitImplMethod(name string) error {
	if e.failOn == "traitimpl" {
		return e.err
	}
	return e.captureEmitter.RustTraitImplMethod(name)
}
func (e *errEmitter) RustAllowDeadName(name string) error {
	if e.failOn == "allowdead" {
		return e.err
	}
	return e.captureEmitter.RustAllowDeadName(name)
}

func TestEmitHarvestPropagatesEmitterErrors(t *testing.T) {
	src := `
#[no_mangle]
fn ffi_entry() {}

#[test]
fn it_works() {
    let _ = v.downcast_ref::<Widget>();
}

struct Widget;

trait T { fn m(&self); }
impl T for Widget { fn m(&self) {} }

#[allow(dead_code)]
fn kept() {}
`
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

	boom := errors.New("boom")
	for _, failOn := range []string{"dispatch", "mention", "export", "test", "traitimpl", "allowdead"} {
		e := &errEmitter{failOn: failOn, err: boom}
		if err := ex.Extract(tree, source, "test.rs", e); !errors.Is(err, boom) {
			t.Errorf("failOn %q: Extract error = %v, want boom", failOn, err)
		}
	}
}

func TestTestSymbolsCaptureCfgTestModule(t *testing.T) {
	c := harvest(t, `
#[cfg(test)]
mod tests {
    fn fixture() {}

    fn another_helper() {}
}

fn production() {}
`)
	for _, want := range []string{"fixture", "another_helper"} {
		if !has(c.testSymbols, want) {
			t.Errorf("symbol %q under #[cfg(test)] should be a test symbol; got %v", want, c.testSymbols)
		}
	}
	if has(c.testSymbols, "production") {
		t.Errorf("production function must not be a test symbol; got %v", c.testSymbols)
	}
}
