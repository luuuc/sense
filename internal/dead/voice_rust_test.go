package dead

import "testing"

func rustSym(name, qualified, kind, visibility, file string) Symbol {
	return Symbol{
		Name:       name,
		Qualified:  qualified,
		Kind:       kind,
		Language:   "rust",
		Visibility: visibility,
		File:       file,
	}
}

func TestRustVoiceLang(t *testing.T) {
	if (rustVoice{}).Lang() != "rust" {
		t.Errorf("rustVoice.Lang() = %q, want rust", (rustVoice{}).Lang())
	}
}

func TestRustVoiceTest(t *testing.T) {
	v := rustVoice{}
	f := Facts{RustTestSymbolNames: map[string]struct{}{"it_works": {}}}
	// A test symbol is harness-invoked, never an indexed caller.
	assertReason(t, v, rustSym("it_works", "m::tests::it_works", "function", "private", "lib.rs"), f, ReasonRustTest)
	// A function not in the test set is unaffected.
	assertReason(t, v, rustSym("plain", "m::plain", "function", "private", "lib.rs"), f, "")
}

func TestRustVoiceFFI(t *testing.T) {
	v := rustVoice{}
	f := Facts{RustExportNames: map[string]struct{}{"ffi_entry": {}, "KEEP": {}}}
	// A `#[no_mangle]` function is called from C → rust_ffi.
	assertReason(t, v, rustSym("ffi_entry", "m::ffi_entry", "function", "public", "ffi.rs"), f, ReasonRustFFI)
	// A `#[used]`/`#[no_mangle]` static (KindConstant) → rust_used.
	assertReason(t, v, rustSym("KEEP", "m::KEEP", "constant", "private", "ffi.rs"), f, ReasonRustUsed)
	// A function not in the export set is unaffected.
	assertReason(t, v, rustSym("plain", "m::plain", "function", "private", "ffi.rs"), f, "")
}

func TestRustVoiceTraitImplByDeclaredName(t *testing.T) {
	v := rustVoice{}
	f := Facts{InterfaceMethodNames: map[string]struct{}{"process": {}}}
	// A method whose name is declared on an in-index trait stays open-world.
	assertReason(t, v, rustSym("process", "m::Money::process", "method", "private", "m.rs"), f, ReasonRustTraitImpl)
	// A free function of the same name is NOT a trait-impl method.
	assertReason(t, v, rustSym("process", "m::process", "function", "private", "m.rs"), f, "")
}

func TestRustVoiceStdTraitMethodNeedsImplBlockSignal(t *testing.T) {
	v := rustVoice{}
	// Hand-written std-trait methods (Display/Drop/Iterator/From …) are recognised
	// structurally via the harvested `impl Trait for` set, not a static name table.
	f := Facts{RustTraitImplMethodNames: map[string]struct{}{"drop": {}, "next": {}, "from": {}}}
	for _, name := range []string{"drop", "next", "from"} {
		assertReason(t, v, rustSym(name, "m::T::"+name, "method", "private", "m.rs"), f, ReasonRustTraitImpl)
	}
	// Without the impl-block signal, an inherent method of the same name is NOT
	// assumed to be a trait impl — it falls through (rustc would warn it if dead).
	assertReason(t, v, rustSym("deref", "m::T::deref", "method", "private", "m.rs"), Facts{}, "")
	// A non-trait private method also falls through to silent.
	assertReason(t, v, rustSym("frobnicate", "m::T::frobnicate", "method", "private", "m.rs"), Facts{}, "")
}

func TestRustVoiceTraitImplByImplBlock(t *testing.T) {
	v := rustVoice{}
	// A method defined in an `impl Trait for Type` block (harvested at scan time)
	// stays open-world even when its name is in no table and no in-index trait —
	// the sound signal for external traits like serde's Deserializer.
	f := Facts{RustTraitImplMethodNames: map[string]struct{}{"deserialize_any": {}}}
	assertReason(t, v, rustSym("deserialize_any", "m::De::deserialize_any", "method", "private", "de.rs"), f, ReasonRustTraitImpl)
	// A free function of that name is not a method, so it is unaffected.
	assertReason(t, v, rustSym("deserialize_any", "m::deserialize_any", "function", "private", "de.rs"), f, "")
}

func TestRustVoiceAllowDead(t *testing.T) {
	v := rustVoice{}
	f := Facts{RustAllowDeadNames: map[string]struct{}{"kept": {}}}
	// An #[allow(dead_code)] item is intentionally retained → never dead.
	assertReason(t, v, rustSym("kept", "m::kept", "function", "private", "m.rs"), f, ReasonRustAllowDead)
	// It wins even over the non-pub silent path that would otherwise earn dead.
	assertReason(t, v, rustSym("other", "m::other", "function", "private", "m.rs"), f, "")
}

func TestRustVoiceModule(t *testing.T) {
	v := rustVoice{}
	// rustc never lints a module as dead, so a surviving module stays possibly_dead.
	assertReason(t, v, rustSym("macros", "crate::macros", "module", "private", "lib.rs"), Facts{}, ReasonRustModule)
	assertReason(t, v, rustSym("api", "crate::api", "module", "public", "lib.rs"), Facts{}, ReasonRustModule)
}

func TestRustVoiceDerive(t *testing.T) {
	v := rustVoice{}
	// Std derivable-trait method names → rust_derive (a `#[derive(...)]` impl is
	// reached with no source caller). Proves derives do not flood the dead set.
	for _, name := range []string{"clone", "fmt", "eq", "cmp", "hash", "default", "serialize", "deserialize"} {
		assertReason(t, v, rustSym(name, "m::T::"+name, "method", "private", "m.rs"), Facts{}, ReasonRustDerive)
	}
	// An in-index trait declaration of a derivable name is labelled the more
	// precise rust_trait_impl, not rust_derive.
	f := Facts{InterfaceMethodNames: map[string]struct{}{"clone": {}}}
	assertReason(t, v, rustSym("clone", "m::T::clone", "method", "private", "m.rs"), f, ReasonRustTraitImpl)
	// A free function named like a derivable method is unaffected (only methods).
	assertReason(t, v, rustSym("clone", "m::clone", "function", "private", "m.rs"), Facts{}, "")
}

func TestRustVoicePub(t *testing.T) {
	v := rustVoice{}
	// `pub` symbol in a binary (not a library) → rust_pub, never dead.
	assertReason(t, v, rustSym("handler", "m::handler", "function", "public", "m.rs"), Facts{IsLibrary: false}, ReasonRustPub)
	assertReason(t, v, rustSym("Config", "m::Config", "class", "public", "m.rs"), Facts{IsLibrary: false}, ReasonRustPub)
	// `pub` callable/type in a LIBRARY → silent here; the core voice raises
	// core_exported_api instead (proven by the arbiter-level test below).
	assertReason(t, v, rustSym("public_api", "m::public_api", "function", "public", "lib.rs"), Facts{IsLibrary: true}, "")
	// A `pub` trait (interface) is not in isPublicAPISymbol, so the Rust voice keeps
	// it open-world (rust_pub) even in a library.
	assertReason(t, v, rustSym("Greeter", "m::Greeter", "interface", "public", "lib.rs"), Facts{IsLibrary: true}, ReasonRustPub)
}

func TestRustVoiceSilentNonPub(t *testing.T) {
	v := rustVoice{}
	// The only shapes that may fall through to dead: non-`pub` fn / method / type /
	// struct / enum / trait / const with no invisible-reach idiom. Unlike Go, a
	// private const is an ordinary candidate (no iota anchor), so it stays silent.
	assertReason(t, v, rustSym("helper", "m::helper", "function", "private", "m.rs"), Facts{}, "")
	assertReason(t, v, rustSym("compute", "m::T::compute", "method", "private", "m.rs"), Facts{}, "")
	assertReason(t, v, rustSym("Widget", "m::Widget", "class", "private", "m.rs"), Facts{}, "")
	assertReason(t, v, rustSym("Alias", "m::Alias", "type", "private", "m.rs"), Facts{}, "")
	assertReason(t, v, rustSym("Secret", "m::Secret", "interface", "private", "m.rs"), Facts{}, "")
	assertReason(t, v, rustSym("THRESHOLD", "m::THRESHOLD", "constant", "private", "m.rs"), Facts{}, "")
}

// TestRustVoiceReasonPriorityOrder pins the most-live-first ordering: when a
// symbol matches several signals, the more-likely-live reason is returned.
func TestRustVoiceReasonPriorityOrder(t *testing.T) {
	v := rustVoice{}
	// A `#[no_mangle]` pub function → rust_ffi wins over rust_pub.
	f := Facts{RustExportNames: map[string]struct{}{"ffi_entry": {}}}
	assertReason(t, v, rustSym("ffi_entry", "m::ffi_entry", "function", "public", "ffi.rs"), f, ReasonRustFFI)
	// A pub method implementing a trait → rust_trait_impl wins over rust_pub.
	f2 := Facts{InterfaceMethodNames: map[string]struct{}{"process": {}}}
	assertReason(t, v, rustSym("process", "m::T::process", "method", "public", "m.rs"), f2, ReasonRustTraitImpl)
}
