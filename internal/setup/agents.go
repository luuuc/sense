package setup

var agents = []templateFile{
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
4. Use sense_blast for impact, and for "what holds X" - its retained_via_interfaces lists holders that keep X behind an interface-typed field and never name X, which no search or graph call surfaces
5. Use Read only to examine specific file contents
6. Synthesize findings into a clear summary
`,
	},
}

// writeAgents creates agent files in .claude/agents/. Existing files
// are overwritten to pick up template changes on re-run.
func writeAgents(root string) (int, error) {
	return writeTemplateFiles(root, "agents", agents)
}
