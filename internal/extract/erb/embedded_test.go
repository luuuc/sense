package erb

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

func findERBEdge(r *recorder, target string) *extract.EmittedEdge {
	for i := range r.edges {
		if r.edges[i].TargetQualified == target && r.edges[i].Kind == model.EdgeCalls {
			return &r.edges[i]
		}
	}
	return nil
}

func erbTargets(r *recorder) []string {
	out := make([]string, 0, len(r.edges))
	for _, e := range r.edges {
		out = append(out, e.TargetQualified)
	}
	return out
}

// The receiver chain and block body — invisible to the receiver-stripping
// regex — now each emit a resolved call, at the ERB line they appear on.
func TestEmbeddedRuby_ReceiverChainAndBlock(t *testing.T) {
	r := extractERB(t, "<ul>\n<% @cart.items.each do |i| %>\n<li><%= i.listing.title %></li>\n<% end %>\n</ul>", "app/views/carts/show.html.erb")

	cases := map[string]int{
		"items":   2, // @cart.items, on line 2
		"each":    2,
		"listing": 3, // i.listing.title, on line 3
		"title":   3,
	}
	for target, wantLine := range cases {
		e := findERBEdge(r, target)
		if e == nil {
			t.Errorf("expected chain/block call %q to emit an edge; got %v", target, erbTargets(r))
			continue
		}
		if e.SourceQualified != "app/views/carts/show.html.erb" {
			t.Errorf("%q edge source = %q, want the ERB file path", target, e.SourceQualified)
		}
		if e.Line == nil || *e.Line != wantLine {
			t.Errorf("%q edge line = %v, want %d", target, e.Line, wantLine)
		}
	}
}

// A receiver-position helper (`current_user`) is emitted by the regex pass
// (the walker can't see it), while the method on it (`email`) is emitted by
// the walker. Both survive.
func TestEmbeddedRuby_ReceiverPositionHelperKept(t *testing.T) {
	r := extractERB(t, `<%= current_user.email %>`, "v.erb")
	if findERBEdge(r, "self.current_user") == nil {
		t.Errorf("expected self.current_user (receiver-position helper), got %v", erbTargets(r))
	}
	if findERBEdge(r, "email") == nil {
		t.Errorf("expected the email call on current_user, got %v", erbTargets(r))
	}
}

// A receiverless call found by both passes collapses to a single edge, keeping
// the higher-confidence (walker, 1.0) variant.
func TestEmbeddedRuby_DedupsAcrossPasses(t *testing.T) {
	r := extractERB(t, `<%= format_money(total) %>`, "v.erb")
	var count int
	for _, e := range r.edges {
		if e.TargetQualified == "self.format_money" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("self.format_money emitted %d times, want 1 (deduped); edges=%v", count, erbTargets(r))
	}
	e := findERBEdge(r, "self.format_money")
	if e.Confidence != extract.ConfidenceConvention {
		t.Errorf("deduped edge confidence = %v, want the helper pass's %v (it runs first and claims the call)",
			e.Confidence, extract.ConfidenceConvention)
	}
}

// A split `<% if %> … <% end %>` conditional must not abort the scan, and the
// predicate is still captured (by the regex pass, since the walker cannot
// recover a dangling `if signed_in?` standalone).
func TestEmbeddedRuby_SplitIfEndCaptured(t *testing.T) {
	r := extractERB(t, "<% if signed_in? %>\n<p>hi</p>\n<% end %>", "v.erb")
	if findERBEdge(r, "self.signed_in?") == nil {
		t.Errorf("expected self.signed_in? from the split conditional, got %v", erbTargets(r))
	}
}

// DSL helpers with a dedicated pass (render) do not also emit a bare
// self.<helper> edge from the embedded walker.
func TestEmbeddedRuby_DedicatedHelpersFiltered(t *testing.T) {
	r := extractERB(t, `<%= render "shared/header" %>`, "app/views/pages/home.html.erb")
	if findERBEdge(r, "self.render") != nil {
		t.Errorf("render should not emit a bare self.render edge; got %v", erbTargets(r))
	}
	if findERBEdge(r, extract.PrefixPartial+"shared/header") == nil {
		t.Errorf("expected the dedicated partial edge, got %v", erbTargets(r))
	}
}

// A dangling `<% end %>` from a split block must not leak a `self.end` keyword
// edge — the fragment walker mis-parses the bare keyword as a call, and the
// dedup/skip layer drops it.
func TestEmbeddedRuby_KeywordNotEmitted(t *testing.T) {
	r := extractERB(t, "<% if signed_in? %>\n<p>hi</p>\n<% end %>", "v.erb")
	for _, kw := range []string{"self.end", "self.if"} {
		if findERBEdge(r, kw) != nil {
			t.Errorf("keyword edge %q must not be emitted, got %v", kw, erbTargets(r))
		}
	}
}

func TestEmbeddedRubyCode(t *testing.T) {
	cases := map[string]string{
		"= current_user": "current_user", // <%= output marker
		" foo.bar ":      "foo.bar",      // <% plain
		"- trimmed":      "trimmed",      // <%- whitespace-trim marker
		"= -1":           "-1",           // unary minus after the output marker survives
		" -1":            "-1",           // <% -1 %>: leading space means '-' is unary minus, not a marker
		"":               "",
		"   ":            "",
		"current_user":   "current_user",
	}
	for in, want := range cases {
		if got := embeddedRubyCode(in); got != want {
			t.Errorf("embeddedRubyCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmbeddedRuby_EmptyTagNoParse(t *testing.T) {
	// `<% %>` has no Ruby to parse — extractEmbeddedRuby short-circuits on the
	// empty code, emitting nothing.
	r := extractERB(t, `<% %>`, "v.erb")
	if len(r.edges) != 0 {
		t.Errorf("empty tag should emit no edges, got %v", erbTargets(r))
	}
}

func TestDedupEmitter(t *testing.T) {
	ln := 7
	mk := func(target string) extract.EmittedEdge {
		l := ln
		return extract.EmittedEdge{SourceQualified: "v.erb", TargetQualified: target, Kind: model.EdgeCalls, Line: &l, Confidence: 0.9}
	}

	r := &recorder{}
	d := dedupEmitter{inner: r, seen: map[string]bool{}}

	// First occurrence emits; the exact-same call at the same line is dropped.
	if err := d.Edge(mk("self.foo")); err != nil {
		t.Fatal(err)
	}
	if err := d.Edge(mk("self.foo")); err != nil {
		t.Fatal(err)
	}
	// A dedicated-helper self-call is dropped entirely.
	if err := d.Edge(mk("self.render")); err != nil {
		t.Fatal(err)
	}
	// A symbol passes straight through.
	if err := d.Symbol(extract.EmittedSymbol{Qualified: "partial:x"}); err != nil {
		t.Fatal(err)
	}

	if len(r.edges) != 1 || r.edges[0].TargetQualified != "self.foo" {
		t.Errorf("edges = %v, want exactly [self.foo]", erbTargets(r))
	}
	if len(r.symbols) != 1 || r.symbols[0].Qualified != "partial:x" {
		t.Errorf("symbols = %v, want [partial:x]", r.symbols)
	}
}

func TestEmbeddedRuby_CommentTagSkipped(t *testing.T) {
	// `<%# ... %>` is a comment — extractTemplateRuby skips it before the
	// embedded pass, so no edge is emitted for its contents.
	r := extractERB(t, `<%# render "x"; current_user %>`, "v.erb")
	if len(r.edges) != 0 {
		t.Errorf("comment tag should emit no edges, got %v", erbTargets(r))
	}
}
