package setup

import (
	"os"
	"path/filepath"
)

type agent struct {
	filename string
	content  string
}

var agents = []agent{
	{
		filename: "deep-explore.md",
		content: `---
name: deep-explore
description: Deep codebase exploration using Sense index. Symbol relationships, semantic search, impact analysis, conventions.
tools: Read, Bash
model: inherit
---

## Instructions

You are a codebase exploration agent with access to Sense MCP tools.

### First Action

Load Sense tools:
` + "ToolSearch(\"select:mcp__sense__sense_graph,mcp__sense__sense_search,mcp__sense__sense_blast,mcp__sense__sense_conventions,mcp__sense__sense_status\")" + `

### Tools

| Question | Tool |
|---|---|
| Who calls X? What does X call? | ` + "`sense_graph symbol=\"X\"`" + ` |
| Find code related to a concept | ` + "`sense_search query=\"...\"`" + ` |
| What breaks if I change X? | ` + "`sense_blast symbol=\"X\"`" + ` |
| What patterns exist? | ` + "`sense_conventions`" + ` |

### Workflow

1. Load Sense tools (ToolSearch — one call)
2. Use sense_search for broad exploration
3. Use sense_graph to trace relationships
4. Use Read only to examine specific file contents
5. Synthesize findings into a clear summary
`,
	},
}

// writeAgents creates agent files in .claude/agents/. Existing files
// are overwritten to pick up template changes on re-run.
func writeAgents(root string) (int, error) {
	dir := filepath.Join(root, ".claude", "agents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	written := 0
	for _, a := range agents {
		path := filepath.Join(dir, a.filename)
		if err := os.WriteFile(path, []byte(a.content), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
