package dead

// rustVoice is the Rust language voice. Rust is an even more airtight `dead`
// candidate than Go: privacy is enforced at module granularity (`pub` vs
// private-by-default), the borrow checker forces every path to be explicit, and
// rustc's own `dead_code` lint flags exactly this class — an unused non-`pub`
// item. The voice re-expresses Rust's invisible-reach idioms (trait dispatch,
// derives, FFI, linker-kept statics, tests) as open-world reasons; like every
// voice it can only raise a hand (push → possibly_dead), never vote for `dead`.
//
// The earned-`dead` candidate is narrow and structural: a NON-`pub` fn / method /
// type / struct / enum / trait with zero incoming edges, whose name is absent
// from the Rust mention set (the arbiter's soundness gate) and which this voice
// cannot tie to a trait impl / derive / FFI export / linker `#[used]` / test.
// Every `pub` symbol stays open-world — rustc's dead_code lint never flags a
// `pub` item (it may be used by another crate Sense never indexed), so letting
// one earn `dead` would break the binding "Sense `dead` ⊆ cargo `dead_code`" gate.
type rustVoice struct{}

func (rustVoice) Lang() string { return "rust" }

// Inspect returns the most-specific (most-likely-live) reason a hidden caller
// could exist for s, or nil when s is a non-`pub` fn/method/type with no
// invisible-reach idiom — the only shape that may fall through to `dead`. Checks
// are ordered most-live-first so the returned reason carries the most useful
// hint; the arbiter independently picks the lowest-priority reason across voices.
func (rustVoice) Inspect(s Symbol, f Facts) *Reason {
	// Intentionally retained: the author annotated `#[allow(dead_code)]` /
	// `#[allow(unused)]`, so rustc suppresses the lint and the symbol is never in
	// the cargo oracle. Respect that intent — never call it dead.
	if _, ok := f.RustAllowDeadNames[s.Name]; ok {
		return reasonPtr(ReasonRustAllowDead)
	}
	// Test-only: a `#[test]` / `#[bench]` item, or one nested under a
	// `#[cfg(test)]` module, is invoked by the test harness and is not even
	// compiled by `cargo build`, so no indexed caller exists by design.
	if _, ok := f.RustTestSymbolNames[s.Name]; ok {
		return reasonPtr(ReasonRustTest)
	}
	// FFI export / linker-kept: a `#[no_mangle]` / `#[export_name]` function is
	// called from C; a `#[no_mangle]` / `#[used]` static is kept alive by the
	// linker. Either way no Rust caller edge exists. The set carries both kinds;
	// a static (KindConstant) is rust_used, a function is rust_ffi.
	if _, ok := f.RustExportNames[s.Name]; ok {
		if s.Kind == "constant" {
			return reasonPtr(ReasonRustUsed)
		}
		return reasonPtr(ReasonRustFFI)
	}
	// A method that implements a trait method is reachable through a trait object
	// or a generic bound, where the static graph shows zero direct callers. The
	// soundest signal is structural: the method is defined in an `impl Trait for
	// Type` block (RustTraitImplMethodNames, harvested at scan time) — this covers
	// every external trait (serde's Deserializer, std::io::Write, …) without
	// enumerating them. A method named on an in-index trait is the second signal.
	if s.Kind == "method" {
		if _, ok := f.RustTraitImplMethodNames[s.Name]; ok {
			return reasonPtr(ReasonRustTraitImpl)
		}
		if _, ok := f.InterfaceMethodNames[s.Name]; ok {
			return reasonPtr(ReasonRustTraitImpl)
		}
		// A `#[derive(...)]` synthesizes a trait impl with no source-level caller;
		// a method sharing a std derivable-trait method name (clone / fmt / eq /
		// cmp / hash / default / serialize / …) is reached the same invisible way.
		if rustDerivableTraitMethods[s.Name] {
			return reasonPtr(ReasonRustDerive)
		}
	}
	// A Rust `mod` is a namespace; rustc's dead_code lint flags the unused items
	// within a module, never the module itself, so a module can never be in the
	// cargo oracle — it must never earn `dead`. (A module whose every child is dead
	// is already collapsed away by Rollup; one that survives here is a namespace
	// whose contents are reached invisibly, e.g. a `macros` module.)
	if s.Kind == "module" {
		return reasonPtr(ReasonRustModule)
	}
	// `pub` items never earn `dead`: rustc's dead_code lint flags only non-`pub`
	// items, so a `pub` one may have a consumer in another crate Sense never
	// indexed. A library's public callable/type API is handled by the core voice
	// (core_exported_api); otherwise raise rust_pub. Note `pub(crate)` / `pub(in
	// path)` are "private" to the extractor (the crate is Rust's export boundary),
	// so they correctly fall through — rustc warns an unused crate-private item.
	if s.Visibility == "public" {
		if f.IsLibrary && isPublicAPISymbol(s) {
			return nil
		}
		return reasonPtr(ReasonRustPub)
	}
	// Non-`pub` from here. Unlike Go (an unused iota anchor is load-bearing), a
	// private Rust const / static is an ordinary `dead` candidate — rustc warns
	// unused ones — so it falls through with everything else.
	return nil
}

// rustDerivableTraitMethods are the method names the standard `#[derive(...)]`
// traits synthesize. A `#[derive(Clone)]` / `#[derive(Serialize)]` impl has no
// source-level caller, so a method of one of these names is reached invisibly and
// must stay open-world (rust_derive). serde's Serialize/Deserialize are included:
// `#[derive(Serialize)]` is ubiquitous in real Rust and the derive flood is the
// primary precision risk the pitch calls out. Name match over-approximates toward
// caution (recall loss at worst), the safe direction — exactly the Go magic-method
// table's bet. Checked only for methods, so a free function of the same name is
// unaffected.
var rustDerivableTraitMethods = map[string]bool{
	"clone": true, "clone_from": true, // Clone
	"fmt": true,             // Debug
	"eq":  true, "ne": true, // PartialEq
	"partial_cmp": true, "lt": true, "le": true, "gt": true, "ge": true, // PartialOrd
	"cmp":       true,                      // Ord
	"hash":      true,                      // Hash
	"default":   true,                      // Default
	"serialize": true, "deserialize": true, // serde
}

func init() {
	registerReasons(map[string]reasonSpec{
		ReasonRustAllowDead: {
			priority: 40,
			hint:     "item is annotated `#[allow(dead_code)]`/`#[allow(unused)]`; the author intentionally retained it and rustc suppresses the warning — remove the annotation first if you believe it is truly unused",
			verify:   "This item carries `#[allow(dead_code)]` (or `#[allow(unused)]`), so the author deliberately kept it and rustc does not warn it. Remove the annotation and rebuild to confirm it is genuinely unused before deleting.",
		},
		ReasonRustModule: {
			priority: 25,
			hint:     "Rust module (namespace); rustc's dead_code lint flags unused items inside a module, never the module itself — inspect its contents rather than removing the `mod`",
			verify:   "This is a Rust module. rustc never reports a module as dead (only the unused items within it), and its contents may be reached invisibly (e.g. macros). Inspect the items inside before removing the `mod`.",
		},
		ReasonRustTest: {
			priority: 20,
			hint:     "Rust test item (`#[test]`/`#[bench]`, or under a `#[cfg(test)]` module); run by the test harness and not compiled by `cargo build` — never remove it as dead",
			verify:   "This symbol is test-only: invoked by the test harness, and `cargo build` does not compile it. Remove it only if you are removing the test it belongs to.",
		},
		ReasonRustFFI: {
			priority: 30,
			hint:     "function is exported across the FFI boundary (`#[no_mangle]`/`#[export_name]`) and called from C; no Rust caller exists — remove only if the C side no longer uses it",
			verify:   "This function is exported to C (`#[no_mangle]` / `#[export_name]`) and has no Rust caller by design. Check the C / FFI code that calls it before removing.",
		},
		ReasonRustUsed: {
			priority: 30,
			hint:     "static is kept alive by the linker (`#[used]`/`#[no_mangle]`); it has no Rust reader by design — do not remove it",
			verify:   "This static is retained by the linker (`#[used]` / `#[no_mangle]`), often for a linker section or the C ABI. It has no Rust reader by design; confirm the consumer before removing.",
		},
		ReasonRustTraitImpl: {
			priority: 30,
			hint:     "method implements a trait method (named on an in-index trait, or a std trait like Display/Drop/Iterator/From); it may be called through a trait object or generic bound — confirm no implementor is used before removing",
			verify:   "This method shares a name with a trait method, so it may be invoked through a trait object or a generic bound (where Sense sees no direct caller). Check whether the type is used behind its trait anywhere before removing.",
		},
		ReasonRustDerive: {
			priority: 35,
			hint:     "method shares a name with a std derivable-trait method (`#[derive(Clone)]`/`Debug`/`PartialEq`/`Serialize`/…); a derived impl is reached with no source caller — confirm the type is unused before removing",
			verify:   "This method shares a name with a derivable trait's method (Clone/Debug/PartialEq/Hash/Default/serde …). A `#[derive(...)]` impl is reached by code generated at compile time, not a source caller. Confirm the type is genuinely unused before removing.",
		},
		ReasonRustPub: {
			priority: 50,
			hint:     "`pub` Rust item with no caller in this crate; another crate may use it (rustc flags only non-`pub` items) — search dependents before removing",
			verify:   "`pub` Rust items can be used by other crates. For each, search dependent crates and the rest of this tree for the path before removing.",
		},
	})
}
