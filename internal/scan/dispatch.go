package scan

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/luuuc/sense/internal/sqlite"
)

// warnMetaWrite reports a name-set meta-write failure to warn without failing
// the scan. A meta-write failure degrades dead-code recall (a stale or missing
// dispatch/mention set keeps a symbol open-world), so it is a warning, not a
// fatal error — the index itself is already written. A nil err is a no-op.
func warnMetaWrite(warn io.Writer, label string, err error) {
	if err != nil {
		_, _ = fmt.Fprintf(warn, "warn: write %s meta: %v\n", label, err)
	}
}

// dispatchNamesMetaKey is the prefix for the per-language sense_meta keys
// holding each language's reflective dispatch-target names (send/const_get/
// define_method literals, constantize receivers) as a JSON string array. The
// actual key is `dispatch_names:<lang>` (see dispatchNamesKey): a symbol whose
// name appears in its OWN language's set stays open-world (possibly_dead)
// rather than being falsely called dead. Keying by language keeps one
// language's reflection literals from muting another's symbol.
const dispatchNamesMetaKey = "dispatch_names"

// dispatchNamesKey is the per-language sense_meta key for a language's dispatch
// set (e.g. `dispatch_names:ruby`). The dead-code reader discovers languages by
// globbing `dispatch_names:*`, so a legacy union key (the bare prefix) is never
// matched and a pre-feature index reads as no-languages-harvested.
func dispatchNamesKey(lang string) string { return dispatchNamesMetaKey + ":" + lang }

// writeDispatchNames persists one language's dispatch-name set gathered during
// the walk, unioning with the existing set (see writeNameSet for the rationale).
func writeDispatchNames(ctx context.Context, idx *sqlite.Adapter, lang string, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, dispatchNamesKey(lang), collected)
}

// readDispatchNames returns one language's persisted dispatch-name set, or an
// empty set when the key is absent. A corrupt value is treated as empty rather
// than fatal — a missing reflection signal degrades to recall loss, never a
// crash.
func readDispatchNames(ctx context.Context, idx *sqlite.Adapter, lang string) (map[string]struct{}, error) {
	return readNameSet(ctx, idx, dispatchNamesKey(lang))
}

// mentionedNamesMetaKey is the sense_meta key holding the project-wide broad
// set of bare names the code mentions — every identifier/symbol token except
// definition names, as a JSON string array. The dead-code arbiter's soundness
// gate reads it: a symbol earns `dead` only when its name is absent here, i.e.
// mentioned nowhere a hidden caller could be. This makes `dead` sound even
// where the resolver could not bind every call (an inherited bare call, a
// `**splat`, a chain receiver, a `validate :sym` symbol arg all leave a
// mention, keeping the target open-world instead of falsely dead).
//
// Operational note: like dispatch_names, this set is union-only across
// incremental scans and unbounded by design — it only grows until a full
// rebuild. The safe consequence is that a name removed from the code lingers
// here until rebuild, so a method that BECOMES dead keeps reading
// `possibly_dead` (a recall loss, never a false `dead`). `dead`-tier recall
// therefore refreshes on a full rescan; the set self-heals removed names then.
const mentionedNamesMetaKey = "mentioned_names"

// mentionedNamesKey is the per-language sense_meta key for a language's mention
// set (e.g. `mentioned_names:ruby`). The dead-code soundness gate earns `dead`
// for a symbol only against the mentions harvested from its OWN language, and
// derives "this language harvested" from the presence of its key — so a
// language with no key (a pre-feature index's legacy union key does not match
// the `mentioned_names:*` glob) fails closed to possibly_dead.
func mentionedNamesKey(lang string) string { return mentionedNamesMetaKey + ":" + lang }

// writeMentionedNames persists one language's broad mention set, unioning with
// the existing set (see writeNameSet for the union rationale).
func writeMentionedNames(ctx context.Context, idx *sqlite.Adapter, lang string, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, mentionedNamesKey(lang), collected)
}

// harvestedLangsMetaKey is the sense_meta key holding the set of languages
// whose mention harvest RAN for this index, as a JSON string array. The
// dead-code soundness gate reads it: a symbol whose language is absent earns
// core_no_harvest (fail closed), so this set — not the mere presence of a
// mention key — is the explicit record that a language's harvest happened. A
// language harvests even when a scan yields zero mentions for it, so this set
// can legitimately contain a language that has no mentioned_names:<lang> key.
const harvestedLangsMetaKey = "harvested_langs"

// writeHarvestedLangs persists the set of languages whose harvest ran, unioning
// with the existing set (same self-heals-on-rebuild rationale as the name sets:
// a language whose files were all removed lingers here until a full rebuild,
// which only ever keeps a symbol open-world, never falsely `dead`).
func writeHarvestedLangs(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, harvestedLangsMetaKey, collected)
}

// cgoExportsMetaKey is the sense_meta key holding the project-wide set of Go
// function names marked with a cgo `//export` directive, as a JSON string array.
// The dead-code Go voice reads it: a function whose name appears here is called
// from C with no Go caller, so it stays open-world (go_cgo) rather than earning
// `dead`. Flat, not per-language — cgo is Go-only. A pre-feature index has no
// such key, which reads as an empty set (no cgo functions known), the safe
// direction: a real cgo export then degrades to a possible false `dead` only
// until the next full scan harvests it, never a crash.
const cgoExportsMetaKey = "cgo_exports"

// writeCgoExports persists the project-wide cgo-export set, unioning with the
// existing set (same self-heals-on-rebuild rationale as the other name sets).
func writeCgoExports(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, cgoExportsMetaKey, collected)
}

// rustExportsMetaKey is the sense_meta key holding the project-wide set of Rust
// function/static names whose reachability the edge graph cannot see: functions
// marked `#[no_mangle]` / `#[export_name]` (called across the FFI boundary) and
// statics marked `#[no_mangle]` / `#[used]` (kept alive by the linker). The Rust
// voice reads it (rust_ffi for a function, rust_used for a static) so such a
// symbol stays open-world rather than earning `dead` off its absent caller. Flat,
// not per-language — these are Rust-only attributes, like cgo is Go-only.
const rustExportsMetaKey = "rust_exports"

// writeRustExports persists the project-wide Rust export/`#[used]` set, unioning
// with the existing set (same self-heals-on-rebuild rationale as the other sets).
func writeRustExports(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, rustExportsMetaKey, collected)
}

// rustTestSymbolsMetaKey is the sense_meta key holding the project-wide set of
// Rust symbol names that are test-only: items marked `#[test]` / `#[bench]` or
// nested under a `#[cfg(test)]` module. The test harness invokes them and
// `cargo build` does not compile them, so the Rust voice keeps them open-world
// (rust_test) rather than earning `dead` off an absent caller. Flat, like the
// other Rust set.
const rustTestSymbolsMetaKey = "rust_test_symbols"

// writeRustTestSymbols persists the project-wide Rust test-symbol set, unioning
// with the existing set (same self-heals-on-rebuild rationale as the other sets).
func writeRustTestSymbols(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, rustTestSymbolsMetaKey, collected)
}

// rustTraitImplMethodsMetaKey is the sense_meta key holding the project-wide set
// of method names defined in `impl Trait for Type` blocks. The Rust voice reads
// it: such a method satisfies a trait and is reached through it, so it stays
// open-world (rust_trait_impl) rather than earning `dead` off an absent caller.
// This is the sound trait-impl signal that covers external traits (serde, std::io)
// the voice's static method-name table cannot enumerate.
const rustTraitImplMethodsMetaKey = "rust_trait_impl_methods"

// writeRustTraitImplMethods persists the project-wide Rust trait-impl method set,
// unioning with the existing set (same self-heals-on-rebuild rationale as above).
func writeRustTraitImplMethods(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, rustTraitImplMethodsMetaKey, collected)
}

// rustAllowDeadMetaKey is the sense_meta key holding the project-wide set of Rust
// item names annotated `#[allow(dead_code)]` / `#[allow(unused)]`. The Rust voice
// reads it: the author deliberately suppressed the lint, so rustc never warns the
// item and it is absent from the cargo oracle — the voice keeps it open-world
// (rust_allow_dead) rather than calling it dead.
const rustAllowDeadMetaKey = "rust_allow_dead"

// writeRustAllowDead persists the project-wide Rust `#[allow(dead_code)]` set,
// unioning with the existing set (same self-heals-on-rebuild rationale as above).
func writeRustAllowDead(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, rustAllowDeadMetaKey, collected)
}

// tsDecoratedMetaKey is the sense_meta key holding the project-wide set of TS/JS
// class and method names carrying a decorator (`@Component` / `@Injectable` /
// `@Controller` / route-method decorators). The dead-code TS voice reads it: a
// framework's DI/router reaches such a symbol with no source caller, so it stays
// open-world (ts_decorator) rather than earning `dead`. Flat, not per-language —
// decorators span the .ts/.tsx/.js family, which shares one extractor.
const tsDecoratedMetaKey = "ts_decorated"

// writeTSDecorated persists the project-wide TS decorated-name set, unioning with
// the existing set (same self-heals-on-rebuild rationale as the other name sets).
func writeTSDecorated(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, tsDecoratedMetaKey, collected)
}

// tsDefaultExportsMetaKey is the sense_meta key holding the project-wide set of
// TS/JS names bound by an `export default` form. The dead-code TS voice reads it:
// a default export is imported by path, not by name, so the voice raises the more
// specific ts_default_export reason. Flat, like the decorated set.
const tsDefaultExportsMetaKey = "ts_default_exports"

// writeTSDefaultExports persists the project-wide TS default-export set, unioning
// with the existing set (same self-heals-on-rebuild rationale as the other sets).
func writeTSDefaultExports(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, tsDefaultExportsMetaKey, collected)
}

// pythonDecoratedMetaKey is the sense_meta key holding the project-wide set of
// Python function/method/class names carrying any decorator. The dead-code
// Python voice reads it: a decorator changes the call story (an attribute access,
// an injected fixture, a CLI entry), so a decorated symbol stays open-world
// (py_decorator) rather than earning `dead`. Flat, not per-language — Python-only.
const pythonDecoratedMetaKey = "py_decorated"

// writePythonDecorated persists the project-wide Python decorated-name set,
// unioning with the existing set (same self-heals-on-rebuild rationale as above).
func writePythonDecorated(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, pythonDecoratedMetaKey, collected)
}

// pythonRoutesMetaKey is the sense_meta key holding the project-wide set of
// Python handler names carrying a route decorator (Flask `@app.route`, FastAPI
// `@app.get`/`@router.post`). The Python voice reads it (py_route) so a
// framework-dispatched handler stays open-world rather than earning `dead`.
const pythonRoutesMetaKey = "py_routes"

// writePythonRoutes persists the project-wide Python route-handler set, unioning
// with the existing set (same self-heals-on-rebuild rationale as above).
func writePythonRoutes(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, pythonRoutesMetaKey, collected)
}

// pythonDjangoMetaKey is the sense_meta key holding the project-wide set of
// Python names carrying a Django-dispatch decorator (`@receiver` signal handler,
// `@admin.register`). The Python voice reads it (py_django) so a symbol Django's
// signal/admin machinery invokes invisibly stays open-world.
const pythonDjangoMetaKey = "py_django"

// writePythonDjango persists the project-wide Python Django-dispatch set, unioning
// with the existing set (same self-heals-on-rebuild rationale as above).
func writePythonDjango(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, pythonDjangoMetaKey, collected)
}

// pythonAllExportsMetaKey is the sense_meta key holding the project-wide set of
// names Python modules declare public via `__all__`. The Python voice reads it
// (py_all_export): such a name is re-exported by `from mod import *`, so it stays
// open-world even when underscore-private — the one case overriding the
// underscore convention, which the identifier mention set misses (`__all__` lists
// names as string literals).
const pythonAllExportsMetaKey = "py_all_exports"

// writePythonAllExports persists the project-wide Python `__all__` set, unioning
// with the existing set (same self-heals-on-rebuild rationale as above).
func writePythonAllExports(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, pythonAllExportsMetaKey, collected)
}

// langspecAnnotatedMetaKey is the sense_meta key holding the project-wide set of
// langspec (Java/Kotlin/C#/Scala/C++/PHP/C) class/method/function names carrying
// any annotation or attribute. The dead-code langspec voice reads it: with no
// per-framework voice for these languages, a framework's DI/router/test runner may
// dispatch an annotated symbol with no source caller, so it stays open-world
// (ls_annotated) rather than earning `dead`. Flat, not per-language — annotations
// span the shared table-driven langspec extractor.
const langspecAnnotatedMetaKey = "langspec_annotated"

// writeLangspecAnnotated persists the project-wide langspec annotated-name set,
// unioning with the existing set (same self-heals-on-rebuild rationale as above).
func writeLangspecAnnotated(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, langspecAnnotatedMetaKey, collected)
}

// addNamesByLang unions names into byLang[lang], creating the language's set on
// first use. Both per-language name accumulators (dispatch, mention) share it so
// the handler keeps each language's names apart for per-language meta writes.
func addNamesByLang(byLang map[string]map[string]struct{}, lang string, names []string) {
	set := byLang[lang]
	if set == nil {
		set = map[string]struct{}{}
		byLang[lang] = set
	}
	for _, n := range names {
		set[n] = struct{}{}
	}
}

// writeNameSet persists a name set to a sense_meta key, UNIONing with the
// already-persisted set rather than overwriting. An incremental scan only
// re-walks changed files, so unioning keeps an unchanged file's names. The
// union is the safe direction for both callers — a stale name only ever keeps
// a symbol open-world (a recall loss at worst), never a false `dead`. A full
// rebuild re-walks everything and self-heals truly removed names. When the
// collected set and the persisted set are both empty, the key is left absent.
func writeNameSet(ctx context.Context, idx *sqlite.Adapter, key string, collected map[string]struct{}) error {
	existing, err := readNameSet(ctx, idx, key)
	if err != nil {
		return err
	}
	if len(collected) == 0 && len(existing) == 0 {
		return nil
	}
	union := make(map[string]struct{}, len(existing)+len(collected))
	for n := range existing {
		union[n] = struct{}{}
	}
	for n := range collected {
		union[n] = struct{}{}
	}
	names := make([]string, 0, len(union))
	for n := range union {
		names = append(names, n)
	}
	sort.Strings(names)
	b, err := json.Marshal(names)
	if err != nil {
		return err
	}
	return idx.WriteMeta(ctx, key, string(b))
}

// readMentionedNames returns one language's persisted mention set, or an empty
// set when the key is absent. A corrupt value is treated as empty — a missing
// mention signal degrades to recall loss (a would-be `dead` stays open-world),
// never a crash or a false `dead`.
func readMentionedNames(ctx context.Context, idx *sqlite.Adapter, lang string) (map[string]struct{}, error) {
	return readNameSet(ctx, idx, mentionedNamesKey(lang))
}

// readNameSet reads a JSON string-array sense_meta value into a set, treating
// an absent or corrupt value as empty (self-heals on the next scan).
func readNameSet(ctx context.Context, idx *sqlite.Adapter, key string) (map[string]struct{}, error) {
	raw, err := idx.ReadMeta(ctx, key)
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	if raw == "" {
		return out, nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return out, nil // corrupt → treat as empty, self-heals on next scan
	}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out, nil
}
