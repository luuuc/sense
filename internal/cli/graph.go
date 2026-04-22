package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/luuuc/sense/internal/mcpio"
)

// graphHelp mirrors the `sense graph` example block in
// .doc/definition/06-mcp-and-cli.md. Kept in one string constant so
// --help output and the pitch's acceptance criterion ("--help text
// matches 06-mcp-and-cli.md examples") have a single source of truth.
const graphHelp = `usage: sense graph <symbol> [flags]

Print a symbol's relationships — inheritance, calls, callers, tests.

Flags:
  --depth N                 Traversal depth around the subject (default 1)
  --direction DIR           One of: both, callers, callees (default both)
  --file PATH               Disambiguate by file path substring
  --language LANG           Disambiguate by language (e.g. "ruby", "go")
  --json                    Emit JSON matching the sense.graph MCP schema
  --no-color                Disable ANSI color (NO_COLOR env var is also respected)
  -h, --help                Show this help

Examples:
  sense graph "CheckoutService"
  sense graph "User#email_verified?" --depth 2
  sense graph "OrdersController#create" --direction callers
  sense graph "Project" --language ruby
  sense graph "CheckoutService" --json | jq '.edges.calls[].symbol'

Exit codes:
  0  success
  1  general error
  2  symbol not found or ambiguous
  3  index missing (run 'sense scan' first)
  4  index corrupt (rebuild via 'rm .sense/index.db && sense scan';
     'sense scan --force' lands in pitch 01-06)
`

// GraphDirection names the traversal direction flag values. Exported
// so the MCP wrapper in 01-05 can reuse the same vocabulary on the
// wire.
type GraphDirection string

const (
	DirectionBoth    GraphDirection = "both"
	DirectionCallers GraphDirection = "callers"
	DirectionCallees GraphDirection = "callees"
)

// graphOptions is the parsed flag shape for the graph subcommand.
// Extracted from RunGraph so the skeleton, the fuller implementation
// in card 5, and tests all share one struct.
type graphOptions struct {
	Symbol    string
	Depth     int
	Direction GraphDirection
	File      string
	Language  string
	JSON      bool
	NoColor   bool
}

// RunGraph is the entrypoint for `sense graph`. Parses args, opens
// the index, resolves the symbol, loads its context, and renders
// either human text (default) or MCP-schema JSON (`--json`).
//
// Exit codes follow .doc/definition/06-mcp-and-cli.md: 2 for
// not-found / ambiguous, 3 for a missing index, 4 for a corrupt
// index, 1 for any other failure, 0 for success.
func RunGraph(args []string, cio IO) int {
	opts, err := parseGraphArgs(args, cio.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}
	if opts.Depth != 1 {
		_, _ = fmt.Fprintln(cio.Stderr, "sense graph: --depth > 1 is not yet supported")
		return ExitGeneralError
	}

	// context.Background() is intentional: cancellation lands with
	// the MCP server in 01-05, not the first-release CLI.
	ctx := context.Background()
	adapter, err := OpenIndex(ctx, cio.Dir)
	if err != nil {
		return handleIndexOpenError(cio.Stderr, err)
	}
	defer func() { _ = adapter.Close() }()

	matches, err := Lookup(ctx, adapter.DB(), opts.Symbol)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense graph:", err)
		return ExitGeneralError
	}
	matches = filterMatches(matches, opts.File, opts.Language)
	switch len(matches) {
	case 0:
		PrintNotFound(cio.Stderr, opts.Symbol)
		return ExitSymbolIssue
	case 1:
		// resolved
	default:
		PrintDisambiguation(cio.Stderr, opts.Symbol, "sense graph", matches)
		return ExitSymbolIssue
	}

	sc, err := adapter.ReadSymbol(ctx, matches[0].ID)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense graph:", err)
		return ExitGeneralError
	}

	fileIDs := CollectFileIDs(sc)
	pathByID, err := LoadFilePaths(ctx, adapter.DB(), fileIDs)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense graph:", err)
		return ExitGeneralError
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	resp := mcpio.BuildGraphResponse(sc, lookup, mcpio.BuildGraphRequest{
		Direction: string(opts.Direction),
	})

	if opts.JSON {
		out, merr := mcpio.MarshalGraph(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense graph:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}
	RenderGraphHuman(cio.Stdout, resp)
	return ExitSuccess
}

// handleIndexOpenError maps OpenIndex's sentinels to the right exit
// code and hint. Missing → 3 ("run sense scan"); corrupt → 4 ("run
// sense scan --force"); anything else → 1.
func handleIndexOpenError(stderr io.Writer, err error) int {
	switch {
	case errors.Is(err, ErrIndexMissing):
		_, _ = fmt.Fprintln(stderr, "sense: no index found. Run 'sense scan' to build one.")
		return ExitIndexMissing
	case errors.Is(err, ErrIndexCorrupt):
		_, _ = fmt.Fprintln(stderr, "sense: index corrupt. Rebuild with 'rm .sense/index.db && sense scan'.")
		return ExitIndexCorrupt
	default:
		_, _ = fmt.Fprintln(stderr, "sense:", err)
		return ExitGeneralError
	}
}

// parseGraphArgs returns the parsed flag struct or an error. The flag
// package writes usage to stderr and returns flag.ErrHelp when `-h` /
// `--help` is passed — RunGraph translates that to ExitSuccess.
func parseGraphArgs(args []string, stderr io.Writer) (graphOptions, error) {
	fs := flag.NewFlagSet("sense graph", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, graphHelp) }

	var opts graphOptions
	var direction string
	fs.IntVar(&opts.Depth, "depth", 1, "traversal depth around the subject")
	fs.StringVar(&direction, "direction", string(DirectionBoth), "both|callers|callees")
	fs.StringVar(&opts.File, "file", "", "disambiguate by file path substring")
	fs.StringVar(&opts.Language, "language", "", "disambiguate by language")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON matching the sense.graph MCP schema")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable ANSI color")

	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return graphOptions{}, err
	}

	if len(positional) < 1 {
		_, _ = fmt.Fprintln(stderr, "sense graph: missing symbol argument")
		_, _ = fmt.Fprintln(stderr, "run 'sense graph --help' for usage")
		return graphOptions{}, fmt.Errorf("missing symbol argument")
	}
	opts.Symbol = positional[0]

	switch GraphDirection(direction) {
	case DirectionBoth, DirectionCallers, DirectionCallees:
		opts.Direction = GraphDirection(direction)
	default:
		_, _ = fmt.Fprintf(stderr, "sense graph: --direction must be one of both, callers, callees (got %q)\n", direction)
		_, _ = fmt.Fprintln(stderr, "run 'sense graph --help' for usage")
		return graphOptions{}, fmt.Errorf("invalid --direction value: %q", direction)
	}

	return opts, nil
}
