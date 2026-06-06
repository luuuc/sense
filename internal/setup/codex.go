package setup

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

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

	return tr, nil
}

// writeAgentsMD creates or updates the Sense section in AGENTS.md.
// Uses the same marker-comment strategy as CLAUDE.md.
func writeAgentsMD(root string) (bool, error) {
	return writeMarkerFile(filepath.Join(root, "AGENTS.md"), guidanceMarkdown)
}
