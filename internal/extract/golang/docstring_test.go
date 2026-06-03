package golang

import (
	"testing"
)

// TestDocstring_Standard pins case (a): a one-line godoc above a
// function attaches and is stored as the exact comment text, marker
// stripped. Anything weaker than exact-equal would let "found a comment
// somewhere" silently substitute for "found the right comment".
func TestDocstring_Standard(t *testing.T) {
	src := `package p

// Foo returns the answer to life.
func Foo() int { return 42 }
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Foo")
	if sym == nil {
		t.Fatalf("symbol p.Foo missing")
	}
	if got, want := sym.Docstring, "Foo returns the answer to life."; got != want {
		t.Errorf("Docstring = %q, want %q", got, want)
	}
}

// TestDocstring_MultiLine pins case (b): a run of consecutive `// ` lines
// without a blank-line gap is preserved as a single joined block. The
// internal newline is significant — a flatten-to-single-line bug would
// pass a "contains" assertion but fail exact-equal.
func TestDocstring_MultiLine(t *testing.T) {
	src := `package p

// Bar does the thing.
// It does it well.
// Across three lines.
func Bar() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Bar")
	if sym == nil {
		t.Fatalf("symbol p.Bar missing")
	}
	want := "Bar does the thing.\nIt does it well.\nAcross three lines."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_BlankLineGap pins case (c): a blank line between a
// comment and a function detaches the comment. The follow-up function
// must still get its own godoc — the helper must not "consume" the
// orphaned comment for the wrong target.
func TestDocstring_BlankLineGap(t *testing.T) {
	src := `package p

// An orphaned comment block that belongs to nothing.

func Skip() {}

// Use is documented.
func Use() {}
`
	r := parse(t, src)
	skip := findSymbol(r, "p.Skip")
	use := findSymbol(r, "p.Use")
	if skip == nil || use == nil {
		t.Fatalf("missing symbols: skip=%v use=%v", skip, use)
	}
	if skip.Docstring != "" {
		t.Errorf("Skip.Docstring = %q, want \"\" (blank-line gap should detach)", skip.Docstring)
	}
	if use.Docstring != "Use is documented." {
		t.Errorf("Use.Docstring = %q, want \"Use is documented.\"", use.Docstring)
	}
}

// TestDocstring_LicenseHeaderFiltered pins case (d): a `Copyright`-led
// comment block directly above a function is dropped (it's a license
// header, not a docstring). The next function below must still get its
// own godoc — the filter must not poison forward attachment.
func TestDocstring_LicenseHeaderFiltered(t *testing.T) {
	src := `package p

// Copyright 2026 Acme Inc. All rights reserved.
// SPDX-License-Identifier: MIT
func main() {}

// Helper does help.
func Helper() {}
`
	r := parse(t, src)
	main := findSymbol(r, "p.main")
	helper := findSymbol(r, "p.Helper")
	if main == nil || helper == nil {
		t.Fatalf("missing symbols: main=%v helper=%v", main, helper)
	}
	if main.Docstring != "" {
		t.Errorf("main.Docstring = %q, want \"\" (license header should be filtered)", main.Docstring)
	}
	if helper.Docstring != "Helper does help." {
		t.Errorf("Helper.Docstring = %q, want \"Helper does help.\"", helper.Docstring)
	}
}

// TestDocstring_MalformedUTF8 pins case (e): a comment containing
// invalid UTF-8 bytes does not panic the extractor and produces an
// empty docstring. SQLite stores TEXT as UTF-8; allowing invalid bytes
// through would corrupt the column and break downstream FTS5 readers.
func TestDocstring_MalformedUTF8(t *testing.T) {
	// Embed a lone continuation byte 0x80 (invalid UTF-8 in isolation).
	src := "package p\n\n// junk \x80\nfunc Bad() {}\n"
	r := parse(t, src)
	sym := findSymbol(r, "p.Bad")
	if sym == nil {
		t.Fatalf("symbol p.Bad missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on malformed-UTF-8 comment = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_BlockComment exercises the /* … */ comment form. Go
// programmers rarely use it for godoc, but the grammar produces the
// same `comment` node kind, so a missing branch in the marker-stripping
// helper would silently regress users who do.
func TestDocstring_BlockComment(t *testing.T) {
	src := `package p

/*
Block is documented with a slash-star block.
Second line.
*/
func Block() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Block")
	if sym == nil {
		t.Fatalf("symbol p.Block missing")
	}
	want := "Block is documented with a slash-star block.\nSecond line."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_TypeAndConstAndMethod pins that the extractor wires
// the helper into every emit site — function (covered above), type,
// const, and method. A wiring miss on any one would let users see
// docstrings on functions but mysteriously not on types or methods.
func TestDocstring_TypeAndConstAndMethod(t *testing.T) {
	src := `package p

// MaxRetries caps the retry loop.
const MaxRetries = 3

// Pi is the constant.
var Pi = 3.14

// Order represents a customer order.
type Order struct{}

// Total computes the order total.
func (o Order) Total() int { return 0 }
`
	r := parse(t, src)
	for _, want := range []struct {
		qual, doc string
	}{
		{"p.MaxRetries", "MaxRetries caps the retry loop."},
		{"p.Pi", "Pi is the constant."},
		{"p.Order", "Order represents a customer order."},
		{"p.Order.Total", "Total computes the order total."},
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

// TestDocstring_InterfaceMethod pins the sixth emit site —
// emitInterfaceMethods (golang.go:330). Interface method comments are
// siblings of the method_elem inside an interface_type body; the helper
// must walk that level too, not just top-level declarations.
func TestDocstring_InterfaceMethod(t *testing.T) {
	src := `package p

type Reader interface {
	// Read reads up to len(p) bytes.
	Read(p []byte) (int, error)
}
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Reader.Read")
	if sym == nil {
		t.Fatalf("symbol p.Reader.Read missing")
	}
	want := "Read reads up to len(p) bytes."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_LicenseFilterBranches pins each prefix recognised by
// the license filter individually. The headline LicenseHeaderFiltered
// test exercises Copyright in combination with SPDX; this test pins
// SPDX standalone so a typo or accidental delete of that filter branch
// is caught on its own.
//
// The companion sub-case proves a *non-license* docstring whose first
// word coincidentally begins with `License…` (a function named License,
// docstring starting "License returns the active license") survives the
// filter — guarding against an over-eager pattern that would silently
// drop real godoc.
func TestDocstring_LicenseFilterBranches(t *testing.T) {
	t.Run("SPDX-stripped", func(t *testing.T) {
		src := "package p\n\n// SPDX-License-Identifier: MIT\nfunc X() {}\n"
		r := parse(t, src)
		sym := findSymbol(r, "p.X")
		if sym == nil {
			t.Fatalf("p.X missing")
		}
		if sym.Docstring != "" {
			t.Errorf("Docstring = %q, want \"\" (SPDX prefix should filter)", sym.Docstring)
		}
	})

	t.Run("LicensePrefix-preserved", func(t *testing.T) {
		src := "package p\n\n// License returns the active license.\nfunc License() string { return \"\" }\n"
		r := parse(t, src)
		sym := findSymbol(r, "p.License")
		if sym == nil {
			t.Fatalf("p.License missing")
		}
		want := "License returns the active license."
		if sym.Docstring != want {
			t.Errorf("Docstring = %q, want %q (filter should NOT swallow legitimate godoc)", sym.Docstring, want)
		}
	})
}

// TestDocstring_NoComment pins the negative path: a function with no
// preceding comment must yield Docstring == "". Without this assertion
// the test suite would not catch a bug that emitted some default text.
func TestDocstring_NoComment(t *testing.T) {
	src := `package p

func Bare() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Bare")
	if sym == nil {
		t.Fatalf("symbol p.Bare missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on commentless func = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_AllBlankComments pins that a run of `//` markers with
// no body text does not panic the extractor and yields "". The explicit
// recover guards the load-bearing path: stripCommentMarkers returns an
// empty slice for body-less comments, and formatGoComments must handle
// that without indexing lines[-1] (an earlier draft did, and would
// panic here).
func TestDocstring_AllBlankComments(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("extractor panicked on body-less comments: %v", r)
		}
	}()
	src := `package p

//
//
func Hush() {}
`
	r := parse(t, src)
	sym := findSymbol(r, "p.Hush")
	if sym == nil {
		t.Fatalf("symbol p.Hush missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on body-less comments = %q, want \"\"", sym.Docstring)
	}
}
