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
  benchmark     Run performance benchmarks on the index
  doctor        Diagnose common index problems
  hook          Claude Code lifecycle hooks (pre-tool-use, pre-compact, etc.)
  mcp           Start the MCP server (stdio transport)
  update        Check for and install the latest version
  version       Print version
  help          Show this help

Run 'sense <command> --help' for per-command usage and exit codes.
`

// osExit indirects os.Exit so the one-line main wrapper is testable without
// terminating the test process. Production behaviour is unchanged: it is
// os.Exit. A test swaps it to capture the code main passes through.
var osExit = os.Exit

// main is the binary's shell: it owns the process-global edges (os.Args, the
// real stdio streams, the exit code) and does nothing else. All dispatch logic
// lives in run, which takes its inputs explicitly so it can be unit-tested.
func main() {
	osExit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the functional core of the CLI: it dispatches args[0] to the matching
// subcommand and returns the process exit code (0 success, 1 general error, 2
// usage/symbol issue, 3 missing index, 4 corrupt index — the table in
// .doc/definition/06-mcp-and-cli.md). It takes its streams as parameters rather
// than reaching for os.Stdin/Stdout/Stderr, and resolves the project root from
// "." (the cwd, the same default scan and hook already use) rather than
// os.Getwd, so a test drives it with buffers and a temp working directory.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		_, _ = fmt.Fprint(stderr, helpText)
		return 2
	}

	cmd := args[0]
	rest := args[1:]
	cio := cli.IO{Stdout: stdout, Stderr: stderr, Dir: "."}

	switch cmd {
	case "version", "--version", "-v":
		_, _ = fmt.Fprintf(stdout, "sense %s (schema v%d, embeddings: %s)\n",
			version.Version, sqlite.SchemaVersion, embed.ModelID)
		return 0

	case "help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, helpText)
		return 0

	case "scan":
		return runScan(rest, stderr)

	case "setup":
		return cli.RunSetup(rest, cio)

	case "search":
		return cli.RunSearch(rest, cio)

	case "graph":
		return cli.RunGraph(rest, cio)

	case "blast":
		return cli.RunBlast(rest, cio)

	case "dead":
		return cli.RunDead(rest, cio)

	case "conventions":
		return cli.RunConventions(rest, cio)

	case "status":
		return cli.RunStatus(rest, cio)

	case "benchmark":
		return cli.RunBenchmark(rest, cio)

	case "doctor":
		return cli.RunDoctor(rest, cio)

	case "hook":
		if len(rest) < 1 {
			_, _ = fmt.Fprintln(stderr, "usage: sense hook <pre-tool-use|pre-compact|subagent-start|session-start>")
			return 1
		}
		return hook.Run(rest[0], ".", stdin, stdout)

	case "mcp":
		fs := flag.NewFlagSet("sense mcp", flag.ContinueOnError)
		fs.SetOutput(stderr)
		dir := fs.String("dir", ".", "project root containing .sense/")
		if err := fs.Parse(rest); err != nil {
			return 1
		}
		if err := mcpserver.Run(*dir); err != nil {
			_, _ = fmt.Fprintln(stderr, "sense mcp:", err)
			return 1
		}
		return 0

	case "update":
		return versioncheck.Update(stdout, stderr)

	default:
		_, _ = fmt.Fprintf(stderr, "sense: unknown command %q. Run 'sense help'.\n", cmd)
		return 1
	}
}

// runScan handles `sense scan`, including the --watch and profiling flags. Split
// from run so the CPU-profile lifecycle scopes to one function: the profile is
// stopped and flushed only on the success path, matching the prior behaviour
// where an error exit skipped the deferred StopCPUProfile (an incomplete run
// writes no profile).
func runScan(args []string, stderr io.Writer) int {
	ctx := context.Background()

	fs := flag.NewFlagSet("sense scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	watchFlag := fs.Bool("watch", false, "keep running and re-index on file changes")
	rebuildFlag := fs.Bool("rebuild", false, "drop and rebuild the index from source (preserves lifetime metrics)")
	embedFlag := fs.Bool("embed", false, "block until embeddings complete (default: defer to MCP server)")
	quietFlag := fs.Bool("quiet", false, "suppress warnings")
	dir := fs.String("dir", ".", "project root")
	cpuprofile := fs.String("cpuprofile", "", "write CPU profile to file")
	memprofile := fs.String("memprofile", "", "write heap profile to file on exit")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	// stopProfile is non-nil only when CPU profiling is active. It is invoked
	// explicitly on the success path (never via defer) so an error return leaves
	// the profile unwritten, as the os.Exit-based shell did before.
	var stopProfile func()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "sense scan: create cpuprofile:", err)
			return 1
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			_, _ = fmt.Fprintln(stderr, "sense scan: start cpuprofile:", err)
			_ = f.Close()
			return 1
		}
		stopProfile = func() { pprof.StopCPUProfile(); _ = f.Close() }
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
			_, _ = fmt.Fprintln(stderr, "sense scan --watch:", err)
			return 1
		}
	} else {
		if _, err := scan.Run(ctx, scan.Options{
			Root:              *dir,
			Warnings:          warnSink,
			Quiet:             *quietFlag,
			EmbeddingsEnabled: cli.EmbeddingsEnabled(*dir),
			Embed:             *embedFlag,
			Rebuild:           *rebuildFlag,
		}); err != nil {
			_, _ = fmt.Fprintln(stderr, "sense scan:", err)
			return 1
		}
	}

	if *memprofile != "" {
		f, err := os.Create(*memprofile)
		if err != nil {
			_, _ = fmt.Fprintln(stderr, "sense scan: create memprofile:", err)
			return 1
		}
		if err := pprof.WriteHeapProfile(f); err != nil {
			_, _ = fmt.Fprintln(stderr, "sense scan: write memprofile:", err)
		}
		_ = f.Close()
	}

	if stopProfile != nil {
		stopProfile()
	}
	return 0
}
