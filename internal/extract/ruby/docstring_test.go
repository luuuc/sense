package ruby

import (
	"testing"
)

// TestDocstring_StackedHash pins case (a): a run of stacked `# ` lines
// above `def foo` attaches as the joined docstring with markers stripped.
// Asserting exact text catches a "found a comment, any comment" bug that
// would pass a non-empty check.
func TestDocstring_StackedHash(t *testing.T) {
	src := `# Computes the total in cents.
# Returns an integer.
def total
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "total")
	if sym == nil {
		t.Fatalf("symbol total missing")
	}
	want := "Computes the total in cents.\nReturns an integer."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_BeginEnd pins case (b): a `=begin/=end` block above a
// class declaration attaches as its body text, delimiters stripped.
// This form is rare in modern Ruby but the grammar emits the same
// `comment` node kind, so a missing branch in the marker-stripping
// helper would silently regress users who use it.
func TestDocstring_BeginEnd(t *testing.T) {
	src := `=begin
Multiline RDoc block.
Second paragraph.
=end
class Foo
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "Foo")
	if sym == nil {
		t.Fatalf("symbol Foo missing")
	}
	want := "Multiline RDoc block.\nSecond paragraph."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_BlankLineGap pins case (c): a blank line between a `#`
// comment and a `def` detaches the comment. Asserting `Docstring == ""`
// catches a bug that would walk past the gap, AND the negative case
// would fail without this fixture (no other test exercises the gap
// rule for Ruby).
func TestDocstring_BlankLineGap(t *testing.T) {
	src := `# Orphaned comment, no def follows immediately.

def bar
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "bar")
	if sym == nil {
		t.Fatalf("symbol bar missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (blank-line gap should detach)", sym.Docstring)
	}
}

// TestDocstring_MagicCommentFiltered pins case (d) from the pitch: a
// `# frozen_string_literal: true` magic comment above two consecutive
// `def`s must result in BOTH having empty docstrings.
//
// The pedagogical assertion: without the magic-comment filter, `first`
// would receive "frozen_string_literal: true" as its docstring (the
// attachment rule fires). The filter ensures it's dropped. The
// `second` assertion guards against unintended forward propagation —
// it must be empty because no comment is immediately above it (the
// preceding sibling is `first`'s def, not a comment), not because of
// any blank-line magic.
func TestDocstring_MagicCommentFiltered(t *testing.T) {
	src := `# frozen_string_literal: true
def first
end
def second
end
`
	r := parseRuby(t, src)
	first := findSymbol(r, "first")
	second := findSymbol(r, "second")
	if first == nil || second == nil {
		t.Fatalf("missing symbols: first=%v second=%v", first, second)
	}
	if first.Docstring != "" {
		t.Errorf("first.Docstring = %q, want \"\" (magic comment must be filtered)", first.Docstring)
	}
	if second.Docstring != "" {
		t.Errorf("second.Docstring = %q, want \"\" (no comment above; def is the previous sibling)", second.Docstring)
	}
}

// TestDocstring_EncodingMagicComment pins the second magic-comment
// branch — `# encoding: utf-8` is the pre-Ruby-2.0 file-top form and
// still appears widely. Without this fixture the only-frozen-string-
// literal case above would let an `encoding:` or `coding:` directive
// silently bleed into a class docstring.
func TestDocstring_EncodingMagicComment(t *testing.T) {
	src := `# encoding: utf-8
class Doc
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "Doc")
	if sym == nil {
		t.Fatalf("symbol Doc missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring = %q, want \"\" (encoding magic comment must be filtered)", sym.Docstring)
	}
}

// TestDocstring_ClassAndConstantAndScope pins wiring at the remaining
// emit sites the pitch names by line number: class (handleClassOrModule),
// constant (handleConstantAssignment), and the Rails `scope` macro
// (emitScopeEdge). Without per-site coverage a wiring miss on any one
// would let users see method docstrings but mysteriously not class or
// constant docstrings.
func TestDocstring_ClassAndConstantAndScope(t *testing.T) {
	src := `# Order is a value object.
class Order
  # MAX_ITEMS caps cart size.
  MAX_ITEMS = 100

  # scope: only paid orders
  scope :paid, -> { where(paid: true) }
end
`
	r := parseRuby(t, src)
	for _, want := range []struct {
		qual, doc string
	}{
		{"Order", "Order is a value object."},
		{"Order::MAX_ITEMS", "MAX_ITEMS caps cart size."},
		{"Order.paid", "scope: only paid orders"},
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

// TestDocstring_RailsCallback pins the emitCallbackEdges emit site —
// the synthetic symbol for `before_save :compute_total` should carry
// the comment that documents that callback.
func TestDocstring_RailsCallback(t *testing.T) {
	src := `class Order < ApplicationRecord
  # runs before every save
  before_save :compute_total
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "Order.before_save")
	if sym == nil {
		t.Fatalf("symbol Order.before_save missing")
	}
	want := "runs before every save"
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q", sym.Docstring, want)
	}
}

// TestDocstring_MultipleMethodsInClass pins the body_statement walk-up
// path against the direct-sibling path in a single fixture. The first
// method's comment lives one level up (sibling of body_statement); the
// second method's comment is a direct sibling inside body_statement.
// A bug in either branch fails this test.
func TestDocstring_MultipleMethodsInClass(t *testing.T) {
	src := `class Box
  # doc for first
  def first
  end

  # doc for second
  def second
  end
end
`
	r := parseRuby(t, src)
	first := findSymbol(r, "Box#first")
	second := findSymbol(r, "Box#second")
	if first == nil || second == nil {
		t.Fatalf("missing symbols: first=%v second=%v", first, second)
	}
	if first.Docstring != "doc for first" {
		t.Errorf("first.Docstring = %q, want \"doc for first\" (walk-up through body_statement)", first.Docstring)
	}
	if second.Docstring != "doc for second" {
		t.Errorf("second.Docstring = %q, want \"doc for second\" (direct sibling)", second.Docstring)
	}
}

// TestDocstring_NoComment pins the negative path: a method with no
// preceding comment yields Docstring == "". Without this guard a bug
// that emitted some default text would slip through.
func TestDocstring_NoComment(t *testing.T) {
	src := `def bare
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "bare")
	if sym == nil {
		t.Fatalf("symbol bare missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on commentless def = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_LicenseHeaderFiltered pins that the Copyright/SPDX-
// filter applies in the Ruby context just as it does in Go — a license
// block sitting directly above a class must not be promoted to the
// class's docstring, AND the next class below must still get its own
// RDoc (proving the filter doesn't poison forward attachment).
func TestDocstring_LicenseHeaderFiltered(t *testing.T) {
	src := `# Copyright 2026 Acme Inc.
# SPDX-License-Identifier: MIT
class First
end

# Second is documented.
class Second
end
`
	r := parseRuby(t, src)
	first := findSymbol(r, "First")
	second := findSymbol(r, "Second")
	if first == nil || second == nil {
		t.Fatalf("missing symbols: first=%v second=%v", first, second)
	}
	if first.Docstring != "" {
		t.Errorf("First.Docstring = %q, want \"\" (license header should filter)", first.Docstring)
	}
	if second.Docstring != "Second is documented." {
		t.Errorf("Second.Docstring = %q, want \"Second is documented.\"", second.Docstring)
	}
}

// TestDocstring_MalformedUTF8 pins that a comment containing invalid
// UTF-8 bytes does not panic the extractor and produces an empty
// docstring. SQLite stores TEXT as UTF-8; allowing invalid bytes
// through would corrupt the column and break downstream FTS5 readers.
func TestDocstring_MalformedUTF8(t *testing.T) {
	src := "# junk \x80\ndef bad\nend\n"
	r := parseRuby(t, src)
	sym := findSymbol(r, "bad")
	if sym == nil {
		t.Fatalf("symbol bad missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on malformed-UTF-8 comment = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_AllBlankComments pins that a run of comment markers
// with no body text (e.g. `#\n#\n`) does not panic the extractor and
// yields "". The explicit recover guards the load-bearing path:
// formatRubyComments must handle the body-less case without indexing
// lines[firstIdx] when firstIdx is -1.
func TestDocstring_AllBlankComments(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("extractor panicked on body-less comments: %v", r)
		}
	}()
	src := "#\n#\ndef hush\nend\n"
	r := parseRuby(t, src)
	sym := findSymbol(r, "hush")
	if sym == nil {
		t.Fatalf("symbol hush missing")
	}
	if sym.Docstring != "" {
		t.Errorf("Docstring on body-less comments = %q, want \"\"", sym.Docstring)
	}
}

// TestDocstring_BeginEndTrailingBlank pins that a `=begin/=end` block
// with trailing blank lines before `=end` trims them rather than
// preserving them as empty docstring lines.
func TestDocstring_BeginEndTrailingBlank(t *testing.T) {
	src := `=begin
Block body.


=end
class Trim
end
`
	r := parseRuby(t, src)
	sym := findSymbol(r, "Trim")
	if sym == nil {
		t.Fatalf("symbol Trim missing")
	}
	want := "Block body."
	if sym.Docstring != want {
		t.Errorf("Docstring = %q, want %q (trailing blanks must be trimmed)", sym.Docstring, want)
	}
}
