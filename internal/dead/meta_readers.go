package dead

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"strings"
)

// This file holds the meta-readers: the helpers that load the per-language and
// per-feature harvested-name sets the scan layer wrote to sense_meta, which the
// dead-code voices read back to keep a name open-world. Every reader degrades a
// missing or corrupt value to an empty set — recall loss, never a crash and
// never a false `dead`.

func readFrameworks(ctx context.Context, db *sql.DB) map[string]struct{} {
	out := map[string]struct{}{}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM sense_meta WHERE key = 'frameworks'`).Scan(&raw)
	if err != nil {
		return out
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		log.Printf("dead: corrupt frameworks meta: %v", err)
		return out
	}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// readDispatchNames returns the per-language sets of reflective dispatch-target
// names persisted to sense_meta by the scan layer under per-language keys
// (dispatch_names:<lang>). A symbol whose name is in its own language's set
// could be invoked dynamically, so the core voice keeps it open-world. A
// missing or corrupt value yields an empty map — degrading to recall loss,
// never a crash or a false `dead`.
func readDispatchNames(ctx context.Context, db *sql.DB) map[string]map[string]struct{} {
	return readNameSetMetaByLang(ctx, db, "dispatch_names")
}

// readMentionedNames returns the per-language broad mention sets persisted to
// sense_meta by the scan layer under per-language keys (mentioned_names:<lang>,
// every identifier/symbol token except definition names). The arbiter's
// soundness gate earns `dead` only when a candidate's name is absent from a
// NON-EMPTY set FOR ITS OWN LANGUAGE — mentioned nowhere a hidden caller could
// be. A language with no key never harvested, which the gate treats as
// "cannot prove closed-world" and blocks `dead` (core_no_harvest, fail-closed).
// A pre-feature index carries only the legacy union key `mentioned_names` (no
// language suffix); it does not match the per-language prefix, so every language
// reads as un-harvested and the whole index degrades to possibly_dead — never a
// false `dead` off stale union data.
func readMentionedNames(ctx context.Context, db *sql.DB) map[string]map[string]struct{} {
	return readNameSetMetaByLang(ctx, db, "mentioned_names")
}

// readHarvestedLangs returns the set of languages whose mention harvest ran,
// persisted to the harvested_langs sense_meta key by the scan layer. The
// soundness gate refuses `dead` for a symbol whose language is absent here. A
// pre-feature index has no such key (the harvest predates it), so every language
// reads as un-harvested and the index degrades to possibly_dead — the safe
// direction. An absent or corrupt value yields an empty set.
func readHarvestedLangs(ctx context.Context, db *sql.DB) map[string]struct{} {
	return readStringSetMeta(ctx, db, "harvested_langs")
}

// The flat per-feature harvested-name sets (cgo_exports, the four rust_* sets,
// the two ts_* sets, the four py_* sets, and langspec_annotated) are each a JSON
// string-set under their own sense_meta key, read identically via
// readStringSetMeta. buildFacts reads each directly by key into its Facts field;
// the soundness role of every set is documented on that field (see arbiter.go),
// so naming the key once at the read site keeps the contract leak-resistant — a
// set can only be read under its own key.

// readStringSetMeta reads a JSON string-array sense_meta value into a set,
// treating an absent or corrupt value as empty.
func readStringSetMeta(ctx context.Context, db *sql.DB, key string) map[string]struct{} {
	out := map[string]struct{}{}
	var raw string
	err := db.QueryRowContext(ctx, `SELECT value FROM sense_meta WHERE key = ?`, key).Scan(&raw)
	if err != nil || raw == "" {
		return out
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		log.Printf("dead: corrupt %s meta: %v", key, err)
		return out
	}
	for _, n := range names {
		out[n] = struct{}{}
	}
	return out
}

// readNameSetMetaByLang reads every per-language sense_meta key with the given
// prefix (e.g. mentioned_names:ruby, mentioned_names:go) into a language-keyed
// map of name sets. A GLOB match on `prefix:*` discovers the languages present;
// the legacy union key (the bare prefix, no `:lang`) does not match and is
// ignored, so a pre-feature index reads as no-languages-harvested (fail-closed).
// An absent or corrupt value for any one language yields an empty set for it,
// self-healing on the next scan.
func readNameSetMetaByLang(ctx context.Context, db *sql.DB, prefix string) map[string]map[string]struct{} {
	out := map[string]map[string]struct{}{}
	rows, err := db.QueryContext(ctx,
		`SELECT key, value FROM sense_meta WHERE key GLOB ?`, prefix+":*")
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			continue
		}
		lang := strings.TrimPrefix(key, prefix+":")
		if lang == "" {
			continue
		}
		set := map[string]struct{}{}
		var names []string
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &names); err != nil {
				log.Printf("dead: corrupt %s meta: %v", key, err)
			}
		}
		for _, n := range names {
			set[n] = struct{}{}
		}
		out[lang] = set
	}
	return out
}
