package main

import (
	"context"
	"fmt"
	"os"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/version"
)

const helpText = `sense — codebase understanding that any tool can query

Usage: sense <command> [args]

Commands:
  scan          Build or refresh the index
  search        Hybrid semantic + keyword search (not yet implemented)
  graph         Symbol relationships — callers, callees, inheritance, tests
  blast         Blast radius for a symbol or diff
  conventions   Detected project conventions (not yet implemented)
  status        Index health (not yet implemented)
  mcp           Start the MCP server (not yet implemented)
  version       Print version
  help          Show this help

Run 'sense <command> --help' for per-command usage and exit codes.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, helpText)
		os.Exit(2)
	}

	ctx := context.Background()

	switch cmd := os.Args[1]; cmd {
	case "version", "--version", "-v":
		fmt.Println(version.Version)

	case "help", "--help", "-h":
		fmt.Print(helpText)

	case "scan":
		if _, err := scan.Run(ctx, scan.Options{}); err != nil {
			fmt.Fprintln(os.Stderr, "sense scan:", err)
			os.Exit(1)
		}

	case "graph":
		os.Exit(cli.RunGraph(os.Args[2:], cli.DefaultIO()))

	case "blast":
		os.Exit(cli.RunBlast(os.Args[2:], cli.DefaultIO()))

	case "search", "conventions", "status", "mcp":
		fmt.Fprintf(os.Stderr,
			"sense: %q is not yet implemented — see .doc/pitches/ for the build plan\n", cmd)
		os.Exit(1)

	default:
		fmt.Fprintf(os.Stderr, "sense: unknown command %q. Run 'sense help'.\n", cmd)
		os.Exit(1)
	}
}
