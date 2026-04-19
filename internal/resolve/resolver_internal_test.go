package resolve

import "testing"

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
