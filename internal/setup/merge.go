package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// readJSONFile reads a JSON file into a map. Returns an empty map if the file
// does not exist, and an error if it exists but is not valid JSON.
//
// It writes nothing on that error. Every caller stops there without touching
// the file, so the config the user needs to fix is still on disk, unchanged,
// exactly where the error names it. An earlier version copied it to a .bak
// beside itself, which protected nothing and left a second copy of a broken
// config in the repo for the agent to read and for git to offer.
func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, err
	}

	if len(data) == 0 {
		return map[string]any{}, nil
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// writeJSONFile writes a map as formatted JSON.
func writeJSONFile(path string, m map[string]any) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// mergeHooks merges Sense hook entries into an existing settings map
// without overwriting hooks from other tools. For each event type,
// if a hook array entry already has a command containing "sense hook",
// it is replaced; otherwise the Sense entry is appended.
func mergeHooks(settings map[string]any, senseHooks map[string]any) {
	existing, _ := settings["hooks"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}

	for event, senseEntries := range senseHooks {
		senseArr, _ := senseEntries.([]any)
		if len(senseArr) == 0 {
			continue
		}

		existingArr, _ := existing[event].([]any)
		merged := removeSenseEntries(existingArr)
		merged = append(merged, senseArr...)
		existing[event] = merged
	}

	settings["hooks"] = existing
}

// removeRetiredHook strips Sense entries from a hook event that Sense no
// longer writes (a retired hook), preserving any non-Sense entries the user
// added. If the event has no entries left, the event key is removed. This
// migrates older settings.json files on the next `sense setup`.
func removeRetiredHook(settings map[string]any, event string) {
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		return
	}
	arr, ok := hooks[event].([]any)
	if !ok {
		return
	}
	kept := removeSenseEntries(arr)
	if len(kept) == 0 {
		delete(hooks, event)
		return
	}
	hooks[event] = kept
}

// removeSenseEntries filters out hook array entries that contain
// a "sense hook" command, so they can be replaced with fresh ones.
func removeSenseEntries(entries []any) []any {
	kept := []any{}
	for _, entry := range entries {
		m, ok := entry.(map[string]any)
		if !ok {
			kept = append(kept, entry)
			continue
		}
		if isSenseHookEntry(m) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

// isSenseHookEntry checks whether a hook entry contains a "sense hook" command.
func isSenseHookEntry(entry map[string]any) bool {
	hooks, _ := entry["hooks"].([]any)
	for _, h := range hooks {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := hm["command"].(string)
		if strings.HasPrefix(cmd, "sense hook") {
			return true
		}
	}
	return false
}

// mergePermissions adds permission patterns to the allow list without
// duplicating existing entries.
func mergePermissions(settings map[string]any, patterns []string) {
	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}

	var allow []any
	if existing, ok := perms["allow"].([]any); ok {
		allow = existing
	}

	seen := map[string]bool{}
	for _, a := range allow {
		if s, ok := a.(string); ok {
			seen[s] = true
		}
	}

	for _, p := range patterns {
		if !seen[p] {
			allow = append(allow, p)
		}
	}

	perms["allow"] = allow
	settings["permissions"] = perms
}

// pruneJSONFile applies strip to the JSON object at path and writes the result
// back, deleting the file when stripping emptied it: an empty .mcp.json is not
// "gone". A missing file is a no-op. An unparseable file errors exactly as it
// does on the setup path, and is left untouched.
func pruneJSONFile(path string, strip func(map[string]any) bool) (outcome, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return outcomeUnchanged, nil
		}
		return outcomeUnchanged, err
	}

	existing, err := readJSONFile(path)
	if err != nil {
		return outcomeUnchanged, err
	}
	if !strip(existing) {
		return outcomeUnchanged, nil
	}

	if len(existing) == 0 {
		if err := removeFile(path); err != nil {
			return outcomeUnchanged, err
		}
		return outcomeDeleted, nil
	}
	if err := writeJSONFile(path, existing); err != nil {
		return outcomeUnchanged, err
	}
	return outcomeStripped, nil
}

// removeMCPServer deletes Sense's entry from the MCP-server map held under key
// ("mcpServers" for Claude Code and Cursor, "mcp" for OpenCode), pruning the
// map itself when Sense's was the only server declared.
func removeMCPServer(m map[string]any, key string) bool {
	servers, _ := m[key].(map[string]any)
	if _, ok := servers["sense"]; !ok {
		return false
	}
	delete(servers, "sense")
	if len(servers) == 0 {
		delete(m, key)
	}
	return true
}

// removeSenseHooks strips every Sense hook entry from every event in the
// settings map, dropping events, and the hooks key, that it empties. It
// walks all events rather than the list setup currently writes, so a hook an
// older version installed (a retired PostToolUse, say) is torn down too.
func removeSenseHooks(settings map[string]any) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	changed := false

	for event, entries := range hooks {
		arr, ok := entries.([]any)
		if !ok {
			continue
		}
		kept := removeSenseEntries(arr)
		if len(kept) == len(arr) {
			continue
		}
		changed = true
		if len(kept) == 0 {
			delete(hooks, event)
			continue
		}
		hooks[event] = kept
	}

	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return changed
}

// removeSensePermissions drops Sense's tool pattern from the allow list,
// pruning the list and the permissions block when nothing else remains.
func removeSensePermissions(settings map[string]any, patterns []string) bool {
	perms, _ := settings["permissions"].(map[string]any)
	allow, ok := perms["allow"].([]any)
	if !ok {
		return false
	}

	drop := map[string]bool{}
	for _, p := range patterns {
		drop[p] = true
	}

	kept := []any{}
	for _, a := range allow {
		if s, isStr := a.(string); isStr && drop[s] {
			continue
		}
		kept = append(kept, a)
	}
	if len(kept) == len(allow) {
		return false
	}

	perms["allow"] = kept
	if len(kept) == 0 {
		delete(perms, "allow")
	}
	if len(perms) == 0 {
		delete(settings, "permissions")
	}
	return true
}

// keepUserCommand carries a user-set command for the Sense server from the
// existing entry into the one setup is about to write. A team that starts the
// server through a wrapper script keeps it when setup re-runs. The args go
// with the command they belong to, absent args included.
func keepUserCommand(servers, senseCfg map[string]any) {
	old, _ := servers["sense"].(map[string]any)
	cmd, _ := old["command"].(string)
	if cmd == "" || cmd == "sense" {
		return
	}
	senseCfg["command"] = cmd
	delete(senseCfg, "args")
	if args, ok := old["args"]; ok {
		senseCfg["args"] = args
	}
}
