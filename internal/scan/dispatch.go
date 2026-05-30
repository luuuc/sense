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

// writeDispatchNames persists the dispatch-name set gathered during the walk.
// It UNIONS the freshly-collected names with the already-persisted set rather
// than overwriting, because an incremental scan only re-walks changed files —
// a name defined in an unchanged file would otherwise be lost. The union is
// the safe direction for a trust feature: a stale dispatch name keeps a symbol
// open-world (a recall loss at worst), and can never produce a false `dead`.
// A full rescan (rebuild) re-walks everything, so the set self-heals of truly
// removed names then.
//
// When the collected set is empty AND nothing is persisted, the key is left
// absent. An empty collected set with an existing key is left untouched (the
// incremental case where no changed file used reflection).
func writeDispatchNames(ctx context.Context, idx *sqlite.Adapter, collected map[string]struct{}) error {
	existing, err := readDispatchNames(ctx, idx)
	if err != nil {
		return err
	}
	if len(collected) == 0 && len(existing) == 0 {
		return nil
	}
	// At least one set is non-empty here (the guard above returns otherwise),
	// so the union always has at least one name.
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
	return idx.WriteMeta(ctx, dispatchNamesMetaKey, string(b))
}

// readDispatchNames returns the persisted dispatch-name set, or an empty set
// when the key is absent. A corrupt value is treated as empty rather than
// fatal — a missing reflection signal degrades to recall loss, never a crash.
func readDispatchNames(ctx context.Context, idx *sqlite.Adapter) (map[string]struct{}, error) {
	raw, err := idx.ReadMeta(ctx, dispatchNamesMetaKey)
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
