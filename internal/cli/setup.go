package cli

import (
	"flag"
	"fmt"

	"github.com/luuuc/sense/internal/setup"
)

const setupHelp = `usage: sense setup [flags]

Configure AI tool integrations for this project. Auto-detects installed
tools (Claude Code, Cursor, Codex CLI) and writes integration files.

Flags:
  --tools   comma-separated list of tools to configure (overrides detection)

Examples:
  sense setup                           # auto-detect and configure all
  sense setup --tools cursor            # configure Cursor only
  sense setup --tools claude-code,codex-cli
`

// RunSetup configures AI tool integrations for the project.
func RunSetup(args []string, cio IO) int {
	fs := flag.NewFlagSet("sense setup", flag.ContinueOnError)
	fs.SetOutput(cio.Stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(cio.Stderr, setupHelp) }
	toolsFlag := fs.String("tools", "", "comma-separated list of tools to configure (claude-code,cursor,codex-cli)")
	if err := fs.Parse(args); err != nil {
		return ExitGeneralError
	}

	var opts setup.Options
	if *toolsFlag != "" {
		tools, err := setup.ParseTools(*toolsFlag)
		if err != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense setup:", err)
			return ExitGeneralError
		}
		opts.Tools = tools
	} else {
		setup.PrintDetection(cio.Stdout)
	}

	if _, err := setup.Run(cio.Dir, cio.Stdout, &opts); err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense setup:", err)
		return ExitGeneralError
	}
	return ExitSuccess
}
