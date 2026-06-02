package python

import "strings"

// Underscore visibility is the Python analog of Ruby's method-visibility pass
// and the structural capitalization rule Go/Rust get for free. Python has no
// enforced privacy — a single leading underscore (`_helper`) is a convention and
// a double leading underscore (`__mangled`) only triggers name-mangling — but
// that convention is the only structural "not part of the public API" signal the
// language offers, so it is what the dead-code Python voice keys its narrow
// earned-`dead` candidacy on.
//
// A symbol is `private` iff its own name carries a leading underscore AND it is
// not a dunder. Everything else is `public`. The split is deliberately
// conservative in the safe direction: a falsely-public symbol never earns
// `dead` (it only loses recall), mirroring the conservatism Ruby's visibility
// pass and Go's magic-method table choose. The soundness of `dead` never rests
// on the underscore alone — the arbiter's mention gate is the backstop for a
// `_helper` imported and called from another module.

// visibilityForName returns "private" for a leading-underscore non-dunder name
// (the only shape the Python voice lets fall through to `dead`), else "public".
// It is applied to every emitted symbol — function, method, class, constant —
// by its own bare name; a private method on a public class is private, and a
// public method on a private class is public, because each tracks its own
// underscore.
func visibilityForName(name string) string {
	if isPrivateName(name) {
		return "private"
	}
	return "public"
}

// isPrivateName reports whether name is underscore-private by Python convention:
// a leading underscore that is not a dunder. `_helper` and `__mangled` are
// private; `__init__` (a dunder, runtime-invoked protocol) and `render` are not.
func isPrivateName(name string) bool {
	return strings.HasPrefix(name, "_") && !isDunder(name)
}

// isDunder reports whether name is a double-underscore "magic" name
// (`__init__`, `__call__`, `__name__`) — leading AND trailing `__` around a
// non-empty core. Such names are part of Python's public protocol surface,
// invoked by the interpreter, so they are not treated as private. The length
// guard (> 4) excludes the degenerate all-underscore forms (`____`).
func isDunder(name string) bool {
	return len(name) > 4 && strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__")
}
