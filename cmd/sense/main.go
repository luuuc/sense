package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/hook"
	"github.com/luuuc/sense/internal/mcpserver"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/version"
	"github.com/luuuc/sense/internal/versioncheck"
	"github.com/luuuc/sense/internal/watch"
)

const helpText = `sense — codebase understanding that any tool can query

Usage: sense [command] [args]

Commands:
  scan          Build or refresh the index
  setup         Configure AI tool integrations (Claude Code, Cursor, Codex CLI)
  search        Hybrid semantic + keyword search
  graph         Symbol relationships — callers, callees, inheritance, tests
  blast         Blast radius for a symbol or diff
  dead          Find dead code (symbols with no incoming references)
  conventions   Detected project conventions
  status        Index health and embedding coverage
  doctor        Diagnose common index problems
  hook          Claude Code lifecycle hooks (pre-tool-use, pre-compact, etc.)
  mcp           Start the MCP server (stdio transport)
  update        Check for and install the latest version
  version       Print version
  help          Show this help

Run 'sense <command> --help' for per-command usage and exit codes.
`

func main() {
	ctx := context.Background()

	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, helpText)
		os.Exit(2)
	}

	cmd := os.Args[1]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("sense %s (schema v%d, embeddings: %s)\n",
			version.Version, sqlite.SchemaVersion, embed.ModelID)

	case "help", "--help", "-h":
		fmt.Print(helpText)

	case "scan":
		fs := flag.NewFlagSet("sense scan", flag.ContinueOnError)
		fs.SetOutput(os.Stderr)
		watchFlag := fs.Bool("watch", false, "keep running and re-index on file changes")
		embedFlag := fs.Bool("embed", false, "block until embeddings complete (default: defer to MCP server)")
		quietFlag := fs.Bool("quiet", false, "suppress warnings")
		dir := fs.String("dir", ".", "project root")
		cpuprofile := fs.String("cpuprofile", "", "write CPU profile to file")
		memprofile := fs.String("memprofile", "", "write heap profile to file on exit")
		if err := fs.Parse(os.Args[2:]); err != nil {
			os.Exit(1)
		}

		if *cpuprofile != "" {
			f, err := os.Create(*cpuprofile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "sense scan: create cpuprofile:", err)
				os.Exit(1)
			}
			if err := pprof.StartCPUProfile(f); err != nil {
				fmt.Fprintln(os.Stderr, "sense scan: start cpuprofile:", err)
				_ = f.Close()
				os.Exit(1)
			}
			defer func() { pprof.StopCPUProfile(); _ = f.Close() }()
		}

		var warnSink io.Writer
		if *quietFlag {
			warnSink = io.Discard
		}

		if *watchFlag {
			if err := watch.Run(ctx, watch.RunOptions{
				Root:              *dir,
				EmbeddingsEnabled: cli.EmbeddingsEnabled(*dir),
			}); err != nil {
				fmt.Fprintln(os.Stderr, "sense scan --watch:", err)
				os.Exit(1)
			}
		} else {
			if _, err := scan.Run(ctx, scan.Options{
				Root:              *dir,
				Warnings:          warnSink,
				EmbeddingsEnabled: cli.EmbeddingsEnabled(*dir),
				Embed:             *embedFlag,
			}); err != nil {
				fmt.Fprintln(os.Stderr, "sense scan:", err)
				os.Exit(1)
			}
		}

		if *memprofile != "" {
			f, err := os.Create(*memprofile)
			if err != nil {
				fmt.Fprintln(os.Stderr, "sense scan: create memprofile:", err)
				os.Exit(1)
			}
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintln(os.Stderr, "sense scan: write memprofile:", err)
			}
			_ = f.Close()
		}

	case "setup":
		os.Exit(cli.RunSetup(os.Args[2:], cli.DefaultIO()))

	case "search":
		os.Exit(cli.RunSearch(os.Args[2:], cli.DefaultIO()))

	case "graph":
		os.Exit(cli.RunGraph(os.Args[2:], cli.DefaultIO()))

	case "blast":
		os.Exit(cli.RunBlast(os.Args[2:], cli.DefaultIO()))

	case "dead":
		os.Exit(cli.RunDead(os.Args[2:], cli.DefaultIO()))

	case "conventions":
		os.Exit(cli.RunConventions(os.Args[2:], cli.DefaultIO()))

	case "status":
		os.Exit(cli.RunStatus(os.Args[2:], cli.DefaultIO()))

	case "doctor":
		os.Exit(cli.RunDoctor(os.Args[2:], cli.DefaultIO()))

	case "hook":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: sense hook <pre-tool-use|pre-compact|subagent-start|session-start>")
			os.Exit(1)
		}
		os.Exit(hook.Run(os.Args[2], ".", os.Stdin, os.Stdout))

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

	case "update":
		os.Exit(versioncheck.Update(os.Stdout, os.Stderr))

	default:
		fmt.Fprintf(os.Stderr, "sense: unknown command %q. Run 'sense help'.\n", cmd)
		os.Exit(1)
	}
}
