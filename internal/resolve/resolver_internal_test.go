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

func TestIsTestPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"app/models/user.rb", false},
		{"app/services/attestation_service.rb", false}, // "test" substring, not a test file
		{"test/models/user_test.rb", true},
		{"spec/models/user_spec.rb", true},
		{"app/foo_test.go", true},
		{"src/components/Button.test.tsx", true},
		{"lib/tests/helper.rb", true},
		{"pkg/testdata/fixture.json", true},
		{"test_helper.py", true}, // test_ prefix marks a Python test file
		{"scripts/test_runner.py", true},
		{"src/WidgetTest.java", true},
		{"src/WidgetTests.cs", true},
		{"", false},
	}
	for _, c := range cases {
		if got := isTestPath(c.path); got != c.want {
			t.Errorf("isTestPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestIsSyntheticTarget(t *testing.T) {
	cases := []struct {
		target string
		want   bool
	}{
		{"partial:users/profile", true},
		{"turbo-channel:notifications", true},
		{"turbo-frame:cart", true},
		{"importmap:application", true},
		{"i18n:account.title", true},
		{"route:admin_users", true},
		{"ruby-core:Kernel#puts", true},
		{"User::Session", false},
		{"count", false},
		{"I18n.t", false},
	}
	for _, c := range cases {
		if got := isSyntheticTarget(c.target); got != c.want {
			t.Errorf("isSyntheticTarget(%q) = %v, want %v", c.target, got, c.want)
		}
	}
}

func TestFilterByTestDirection(t *testing.T) {
	prod := model.SymbolRef{ID: 1, Qualified: "App::Worker", FileID: 10}
	test := model.SymbolRef{ID: 2, Qualified: "WorkerTest", FileID: 20}
	fileIsTest := map[int64]bool{10: false, 20: true}

	t.Run("production source drops test candidates", func(t *testing.T) {
		got := filterByTestDirection([]model.SymbolRef{prod, test}, false, fileIsTest)
		if len(got) != 1 || got[0].ID != 1 {
			t.Fatalf("got %+v, want only id 1 (production)", got)
		}
	})
	t.Run("test source keeps everything", func(t *testing.T) {
		got := filterByTestDirection([]model.SymbolRef{prod, test}, true, fileIsTest)
		if len(got) != 2 {
			t.Fatalf("got %d, want 2 (test source is exempt)", len(got))
		}
	})
	t.Run("no test candidate returns input untouched", func(t *testing.T) {
		in := []model.SymbolRef{prod, {ID: 3, Qualified: "App::Other", FileID: 11}}
		got := filterByTestDirection(in, false, fileIsTest)
		if len(got) != 2 {
			t.Fatalf("got %d, want 2 (no test candidate to drop)", len(got))
		}
	})
	t.Run("unknown file test-ness is kept (fail open)", func(t *testing.T) {
		unknown := model.SymbolRef{ID: 4, Qualified: "Mystery", FileID: 99} // not in fileIsTest
		got := filterByTestDirection([]model.SymbolRef{unknown}, false, fileIsTest)
		if len(got) != 1 {
			t.Fatalf("got %d, want 1 (unknown test-ness fails open)", len(got))
		}
	})
}

func TestFilterByReceiver(t *testing.T) {
	instance := model.SymbolRef{ID: 1, Qualified: "Counter#zero", Receiver: extract.ReceiverInstance}
	singleton := model.SymbolRef{ID: 2, Qualified: "Money.zero", Receiver: extract.ReceiverSingleton}
	bare := model.SymbolRef{ID: 3, Qualified: "zero"} // no receiver (e.g. another language)

	t.Run("instance separator keeps instance and bare, drops singleton", func(t *testing.T) {
		got, contradicted := filterByReceiver([]model.SymbolRef{instance, singleton, bare}, "#")
		if len(got) != 2 || got[0].ID != 1 || got[1].ID != 3 {
			t.Fatalf("got %+v, want ids [1 3]", got)
		}
		if contradicted {
			t.Error("contradicted = true, want false (instance candidate survives)")
		}
	})
	t.Run("singleton separator keeps singleton and bare, drops instance", func(t *testing.T) {
		got, contradicted := filterByReceiver([]model.SymbolRef{instance, singleton, bare}, ".")
		if len(got) != 2 || got[0].ID != 2 || got[1].ID != 3 {
			t.Fatalf("got %+v, want ids [2 3]", got)
		}
		if contradicted {
			t.Error("contradicted = true, want false (singleton candidate survives)")
		}
	})
	t.Run("no receiver declared leaves candidates untouched", func(t *testing.T) {
		in := []model.SymbolRef{bare, {ID: 4, Qualified: "pkg.zero"}}
		got, contradicted := filterByReceiver(in, "#")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
		if contradicted {
			t.Error("contradicted = true, want false (no receiver declared)")
		}
	})
	t.Run("non-dispatch separator is a no-op", func(t *testing.T) {
		in := []model.SymbolRef{instance, singleton}
		got, contradicted := filterByReceiver(in, "::")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2", len(got))
		}
		if contradicted {
			t.Error("contradicted = true, want false (`::` carries no dispatch hint)")
		}
	})
	t.Run("empty result falls back to original set and reports contradiction", func(t *testing.T) {
		// All candidates are singletons but the call is an instance dispatch:
		// filtering would empty the set, so the original is returned as a
		// tie-break rather than dropping the edge — and the contradiction is
		// reported so the resolver demotes the kind-mismatched bind.
		in := []model.SymbolRef{singleton, {ID: 5, Qualified: "Other.zero", Receiver: extract.ReceiverSingleton}}
		got, contradicted := filterByReceiver(in, "#")
		if len(got) != 2 {
			t.Fatalf("got %d candidates, want 2 (original set)", len(got))
		}
		if !contradicted {
			t.Error("contradicted = false, want true (every candidate's kind disagreed)")
		}
	})
}
