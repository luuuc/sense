package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// readJSONFile reads a JSON file into a map. Returns an empty map if
// the file does not exist. Returns an error (and backs up the file)
// if the file exists but is not valid JSON.
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
		backup := path + ".bak"
		_ = os.WriteFile(backup, data, 0o644)
		return nil, fmt.Errorf("parse %s (backed up to %s): %w", path, backup, err)
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
