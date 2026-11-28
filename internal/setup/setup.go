// Package setup generates AI tool integration files for detected
// coding tools. It writes per-tool config files (MCP server entries,
// routing guidance, hooks, skills) so that AI tools discover and
// prefer Sense without manual configuration.
//
// The set of tools is a registry (registry.go): each tool is one entry
// pairing a detector with a configurer, and each tool's files live in
// its own file (claude.go, cursor.go, codex.go, opencode.go). Adding a
// tool touches a new file plus one registry line; see
// CONTRIBUTING-AN-AI-TOOL.md.
//
// All writes are idempotent: JSON files are deep-merged, Markdown
// files use marker comments, skill files are overwritten. Running
// setup twice produces the same result.
package setup

import (
	"fmt"
	"io"
	"strings"
)

// Options controls which tools setup configures.
type Options struct {
	// Tools overrides auto-detection. When non-empty, only these
	// tools are configured regardless of detection results.
	Tools []Tool

	// CurrentOnly restricts setup to the tool the user is currently
	// running inside (detected via env vars). Used by scan first-run.
	CurrentOnly bool

	// SkipConfigured leaves a tool alone when its Sense guidance section is
	// already in the project, so a fresh clone or worktree of a repo that
	// commits its integration keeps those files as committed. Used by scan
	// first-run; `sense setup` always writes.
	SkipConfigured bool
}

// ToolResult summarises what was written for a single tool.
type ToolResult struct {
	Tool  Tool
	Files []string // relative paths of files written
	Notes []string // post-setup guidance printed after the file list
}

// Result summarises what setup wrote across all tools.
type Result struct {
	Tools []ToolResult

	// Project lists teardown items belonging to no single tool: the .sense
	// index directory and the .gitignore entry. Undo populates it; Run
	// leaves it empty, since setup writes neither.
	Project []string

	// Notes carries guidance printed after the summary (the machine-wide
	// cache Undo deliberately spares, a hand-written config.yml that went
	// with the index).
	Notes []string
}

// Run detects installed AI tools and writes integration files into root.
// When opts is nil, all detected tools are configured.
func Run(root string, out io.Writer, opts *Options) (*Result, error) {
	tools := resolveTools(opts)
	res := &Result{}

	for _, t := range tools {
		if opts != nil && opts.SkipConfigured && isConfigured(root, t) {
			_, _ = fmt.Fprintf(out, "Sense is already set up for %s here; left its files alone (run `sense setup` to refresh them).\n", t.DisplayName())
			continue
		}
		tr, err := configureTool(root, t)
		if err != nil {
			return nil, fmt.Errorf("configure %s: %w", t.DisplayName(), err)
		}
		res.Tools = append(res.Tools, *tr)
	}

	printSetupSummary(out, res)
	return res, nil
}

func resolveTools(opts *Options) []Tool {
	if opts != nil && len(opts.Tools) > 0 {
		return opts.Tools
	}
	if opts != nil && opts.CurrentOnly {
		return []Tool{DetectCurrent()}
	}
	var tools []Tool
	for _, dr := range DetectAll() {
		if dr.Found {
			tools = append(tools, dr.Tool)
		}
	}
	if len(tools) == 0 {
		tools = []Tool{ToolClaudeCode}
	}
	return tools
}

func printSetupSummary(out io.Writer, res *Result) {
	if len(res.Tools) == 0 {
		return
	}

	_, _ = fmt.Fprintln(out, "")
	for _, tr := range res.Tools {
		_, _ = fmt.Fprintf(out, "Configuring %s...\n", tr.Tool.DisplayName())
		for _, f := range tr.Files {
			_, _ = fmt.Fprintf(out, "  wrote %s\n", f)
		}
		for _, n := range tr.Notes {
			_, _ = fmt.Fprintf(out, "  note: %s\n", n)
		}
		_, _ = fmt.Fprintln(out, "")
	}

	var names []string
	for _, tr := range res.Tools {
		names = append(names, tr.Tool.DisplayName())
	}
	_, _ = fmt.Fprintf(out, "Done. Sense is configured for %s.\n", joinNames(names))
}

func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}
