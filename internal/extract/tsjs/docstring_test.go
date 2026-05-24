package tsjs

import (
	"testing"
)

// TestDocstring_FunctionClassConst pins case (a): a JSDoc block above a
// function, a class, and a const each attaches as the exact comment
// text, markers stripped. One test, three targets — covers the wiring
// at handleFunction, emitClassWithBody, and handleVariableDeclarator.
func TestDocstring_FunctionClassConst(t *testing.T) {
	src := `/** Sum returns the sum of a and b. */
function sum(a: number, b: number) { return a + b }

/** Greeter says hello. */
class Greeter {}

/** PI is the constant. */
const PI = 3.14
`
	r := parseTS(t, src, "test.ts")
	for _, want := range []struct {
		qual, doc string
	}{
		{"sum", "Sum returns the sum of a and b."},
		{"Greeter", "Greeter says hello."},
		{"PI", "PI is the constant."},
	} {
		sym := findSym(r, want.qual)
		if sym == nil {
			t.Errorf("symbol %s missing", want.qual)
			continue
		}
		if sym.Docstring != want.doc {
			t.Errorf("%s.Docstring = %q, want %q", want.qual, sym.Docstring, want.doc)
		}
	}
}

// TestDocstring_AtParamPreserved pins case (b): @param annotations
// are stored byte-for-byte (no special parsing, no stripping of the
// @ tag — JSDoc tag semantics are out of scope; preservation is in).
func TestDocstring_AtParamPreserved(t *testing.T) {
	src := `/**
 * Transfer moves money between accounts.
 * @param from source account
 * @param amount cents
 */
function transfer(from: string, amount: number) {}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "transfer")
	if sym == nil {
		t.Fatalf("symbol transfer missing")
	}
	want := "Transfer moves money between accounts.\n@param from source account\n@param amount cents"
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_ExportDefault pins case (c): JSDoc above `export
// default function` must reach the default-exported function symbol.
// This exercises the export_statement walk-up — without it, the
// function_declaration's PrevNamedSibling is nil because the comment
// lives one level up.
func TestDocstring_ExportDefault(t *testing.T) {
	src := `/** Default doc applies to the export. */
export default function qux() {}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "qux")
	if sym == nil {
		t.Fatalf("symbol qux missing")
	}
	want := "Default doc applies to the export."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_ArrowFunction pins case (d): JSDoc above
// `const x = () => {}` must reach the const symbol. Exercises the
// lexical_declaration walk-up — the JSDoc is above the declaration,
// not the inner variable_declarator that emits the symbol.
func TestDocstring_ArrowFunction(t *testing.T) {
	src := `/** doc for arrow */
const arrow = () => 1
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "arrow")
	if sym == nil {
		t.Fatalf("symbol arrow missing")
	}
	want := "doc for arrow"
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_DecoratorThroughClass pins case (e): a JSDoc block
// above a decorated class must reach the class with the decorator-
// argument text NOT mistaken for the docstring. The conservative
// danger here is a "find the first comment-like thing inside the
// class" implementation pulling the decorator's identifier as
// docstring text — which is silent corruption.
func TestDocstring_DecoratorThroughClass(t *testing.T) {
	src := `/** Cls is the documented class. */
@Component()
class Cls {}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "Cls")
	if sym == nil {
		t.Fatalf("symbol Cls missing")
	}
	want := "Cls is the documented class."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q (must reach class through decorator, not pull decorator argument)", sym.Docstring, want)
	}
}

// TestDocstring_PlainSlashSlashIgnored pins case (f): a plain `//`
// comment above a function is NOT JSDoc and must yield empty docstring.
// This guards against an implementation that treats all comments
// uniformly.
func TestDocstring_PlainSlashSlashIgnored(t *testing.T) {
	src := `// not a JSDoc comment
function noDoc() {}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "noDoc")
	if sym == nil {
		t.Fatalf("symbol noDoc missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (`//` is not JSDoc)", sym.Docstring)
	}
}

// TestDocstring_BlankLineGap pins case (g): a blank line between a
// JSDoc block and the function detaches the comment. The next function
// (with its own JSDoc, no gap) must still attach correctly — proves
// the helper doesn't poison forward attachment.
func TestDocstring_BlankLineGap(t *testing.T) {
	src := `/** Orphan doc. */

function detached() {}

/** Attached doc. */
function attached() {}
`
	r := parseTS(t, src, "test.ts")
	det := findSym(r, "detached")
	att := findSym(r, "attached")
	if det == nil || att == nil {
		t.Fatalf("missing symbols: detached=%v attached=%v", det, att)
	}
	if det.Docstring != "" {
		t.Errorf("detached.Docstring = %q, want \"\" (blank-line gap detaches)", det.Docstring)
	}
	if att.Docstring != "Attached doc." {
		t.Errorf("attached.Docstring = %q, want \"Attached doc.\"", att.Docstring)
	}
}

// TestDocstring_PlainSlashStarIgnored pins that plain `/* … */`
// (single star) is NOT JSDoc either. JSDoc requires the double-star
// `/**`. Without this test, the strip helper might silently treat
// every block comment as documentation.
func TestDocstring_PlainSlashStarIgnored(t *testing.T) {
	src := `/* plain block, not JSDoc */
function f() {}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "f")
	if sym == nil {
		t.Fatalf("symbol f missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (single-star block is not JSDoc)", sym.Docstring)
	}
}

// TestDocstring_InterfaceTypeAliasEnum pins wiring at the remaining
// emit sites the pitch identifies (handleInterface, handleTypeAlias,
// handleEnum). Without per-site coverage a wiring miss on any one
// would silently regress users who document their type-only API.
func TestDocstring_InterfaceTypeAliasEnum(t *testing.T) {
	src := `/** Account is an account contract. */
interface Account { id: string }

/** UserID is a branded string. */
type UserID = string

/** Color enumerates the brand palette. */
enum Color { Red, Green }
`
	r := parseTS(t, src, "test.ts")
	for _, want := range []struct {
		qual, doc string
	}{
		{"Account", "Account is an account contract."},
		{"UserID", "UserID is a branded string."},
		{"Color", "Color enumerates the brand palette."},
	} {
		sym := findSym(r, want.qual)
		if sym == nil {
			t.Errorf("symbol %s missing", want.qual)
			continue
		}
		if sym.Docstring != want.doc {
			t.Errorf("%s.Docstring = %q, want %q", want.qual, sym.Docstring, want.doc)
		}
	}
}

// TestDocstring_ClassMethod pins that JSDoc on a class method (inside
// the class body) attaches. Without this test, a wiring miss on
// handleMethod would not be caught — the body-level walk is its own
// path through docstringFor.
func TestDocstring_ClassMethod(t *testing.T) {
	src := `class Service {
  /** doStuff is a method. */
  doStuff() {}
}
`
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "Service.doStuff")
	if sym == nil {
		t.Fatalf("symbol Service.doStuff missing")
	}
	want := "doStuff is a method."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_LicenseHeaderFiltered pins the Copyright/SPDX filter
// in TS/JS context. A license header directly above a class must not
// attach; the next class below must still get its own JSDoc.
func TestDocstring_LicenseHeaderFiltered(t *testing.T) {
	src := `/** Copyright 2026 Acme Inc. */
class First {}

/** Second is documented. */
class Second {}
`
	r := parseTS(t, src, "test.ts")
	first := findSym(r, "First")
	second := findSym(r, "Second")
	if first == nil || second == nil {
		t.Fatalf("missing symbols: first=%v second=%v", first, second)
	}
	if first.Docstring != "" {
		t.Errorf("First.Docstring = %q, want \"\" (license header filter)", first.Docstring)
	}
	if second.Docstring != "Second is documented." {
		t.Errorf("Second.Docstring = %q, want \"Second is documented.\"", second.Docstring)
	}
}

// TestDocstring_MalformedUTF8 pins that invalid UTF-8 bytes in a JSDoc
// comment yield empty docstring and do not panic.
func TestDocstring_MalformedUTF8(t *testing.T) {
	src := "/** junk \x80 */\nfunction bad() {}\n"
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "bad")
	if sym == nil {
		t.Fatalf("symbol bad missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on malformed-UTF-8 comment = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_AllBlankJSDoc pins that a JSDoc block whose body is
// only the wrapper markers (e.g. `/** */`) does not panic the extractor
// and yields "". The explicit recover guards the load-bearing path:
// formatJSDocComments must handle the empty-body case without indexing
// lines[firstIdx] when firstIdx is -1.
func TestDocstring_AllBlankJSDoc(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("extractor panicked on body-less JSDoc: %v", r)
		}
	}()
	src := "/** */\nfunction hush() {}\n"
	r := parseTS(t, src, "test.ts")
	sym := findSym(r, "hush")
	if sym == nil {
		t.Fatalf("symbol hush missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on body-less JSDoc = %q, want \"\"", sym.Docstring)
	}
}
