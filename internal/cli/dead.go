package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/luuuc/sense/internal/dead"
	"github.com/luuuc/sense/internal/mcpio"
)

const deadHelp = `usage: sense dead [flags]

Find dead code: symbols with no incoming references.

Flags:
  --language LANG    Filter by language (e.g. "go", "ruby")
  --domain PATH      Filter by path substring (e.g. "services", "models")
  --limit N          Maximum symbols to report (default 100)
  --json             Emit JSON matching the sense.graph dead_code MCP schema
  -h, --help         Show this help

Examples:
  sense dead
  sense dead --language go
  sense dead --domain services --json
  sense dead --limit 50

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
  4  index corrupt
`

type deadOptions struct {
	Language string
	Domain   string
	Limit    int
	JSON     bool
}

func RunDead(args []string, cio IO) int {
	opts, err := parseDeadArgs(args, cio.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}

	ctx := context.Background()

	adapter, err := OpenIndex(ctx, cio.Dir)
	if err != nil {
		return handleIndexOpenError(cio.Stderr, err)
	}
	defer func() { _ = adapter.Close() }()

	result, err := dead.FindDead(ctx, adapter.DB(), dead.Options{
		Language: opts.Language,
		Domain:   opts.Domain,
		Limit:    opts.Limit,
	})
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense dead:", err)
		return ExitGeneralError
	}

	rolled := dead.Rollup(result.Dead)

	if opts.JSON {
		resp := mcpio.BuildDeadCodeResponse(rolled, result.TotalSymbols)
		out, merr := mcpio.MarshalDeadCode(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense dead:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}

	RenderDeadHuman(cio.Stdout, rolled, result.TotalSymbols)
	return ExitSuccess
}

func RenderDeadHuman(w io.Writer, symbols []dead.Symbol, totalSymbols int) {
	if len(symbols) == 0 {
		_, _ = fmt.Fprintln(w, "No dead code found.")
		return
	}

	_, _ = fmt.Fprintf(w, "Dead code: %d symbols with no incoming references\n\n", len(symbols))

	var currentFile string
	for _, s := range symbols {
		if s.File != currentFile {
			if currentFile != "" {
				_, _ = fmt.Fprintln(w)
			}
			currentFile = s.File
			_, _ = fmt.Fprintln(w, s.File)
		}
		_, _ = fmt.Fprintf(w, "  %s (%s, lines %d-%d)\n", s.Qualified, s.Kind, s.LineStart, s.LineEnd)
	}

	pct := float64(len(symbols)) * 100 / float64(totalSymbols)
	_, _ = fmt.Fprintf(w, "\n%d dead symbols out of %d total (%.1f%%)\n", len(symbols), totalSymbols, pct)
}

func parseDeadArgs(args []string, stderr io.Writer) (deadOptions, error) {
	fs := flag.NewFlagSet("sense dead", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, deadHelp) }

	var opts deadOptions
	fs.StringVar(&opts.Language, "language", "", "filter by language")
	fs.StringVar(&opts.Domain, "domain", "", "filter by path substring")
	fs.IntVar(&opts.Limit, "limit", 100, "maximum symbols to report")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON")

	if err := fs.Parse(args); err != nil {
		return deadOptions{}, err
	}

	return opts, nil
}
