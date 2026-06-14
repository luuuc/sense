package setup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// codexConfigSection is the marker-delimited TOML block that registers the
// Sense MCP server with the Codex CLI. Codex reads MCP servers only from
// config.toml ([mcp_servers.<name>]); it ignores the .mcp.json that Claude
// Code and Cursor read, so without this block Codex never sees the sense_*
// tools and falls back to shelling out `sense ... --json`.
const codexConfigSection = "# sense:start — managed by `sense setup`; edit Sense's entry outside these markers\n" +
	"[mcp_servers.sense]\n" +
	"command = \"sense\"\n" +
	"args = [\"mcp\"]\n" +
	"# sense:end"

// codexTrustNote is printed after setup so users know the project-scoped
// config only takes effect once Codex trusts the project, with the global
// registration as the fallback.
const codexTrustNote = "Codex loads project `.codex/config.toml` only in trusted projects — " +
	"open Codex here and trust the project, or register globally with `codex mcp add sense -- sense mcp`."

// detectCodexCLI looks for evidence that the Codex CLI is installed.
func detectCodexCLI() DetectResult {
	r := DetectResult{Tool: ToolCodexCLI}
	if _, err := exec.LookPath("codex"); err == nil {
		r.Found = true
		r.Evidence = "codex on PATH"
		return r
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".codex")); err == nil {
			r.Found = true
			r.Evidence = "~/.codex/ directory"
			return r
		}
	}
	return r
}

// configureCodexCLI writes the shared .mcp.json server entry and the AGENTS.md
// guidance Codex reads.
func configureCodexCLI(root string) (*ToolResult, error) {
	tr := &ToolResult{Tool: ToolCodexCLI}

	if wrote, err := writeCodexConfigTOML(root); err != nil {
		return nil, fmt.Errorf("write .codex/config.toml: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".codex/config.toml")
	}

	// .mcp.json is written for consistency with the other tools in a shared
	// repo; Codex itself ignores it (see codexConfigSection).
	if wrote, err := writeMCPJSON(root); err != nil {
		return nil, fmt.Errorf("write .mcp.json: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, ".mcp.json")
	}

	if wrote, err := writeAgentsMD(root); err != nil {
		return nil, fmt.Errorf("write AGENTS.md: %w", err)
	} else if wrote {
		tr.Files = append(tr.Files, "AGENTS.md")
	}

	tr.Notes = append(tr.Notes, codexTrustNote)

	return tr, nil
}

// writeCodexConfigTOML registers the Sense MCP server in the project-scoped
// .codex/config.toml using marker-delimited TOML so re-runs are idempotent.
// If the user already declared [mcp_servers.sense] by hand (no Sense
// markers), it is left untouched: a second table would be a TOML
// duplicate-key error.
func writeCodexConfigTOML(root string) (bool, error) {
	path := filepath.Join(root, ".codex", "config.toml")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	content := string(data)
	if !strings.Contains(content, tomlMarkerStart) && strings.Contains(content, "[mcp_servers.sense]") {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return writeMarkerFileWith(path, codexConfigSection, tomlMarkerStart, tomlMarkerEnd)
}

// writeAgentsMD creates or updates the Sense section in AGENTS.md.
// Uses the same marker-comment strategy as CLAUDE.md.
func writeAgentsMD(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, "AGENTS.md"), guidanceMarkdown)
}
