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
