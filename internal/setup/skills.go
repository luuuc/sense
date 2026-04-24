package setup

import (
	"os"
	"path/filepath"
)

type skill struct {
	filename string
	content  string
}

var skills = []skill{
	{
		filename: "sense-explore.md",
		content: `---
name: sense-explore
description: Explore codebase structure using the Sense graph — find symbols, trace callers, understand architecture
---

# Explore codebase with Sense

Use Sense MCP tools to navigate the codebase structurally.

## Steps

1. Run ` + "`sense_status`" + ` to confirm the index is healthy.
2. Run ` + "`sense_search query=\"<topic>\"`" + ` to find relevant symbols.
3. For each interesting symbol, run ` + "`sense_graph symbol=\"<name>\"`" + ` to see callers, callees, and relationships.
4. If a symbol has many connections, run ` + "`sense_graph symbol=\"<name>\" direction=\"callers\"`" + ` or ` + "`sense_graph symbol=\"<name>\" direction=\"callees\"`" + ` to focus.
5. Summarize the architecture you found: key symbols, how they connect, entry points.
`,
	},
	{
		filename: "sense-impact.md",
		content: `---
name: sense-impact
description: Check blast radius and impact before changing code — what breaks if you modify a symbol
---

# Impact analysis with Sense

Use Sense blast radius to understand what a change will affect before making it.

## Steps

1. Run ` + "`sense_blast symbol=\"<name>\"`" + ` to see direct and indirect callers.
2. Review the affected files and symbols — pay attention to test coverage.
3. If the blast radius is large, consider whether to change the interface or add a new symbol instead.
4. After making changes, run ` + "`sense_blast diff=\"HEAD~1\"`" + ` to verify the actual scope matches expectations.
`,
	},
	{
		filename: "sense-conventions.md",
		content: `---
name: sense-conventions
description: Check project patterns and conventions before writing code — naming, structure, testing, inheritance
---

# Check conventions with Sense

Use Sense conventions to understand project patterns before writing new code.

## Steps

1. Run ` + "`sense_conventions`" + ` to see all detected patterns.
2. If working in a specific domain, run ` + "`sense_conventions domain=\"<domain>\"`" + ` to filter.
3. Follow the detected patterns in your new code — match naming conventions, file structure, testing patterns, and inheritance hierarchies.
4. If you need to deviate from a convention, note why.
`,
	},
}

// writeSkills creates skill files in .claude/skills/. Existing files
// are overwritten to pick up template changes on --init.
func writeSkills(root string) (int, error) {
	dir := filepath.Join(root, ".claude", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}

	written := 0
	for _, s := range skills {
		path := filepath.Join(dir, s.filename)
		if err := os.WriteFile(path, []byte(s.content), 0o644); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
