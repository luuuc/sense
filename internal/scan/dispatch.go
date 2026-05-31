package scan

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/luuuc/sense/internal/sqlite"
)

// dispatchNamesMetaKey is the sense_meta key holding the project-wide set of
// reflective dispatch-target names (send/const_get/define_method literals,
// constantize receivers) as a JSON string array. The dead-code arbiter reads
// it so a symbol whose name appears here stays open-world (possibly_dead)
// rather than being falsely called dead.
const dispatchNamesMetaKey = "dispatch_names"

// writeDispatchNames persists the dispatch-name set gathered during the walk,
// unioning with the existing set (see writeNameSet for the union rationale).
func writeDispatchNames(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, dispatchNamesMetaKey, collected)
}

// readDispatchNames returns the persisted dispatch-name set, or an empty set
// when the key is absent. A corrupt value is treated as empty rather than
// fatal — a missing reflection signal degrades to recall loss, never a crash.
func readDispatchNames(ctx context.Context, idx *sqlite.Adapter) (map[string]struct{}, error) {
	return readNameSet(ctx, idx, dispatchNamesMetaKey)
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

// writeMentionedNames persists the broad mention set, unioning with the
// existing set (see writeNameSet for the union rationale).
func writeMentionedNames(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	return writeNameSet(ctx, idx, mentionedNamesMetaKey, collected)
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

// readMentionedNames returns the persisted mention set, or an empty set when
// the key is absent. A corrupt value is treated as empty — a missing mention
// signal degrades to recall loss (a would-be `dead` stays open-world), never a
// crash or a false `dead`.
func readMentionedNames(ctx context.Context, idx *sqlite.Adapter) (map[string]struct{}, error) {
	return readNameSet(ctx, idx, mentionedNamesMetaKey)
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
