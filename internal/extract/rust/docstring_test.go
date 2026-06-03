package rust

import (
	"testing"
)

// TestDocstring_TripleSlash pins case (a): a run of `///` lines above
// `fn foo` attaches as the exact joined text, leading-space-after-
// slashes stripped per rustdoc convention.
func TestDocstring_TripleSlash(t *testing.T) {
	src := `/// foo computes the answer.
/// Across two lines.
fn foo() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "foo")
	if sym == nil {
		t.Fatalf("symbol foo missing")
	}
	want := "foo computes the answer.\nAcross two lines."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_BlockComment pins case (b): a `/** … */` block above a
// `struct Bar` attaches as the body text with `/**`, `*/`, and the
// per-line `*` continuation prefix stripped. The grammar exposes a
// `doc_comment` child node whose body already excludes the outer
// markers; the per-line strip pass handles the inner ` * ` prefix.
func TestDocstring_BlockComment(t *testing.T) {
	src := `/**
 * Bar is a documented struct.
 * Second line.
 */
struct Bar {}
`
	r := parse(t, src)
	sym := findSymbol(r, "Bar")
	if sym == nil {
		t.Fatalf("symbol Bar missing")
	}
	want := "Bar is a documented struct.\nSecond line."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_AttributeSkipped pins case (c): a `#[derive(Debug)]`
// attribute between the `///` comment and the struct must NOT break
// attachment. The walker steps over attribute_item nodes when looking
// for the preceding doc comment. Without this skip, every derive-
// macro'd struct in the codebase would silently lose its docstring.
func TestDocstring_AttributeSkipped(t *testing.T) {
	src := `/// Skipping attributes works.
#[derive(Debug, Clone)]
struct Wrapped {}
`
	r := parse(t, src)
	sym := findSymbol(r, "Wrapped")
	if sym == nil {
		t.Fatalf("symbol Wrapped missing")
	}
	want := "Skipping attributes works."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q (attribute_item must be skipped, not block attachment)", sym.Docstring, want)
	}
}

// TestDocstring_AttributeOnlyNoDoc pins case (d): an attribute above
// an item with NO `///` comment above the attribute must yield empty
// docstring. Distinguishes "attribute filtered" from "comment found
// by accident": without this, the walker might emit attribute text.
func TestDocstring_AttributeOnlyNoDoc(t *testing.T) {
	src := `#[derive(Debug)]
struct Bare {}
`
	r := parse(t, src)
	sym := findSymbol(r, "Bare")
	if sym == nil {
		t.Fatalf("symbol Bare missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (attribute alone is not documentation)", sym.Docstring)
	}
}

// TestDocstring_BlankLineGap pins case (e): a blank line between `///`
// and the item detaches the comment. The next item below (with its own
// `///` and no gap) must still attach — proves the helper doesn't
// poison forward attachment.
func TestDocstring_BlankLineGap(t *testing.T) {
	src := `/// Orphaned line.

fn detached() {}

/// Attached doc.
fn attached() {}
`
	r := parse(t, src)
	det := findSymbol(r, "detached")
	att := findSymbol(r, "attached")
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

// TestDocstring_PlainLineCommentIgnored pins that a plain `// foo`
// comment (no third slash) is NOT a doc comment and yields empty
// docstring. The grammar distinguishes via the outer_doc_comment_marker
// child; this test pins that we check that, not just kind=="line_comment".
func TestDocstring_PlainLineCommentIgnored(t *testing.T) {
	src := `// not a doc comment
fn plain() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "plain")
	if sym == nil {
		t.Fatalf("symbol plain missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (`//` is not a doc comment)", sym.Docstring)
	}
}

// TestDocstring_ConstAndModAndTrait pins wiring at the const, module,
// and trait (via handleTypeDef) emit sites. Without per-site coverage
// a wiring miss on any one would silently regress users of those
// item kinds.
func TestDocstring_ConstAndModAndTrait(t *testing.T) {
	src := `/// MAX_RETRIES caps the loop.
const MAX_RETRIES: u32 = 3;

/// shapes module.
mod shapes {
    /// Drawable can be drawn.
    pub trait Drawable {
        /// draw renders to a canvas.
        fn draw(&self);
    }
}
`
	r := parse(t, src)
	for _, want := range []struct {
		qual, doc string
	}{
		{"MAX_RETRIES", "MAX_RETRIES caps the loop."},
		{"shapes", "shapes module."},
		{"shapes::Drawable", "Drawable can be drawn."},
		{"shapes::Drawable::draw", "draw renders to a canvas."},
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

// TestDocstring_ImplMethod pins the impl-method emit site
// (handleImpl). Without this test a wiring miss on impl-method
// docstrings would not be caught by the trait or top-level function
// tests above.
func TestDocstring_ImplMethod(t *testing.T) {
	src := `struct Point;

impl Point {
    /// origin returns the zero point.
    pub fn origin() -> Self {
        Point
    }
}
`
	r := parse(t, src)
	sym := findSymbol(r, "Point::origin")
	if sym == nil {
		t.Fatalf("symbol Point::origin missing")
	}
	want := "origin returns the zero point."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_LicenseHeaderFiltered pins the Copyright/SPDX filter
// in Rust context.
func TestDocstring_LicenseHeaderFiltered(t *testing.T) {
	src := `/// Copyright 2026 Acme Inc.
struct First {}

/// Second is documented.
struct Second {}
`
	r := parse(t, src)
	first := findSymbol(r, "First")
	second := findSymbol(r, "Second")
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

// TestDocstring_MalformedUTF8 pins that invalid UTF-8 in a doc
// comment yields empty docstring without panicking.
func TestDocstring_MalformedUTF8(t *testing.T) {
	src := "/// junk \x80\nfn bad() {}\n"
	r := parse(t, src)
	sym := findSymbol(r, "bad")
	if sym == nil {
		t.Fatalf("symbol bad missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on malformed-UTF-8 = %q, want \"\"", sym.Docstring)
	}
}

// TestHasBlankLineGap_Contract pins the post-simplification contract of
// the gap helper directly, since the inline copies in the Go/Ruby/TS
// extractors share the same shape. Two cases matter:
//
//  1. `\n \n` (whitespace-only gap) returns true — the real-input case,
//     produced by tree-sitter for blank-line-separated siblings.
//  2. `\nX\n` (stray non-newline byte between two newlines) returns true
//     too — documents the simplification: non-newline bytes are
//     transparent. The previous switch-based form would have returned
//     false here. Tree-sitter doesn't produce this shape today; pinning
//     it makes the contract change visible if a future grammar bump
//     ever does.
func TestHasBlankLineGap_Contract(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		s, e uint
		want bool
	}{
		{"whitespace-only blank gap", "a\n \nb", 1, 4, true},
		{"non-newline byte transparent", "a\nx\nb", 1, 4, true},
		{"single newline is not blank", "a\nb", 1, 2, false},
		{"no newlines is not blank", "abc", 1, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasBlankLineGap([]byte(tc.src), tc.s, tc.e); got != tc.want {
				t.Errorf("hasBlankLineGap(%q, %d, %d) = %v, want %v", tc.src, tc.s, tc.e, got, tc.want)
			}
		})
	}
}

// TestDocstring_BlankLineAfterAttribute pins that a blank line between
// the attribute_item and the item it decorates detaches any `///`
// above the attribute. Without this, a visually-separated derive macro
// would silently carry doc text from above into the struct.
func TestDocstring_BlankLineAfterAttribute(t *testing.T) {
	src := `/// orphaned doc above attribute
#[derive(Debug)]

struct Detached {}
`
	r := parse(t, src)
	sym := findSymbol(r, "Detached")
	if sym == nil {
		t.Fatalf("symbol Detached missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (blank line below attribute detaches the chain)", sym.Docstring)
	}
}
