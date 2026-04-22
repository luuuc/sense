package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/pprof"

	"github.com/mattn/go-isatty"

	"github.com/luuuc/sense/internal/cli"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/mcpserver"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/tui"
	"github.com/luuuc/sense/internal/version"
	"github.com/luuuc/sense/internal/versioncheck"
	"github.com/luuuc/sense/internal/watch"
)

const helpText = `sense — codebase understanding that any tool can query

Usage: sense [command] [args]

  Running 'sense' with no arguments in a terminal launches the graph TUI.

Commands:
  scan          Build or refresh the index
  search        Hybrid semantic + keyword search
  graph         Symbol relationships — callers, callees, inheritance, tests
  blast         Blast radius for a symbol or diff
  conventions   Detected project conventions
  status        Index health and embedding coverage
  doctor        Diagnose common index problems
  mcp           Start the MCP server (stdio transport)
  version       Print version
  help          Show this help

Run 'sense <command> --help' for per-command usage and exit codes.
`

func main() {
	ctx := context.Background()

	if len(os.Args) < 2 {
		if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
			versioncheck.CheckAndNotify(os.Stderr)
			adapter, err := cli.OpenIndex(ctx, ".")
			if err != nil {
				if errors.Is(err, cli.ErrIndexMissing) {
					fmt.Fprintln(os.Stderr, "sense: no index found. Run 'sense scan' first.")
					os.Exit(3)
				}
				fmt.Fprintln(os.Stderr, "sense:", err)
				os.Exit(1)
			}
			tuiErr := tui.Run(ctx, adapter, tui.SenseDir("."))
			_ = adapter.Close()
			if tuiErr != nil {
				fmt.Fprintln(os.Stderr, "sense:", tuiErr)
				os.Exit(1)
			}
			os.Exit(0)
		}
		fmt.Fprint(os.Stderr, helpText)
		os.Exit(2)
	}

	cmd := os.Args[1]

	switch cmd {
	case "version", "--version", "-v", "help", "--help", "-h", "mcp":
		// no version check for these
	default:
		jsonMode := false
		for _, a := range os.Args[2:] {
			if a == "--" {
				break
			}
			if a == "--json" || a == "-json" {
				jsonMode = true
				break
			}
		}
		if !jsonMode {
			versioncheck.CheckAndNotify(os.Stderr)
		}
	}

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

	case "search":
		os.Exit(cli.RunSearch(os.Args[2:], cli.DefaultIO()))

	case "graph":
		os.Exit(cli.RunGraph(os.Args[2:], cli.DefaultIO()))

	case "blast":
		os.Exit(cli.RunBlast(os.Args[2:], cli.DefaultIO()))

	case "conventions":
		os.Exit(cli.RunConventions(os.Args[2:], cli.DefaultIO()))

	case "status":
		os.Exit(cli.RunStatus(os.Args[2:], cli.DefaultIO()))

	case "doctor":
		os.Exit(cli.RunDoctor(os.Args[2:], cli.DefaultIO()))

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

	default:
		fmt.Fprintf(os.Stderr, "sense: unknown command %q. Run 'sense help'.\n", cmd)
		os.Exit(1)
	}
}
