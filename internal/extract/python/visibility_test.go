package python

import "testing"

func TestVisibilityUnderscoreConvention(t *testing.T) {
	src := `
def public_fn():
    pass

def _private_fn():
    pass

def __mangled_fn():
    pass

PUBLIC_CONST = 1
_PRIVATE_CONST = 2

class PublicClass:
    def method(self):
        pass

    def _helper(self):
        pass

    def __init__(self):
        pass

    def __mangled(self):
        pass

class _PrivateClass:
    pass
`
	r := parse(t, src)

	cases := map[string]string{
		"public_fn":             "public",
		"_private_fn":           "private",
		"__mangled_fn":          "private",
		"PUBLIC_CONST":          "public",
		"_PRIVATE_CONST":        "private",
		"PublicClass":           "public",
		"_PrivateClass":         "private",
		"PublicClass.method":    "public",
		"PublicClass._helper":   "private",
		"PublicClass.__init__":  "public", // dunder: public protocol, not private
		"PublicClass.__mangled": "private",
	}
	for qualified, want := range cases {
		sym := findSymbol(r, qualified)
		if sym == nil {
			t.Errorf("symbol %q not emitted", qualified)
			continue
		}
		if sym.Visibility != want {
			t.Errorf("%s visibility = %q, want %q", qualified, sym.Visibility, want)
		}
	}
}

func TestIsDunder(t *testing.T) {
	cases := map[string]bool{
		"__init__":  true,
		"__name__":  true,
		"__a__":     true,
		"_private":  false,
		"__mangled": false, // no trailing __
		"plain":     false,
		"____":      false, // degenerate: no core
		"__":        false,
	}
	for name, want := range cases {
		if got := isDunder(name); got != want {
			t.Errorf("isDunder(%q) = %v, want %v", name, got, want)
		}
	}
}
