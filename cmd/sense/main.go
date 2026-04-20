package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/mcpserver"
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
  status        Index health and embedding coverage
  mcp           Start the MCP server (stdio transport)
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
		if _, err := scan.Run(ctx, scan.Options{
			EmbeddingsEnabled: cli.EmbeddingsEnabled("."),
		}); err != nil {
			fmt.Fprintln(os.Stderr, "sense scan:", err)
			os.Exit(1)
		}

	case "graph":
		os.Exit(cli.RunGraph(os.Args[2:], cli.DefaultIO()))

	case "blast":
		os.Exit(cli.RunBlast(os.Args[2:], cli.DefaultIO()))

	case "mcp":
		fs := flag.NewFlagSet("sense mcp", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		dir := fs.String("dir", ".", "project root containing .sense/")
		if err := fs.Parse(os.Args[2:]); err != nil {
			os.Exit(1)
		}
		if err := mcpserver.Run(*dir); err != nil {
			fmt.Fprintln(os.Stderr, "sense mcp:", err)
			os.Exit(1)
		}

	case "status":
		os.Exit(cli.RunStatus(os.Args[2:], cli.DefaultIO()))

	case "search", "conventions":
		fmt.Fprintf(os.Stderr,
			"sense: %q is not yet implemented — see .doc/pitches/ for the build plan\n", cmd)
		os.Exit(1)

	default:
		fmt.Fprintf(os.Stderr, "sense: unknown command %q. Run 'sense help'.\n", cmd)
		os.Exit(1)
	}
}

