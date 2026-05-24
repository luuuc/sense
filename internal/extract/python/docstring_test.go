package python

import (
	"testing"
)

// TestDocstring_TripleQuotedMultiLine pins case (a): a multi-line
// triple-quoted docstring as the first statement of a function body
// attaches as the exact body text. Trim-leading/trailing whitespace
// normalisation is applied (artefacts of the `"""…"""` wrapper); the
// internal blank line is preserved.
func TestDocstring_TripleQuotedMultiLine(t *testing.T) {
	src := `def foo():
    """foo does the thing.

    Across multiple lines.
    """
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "foo")
	if sym == nil {
		t.Fatalf("symbol foo missing")
	}
	want := "foo does the thing.\n\n    Across multiple lines."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_SingleLineTriple pins case (b): a one-line
// `"""…"""` docstring stores the exact text between the quotes.
func TestDocstring_SingleLineTriple(t *testing.T) {
	src := `def bar():
    """bar is brief."""
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "bar")
	if sym == nil {
		t.Fatalf("symbol bar missing")
	}
	want := "bar is brief."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_PassOnlyBody pins case (c): a function whose body is
// only `pass` (no docstring) yields Docstring == "". Without this
// fixture an implementation that read any first-statement bytes
// (rather than checking the statement kind) would smuggle the
// `pass_statement` text into the column.
func TestDocstring_PassOnlyBody(t *testing.T) {
	src := `def empty():
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "empty")
	if sym == nil {
		t.Fatalf("symbol empty missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (pass-only body has no docstring)", sym.Docstring)
	}
}

// TestDocstring_DecoratorClass pins case (d): a `@dataclass`-decorated
// class's docstring is the body's first string literal, NOT the
// decorator's argument. The pedagogical danger is an implementation
// that walks PRECEDING comments or inspects the decorator's call
// arguments — both would silently store `dataclass` (or similar) in
// the column instead of the real docstring.
func TestDocstring_DecoratorClass(t *testing.T) {
	src := `@dataclass
class Decorated:
    """decorated body doc"""
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "Decorated")
	if sym == nil {
		t.Fatalf("symbol Decorated missing")
	}
	want := "decorated body doc"
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q (must be body string, not decorator argument)", sym.Docstring, want)
	}
}

// TestDocstring_ModuleLevelSkipped pins case (e): a module-level
// docstring (the first string literal of the file) is NOT promoted
// to any symbol — there is no owning symbol for it. The test asserts
// that the only emitted class's Docstring is its OWN body string,
// untouched by the module-level docstring above it.
func TestDocstring_ModuleLevelSkipped(t *testing.T) {
	src := `"""Module-level docstring that has nowhere to attach."""

class Owner:
    """class doc"""
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "Owner")
	if sym == nil {
		t.Fatalf("symbol Owner missing")
	}
	want := "class doc"
	if sym.Docstring != want {
		t.Errorf("Owner.Docstring = %q, want %q (module-level must not leak)", sym.Docstring, want)
	}
}

// TestDocstring_ClassAndMethod pins that wiring fires at BOTH emit
// sites in this card (handleClass + emitFunctionAndWalkBody for
// methods). A wiring miss on either would let users see one but not
// the other.
func TestDocstring_ClassAndMethod(t *testing.T) {
	src := `class Service:
    """Service is the documented class."""

    def do(self):
        """do is a method."""
        return 1
`
	r := parse(t, src)
	for _, want := range []struct {
		qual, doc string
	}{
		{"Service", "Service is the documented class."},
		{"Service.do", "do is a method."},
	} {
		sym := findSymbol(r, want.qual)
		if sym == nil {
			t.Errorf("symbol %s missing", want.qual)
			continue
		}
		if sym.Docstring != want.doc {
			t.Errorf("%s.Docstring = %q, want %q", want.qual, sym.Docstring, want.doc)
		}
	}
}

// TestDocstring_FirstStatementNonString pins the negative path: a
// function whose first statement is anything other than a string
// literal yields empty docstring. Without this the helper might
// emit text for an unrelated first expression.
func TestDocstring_FirstStatementNonString(t *testing.T) {
	src := `def noDoc():
    x = 1
    return x
`
	r := parse(t, src)
	sym := findSymbol(r, "noDoc")
	if sym == nil {
		t.Fatalf("symbol noDoc missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (first statement is assignment, not string)", sym.Docstring)
	}
}

// TestDocstring_LicenseHeaderFiltered pins that a function whose
// docstring text starts with a license-header prefix is dropped.
// Rare in Python (typically only at module scope, which we skip
// anyway) but the filter applies for parity with the other extractors.
func TestDocstring_LicenseHeaderFiltered(t *testing.T) {
	src := `def f():
    """Copyright 2026 Acme Inc."""
    pass
`
	r := parse(t, src)
	sym := findSymbol(r, "f")
	if sym == nil {
		t.Fatalf("symbol f missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (Copyright prefix should filter)", sym.Docstring)
	}
}

// TestDocstring_MalformedUTF8 pins that invalid UTF-8 in the docstring
// content yields empty docstring and does not panic. Python source
// can contain non-UTF-8 bytes in literal strings; we must defend.
func TestDocstring_MalformedUTF8(t *testing.T) {
	src := "def bad():\n    \"junk \x80\"\n    pass\n"
	r := parse(t, src)
	sym := findSymbol(r, "bad")
	if sym == nil {
		t.Fatalf("symbol bad missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on malformed-UTF-8 = %q, want \"\"", sym.Docstring)
	}
}
