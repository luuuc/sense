package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// detectCursor looks for evidence that Cursor is installed.
func detectCursor() DetectResult {
	r := DetectResult{Tool: ToolCursor}
	if hasCursorEnv() {
		r.Found = true
		r.Evidence = "CURSOR_* env var set"
		return r
	}
	if _, err := exec.LookPath("cursor"); err == nil {
		r.Found = true
		r.Evidence = "cursor on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".cursor")); err == nil {
			r.Found = true
			r.Evidence = "~/.cursor/ directory"
			return r
		}
	}
	return r
}

// cursorSessionEnvs are the env vars whose presence means the user is running
// inside Cursor right now. Single source: detectCursor reads them via
// hasCursorEnv, and the registry exposes them to DetectCurrent as currentEnv.
var cursorSessionEnvs = []string{"CURSOR_TRACE_ID", "CURSOR_SESSION_ID"}

// hasCursorEnv reports whether a Cursor session env var is set.
func hasCursorEnv() bool {
	for _, key := range cursorSessionEnvs {
		if os.Getenv(key) != "" {
			return true
		}
	}
	return false
}

// configureCursor writes Cursor's MCP config and its .cursorrules guidance.
func configureCursor(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolCursor}

	if wrote, err := writeCursorMCPJSON(root); err != nil {
		return nil, fmt.Errorf("write .cursor/mcp.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".cursor/mcp.json")
	}

	if wrote, err := writeCursorRules(root); err != nil {
		return nil, fmt.Errorf("write .cursorrules: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".cursorrules")
	}

	return tr, nil
}

// writeCursorMCPJSON creates or merges the Sense MCP server entry into
// .cursor/mcp.json (Cursor's MCP config location).
func writeCursorMCPJSON(root string) (bool, error) {
	dir := filepath.Join(root, ".cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	path := filepath.Join(dir, "mcp.json")

	senseCfg := map[string]any{
		"command": "sense",
		"args":    []any{"mcp"},
	}

	existing, err := readJSONFile(path)
	if err != nil {
		return false, err
	}

	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers["sense"] = senseCfg
	existing["mcpServers"] = servers

	if err := writeJSONFile(path, existing); err != nil {
		return false, err
	}
	return true, nil
}

// writeCursorRules creates or updates the Sense section in .cursorrules.
// Uses the same marker-comment strategy as CLAUDE.md for idempotent merges.
func writeCursorRules(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, ".cursorrules"), guidanceMarkdown)
}
