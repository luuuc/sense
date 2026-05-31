package mcpio

import "strings"

// verifyTooCommonThreshold is the index-occurrence count above which a name is
// considered too common to auto-verify by grep. Above it, a text search for
// the name floods the caller (a predicate like "success?" is defined and
// called all over a Rails app), so the verify hint points at the definition
// site for manual inspection instead. Estimated from the index (symbols +
// resolved edges sharing the name); see dead.Symbol.NameOccurrences. Tuned
// against maket, where the mis-attributed Checkout predicates sit well above
// it. Consumed by the unreferenced-symbols verify-recipe builder.
const verifyTooCommonThreshold = 25

// escapeForERE escapes the ERE metacharacters that occur in Ruby method
// names — `?` (predicate), `!` (bang), and `.` — so the call-scoped grep
// pattern matches the literal name. Other identifier characters are
// regex-safe.
func escapeForERE(name string) string {
	r := strings.NewReplacer(
		`.`, `\.`,
		`?`, `\?`,
		`!`, `\!`,
	)
	return r.Replace(name)
}
