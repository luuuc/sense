package ruby

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// hasEmbeddedEdge reports whether a calls edge with the given source + target
// was emitted, for terse presence assertions.
func hasEmbeddedEdge(r *recorder, source, target string) bool {
	return findEdge(r, source, target, string(model.EdgeCalls)) != nil
}

func TestExtractEmbeddedCalls_BareReceiverlessCall(t *testing.T) {
	r := &recorder{}
	// `<%= current_user %>` — a bare helper, no receiver.
	if err := ExtractEmbeddedCalls([]byte(`current_user`), 0, "app/views/orders/show.html.erb", r); err != nil {
		t.Fatalf("ExtractEmbeddedCalls: %v", err)
	}
	if !hasEmbeddedEdge(r, "app/views/orders/show.html.erb", "self.current_user") {
		t.Fatalf("expected self.current_user edge, got %+v", r.edges)
	}
}

func TestExtractEmbeddedCalls_ReceiverChainAndBlock(t *testing.T) {
	r := &recorder{}
	// `<% @cart.items.each do |i| i.listing.title end %>` — the chained calls
	// and the block body that the receiver-stripping regex could never see.
	frag := `@cart.items.each do |i| i.listing.title end`
	if err := ExtractEmbeddedCalls([]byte(frag), 0, "v.erb", r); err != nil {
		t.Fatalf("ExtractEmbeddedCalls: %v", err)
	}
	// The chain resolves to its trailing method names — not a single bare token,
	// and not nothing (the regex baseline).
	for _, want := range []string{"items", "each", "listing", "title"} {
		if !hasEmbeddedEdge(r, "v.erb", want) {
			t.Errorf("expected chain/block call %q to emit an edge; got %+v", want, edgeTargets(r))
		}
	}
}

func TestExtractEmbeddedCalls_ConstantReceiver(t *testing.T) {
	r := &recorder{}
	// `<%= Money.format(total) %>` — a constant receiver resolves exactly.
	if err := ExtractEmbeddedCalls([]byte(`Money.format(total)`), 0, "v.erb", r); err != nil {
		t.Fatalf("ExtractEmbeddedCalls: %v", err)
	}
	if !hasEmbeddedEdge(r, "v.erb", "Money.format") {
		t.Fatalf("expected Money.format edge, got %+v", edgeTargets(r))
	}
}

func TestExtractEmbeddedCalls_LineOffset(t *testing.T) {
	r := &recorder{}
	// A fragment whose content sits on ERB line 12: pass offset 11 so the
	// fragment's line-1 call reports line 12.
	if err := ExtractEmbeddedCalls([]byte(`current_user`), 11, "v.erb", r); err != nil {
		t.Fatalf("ExtractEmbeddedCalls: %v", err)
	}
	e := findEdge(r, "v.erb", "self.current_user", string(model.EdgeCalls))
	if e == nil {
		t.Fatalf("expected self.current_user edge, got %+v", edgeTargets(r))
	}
	if e.Line == nil || *e.Line != 12 {
		t.Fatalf("expected edge line 12 (offset 11 + fragment line 1), got %v", e.Line)
	}
}

func TestExtractEmbeddedCalls_SplitFragmentsDoNotAbort(t *testing.T) {
	// A `<% if … %> … <% end %>` conditional is split across tags. Each
	// fragment alone is not standalone-valid Ruby; parsing must never error.
	for _, frag := range []string{`if signed_in?`, `end`, `else`} {
		r := &recorder{}
		if err := ExtractEmbeddedCalls([]byte(frag), 0, "v.erb", r); err != nil {
			t.Errorf("split fragment %q must not error, got: %v", frag, err)
		}
	}

	// A dangling block opener — `<% @items.each do |item| %>` with the `end` in
	// a later tag — recovers cleanly (only the `end` is MISSING), so emission
	// surfaces what it can: the chained `items` and `each` calls.
	r := &recorder{}
	if err := ExtractEmbeddedCalls([]byte(`@items.each do |item|`), 0, "v.erb", r); err != nil {
		t.Fatalf("dangling block opener must not error, got: %v", err)
	}
	if !hasEmbeddedEdge(r, "v.erb", "each") {
		t.Errorf("expected the `each` call from the dangling block opener, got %+v", edgeTargets(r))
	}
}

func TestExtractEmbeddedCalls_EmptyFragmentSkipped(t *testing.T) {
	for _, frag := range []string{"", "   ", "\n\t "} {
		r := &recorder{}
		if err := ExtractEmbeddedCalls([]byte(frag), 0, "v.erb", r); err != nil {
			t.Fatalf("empty fragment %q: %v", frag, err)
		}
		if len(r.edges) != 0 {
			t.Errorf("empty fragment %q emitted %d edges, want 0", frag, len(r.edges))
		}
	}
}

func TestExtractEmbeddedCalls_EmitterErrorPropagates(t *testing.T) {
	// A fragment with at least one call so the emitter is invoked; the failing
	// emitter then forces the walk's error path to surface.
	err := ExtractEmbeddedCalls([]byte(`Money.format(total)`), 0, "v.erb", &failAfterN{edgesLeft: 0})
	if err == nil {
		t.Fatal("expected the emitter error to propagate, got nil")
	}
}

func TestOffsetEmitter(t *testing.T) {
	r := &recorder{}
	oe := offsetEmitter{inner: r, offset: 10}

	// Symbol: both line endpoints shift.
	if err := oe.Symbol(extract.EmittedSymbol{Qualified: "s", LineStart: 1, LineEnd: 3}); err != nil {
		t.Fatalf("Symbol: %v", err)
	}
	if r.symbols[0].LineStart != 11 || r.symbols[0].LineEnd != 13 {
		t.Errorf("symbol lines = %d..%d, want 11..13", r.symbols[0].LineStart, r.symbols[0].LineEnd)
	}

	// Edge with a line: shifts.
	ln := 5
	if err := oe.Edge(extract.EmittedEdge{TargetQualified: "t", Line: &ln}); err != nil {
		t.Fatalf("Edge: %v", err)
	}
	if r.edges[0].Line == nil || *r.edges[0].Line != 15 {
		t.Errorf("edge line = %v, want 15", r.edges[0].Line)
	}
	// The shift must not mutate the caller's original *int.
	if ln != 5 {
		t.Errorf("offsetEmitter mutated caller's line pointer: %d", ln)
	}

	// Edge with no line: passes through untouched.
	if err := oe.Edge(extract.EmittedEdge{TargetQualified: "t2"}); err != nil {
		t.Fatalf("Edge nil line: %v", err)
	}
	if r.edges[1].Line != nil {
		t.Errorf("nil-line edge should stay nil, got %v", r.edges[1].Line)
	}
}

func edgeTargets(r *recorder) []string {
	out := make([]string, 0, len(r.edges))
	for _, e := range r.edges {
		out = append(out, e.TargetQualified)
	}
	return out
}
