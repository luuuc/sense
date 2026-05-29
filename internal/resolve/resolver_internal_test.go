package resolve

import (
	"testing"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
)

// TestUnqualifiedName is the direct-access white-box test for the
// separator-stripping helper. Lives in the internal package because
// the helper is not exported — black-box callers exercise it only
// via the Resolve fallback path.
func TestUnqualifiedName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"foo", "foo"},
		{"a.b", "b"},
		{"A::B::c", "c"},
		{"Greeter#greet", "greet"},
		{"A::B.c", "c"}, // rightmost separator wins when mixed
		{"app.User.email", "email"},
	}
	for _, c := range cases {
		if got := unqualifiedName(c.in); got != c.want {
			t.Errorf("unqualifiedName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestUnqualifiedNameSep(t *testing.T) {
	cases := []struct {
		in, wantName, wantSep string
	}{
		{"foo", "foo", ""},
		{"a.b", "b", "."},
		{"A::B::c", "c", "::"},
		{"Greeter#greet", "greet", "#"},
		{"A::B.c", "c", "."}, // rightmost separator wins when mixed
	}
	for _, c := range cases {
		name, sep := unqualifiedNameSep(c.in)
		if name != c.wantName || sep != c.wantSep {
			t.Errorf("unqualifiedNameSep(%q) = (%q, %q), want (%q, %q)",
				c.in, name, sep, c.wantName, c.wantSep)
		}
	}
}

func TestReceiverForSeparator(t *testing.T) {
	cases := []struct{ sep, want string }{
		{"#", extract.ReceiverInstance},
		{".", extract.ReceiverSingleton},
		{"::", ""},
		{"", ""},
	}
	for _, c := range cases {
		if got := receiverForSeparator(c.sep); got != c.want {
			t.Errorf("receiverForSeparator(%q) = %q, want %q", c.sep, got, c.want)
		}
	}
}

func TestFilterByReceiver(t *testing.T) {
	instance := model.SymbolRef{ID: 1, Qualified: "Counter#zero", Receiver: extract.ReceiverInstance}
	singleton := model.SymbolRef{ID: 2, Qualified: "Money.zero", Receiver: extract.ReceiverSingleton}
	bare := model.SymbolRef{ID: 3, Qualified: "zero"} // no receiver (e.g. another language)

	t.Run("instance separator keeps instance and bare, drops singleton", func(t *testing.T) {
		got := filterByReceiver([]model.SymbolRef{instance, singleton, bare}, "#")
		if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
			t.Fatalf("got %+v, want ids [1 3]", got)
		}
	})
	t.Run("singleton separator keeps singleton and bare, drops instance", func(t *testing.T) {
		got := filterByReceiver([]model.SymbolRef{instance, singleton, bare}, ".")
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("got %+v, want ids [2 3]", got)
		}
	})
	t.Run("no receiver declared leaves candidates untouched", func(t *testing.T) {
		in := []model.SymbolRef{bare, {ID: 4, Qualified: "pkg.zero"}}
		got := filterByReceiver(in, "#")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
	})
	t.Run("non-dispatch separator is a no-op", func(t *testing.T) {
		in := []model.SymbolRef{instance, singleton}
		got := filterByReceiver(in, "::")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
	})
	t.Run("empty result falls back to original set", func(t *testing.T) {
		// All candidates are singletons but the call is an instance dispatch:
		// filtering would empty the set, so the original is returned as a
		// tie-break rather than dropping the edge.
		in := []model.SymbolRef{singleton, {ID: 5, Qualified: "Other.zero", Receiver: extract.ReceiverSingleton}}
		got := filterByReceiver(in, "#")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2 (original set)", len(got))
		}
	})
}
