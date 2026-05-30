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

Find unreferenced symbols: those with no incoming references, split into
the rare provably-dead (safe to remove) and possibly-dead (verify first).

Flags:
  --language LANG    Filter by language (e.g. "go", "ruby")
  --domain PATH      Filter by path substring (e.g. "services", "models")
  --limit N          Maximum symbols to report (default 100)
  --json             Emit JSON matching the sense_graph dead_code MCP schema
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

	resp := mcpio.BuildUnreferencedResponse(result.Findings, result.TotalSymbols, opts.Limit)

	if opts.JSON {
		out, merr := mcpio.MarshalUnreferenced(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense dead:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}

	RenderUnreferencedHuman(cio.Stdout, resp)
	return ExitSuccess
}

// RenderUnreferencedHuman prints the honest-verdict output for a human
// reader: the earned `dead` list first (safe to remove), then the
// `possibly_dead` groups by reason (verify before removing), each with its
// recipe. It mirrors the wire shape so the CLI and MCP surfaces tell the
// same story.
func RenderUnreferencedHuman(w io.Writer, resp mcpio.UnreferencedResponse) {
	u := resp.Unreferenced
	if len(u.Dead) == 0 && len(u.PossiblyDead) == 0 {
		_, _ = fmt.Fprintln(w, "No unreferenced symbols found.")
		return
	}

	if len(u.Dead) > 0 {
		_, _ = fmt.Fprintf(w, "Dead (%d) — no references and safe to remove:\n", resp.DeadCount)
		for _, d := range u.Dead {
			_, _ = fmt.Fprintf(w, "  %s (%s) %s:%d\n", d.Qualified, d.Kind, d.File, d.Line)
			_, _ = fmt.Fprintf(w, "    verify: %s\n", d.Verify)
		}
		_, _ = fmt.Fprintln(w)
	}

	if len(u.PossiblyDead) > 0 {
		_, _ = fmt.Fprintf(w, "Possibly dead (%d) — unreferenced, but a hidden caller may exist:\n", resp.PossiblyDeadCount)
		for _, g := range u.PossiblyDead {
			_, _ = fmt.Fprintf(w, "  [%s] %s\n", g.Reason.Code, g.Reason.Hint)
			for _, s := range g.Symbols {
				_, _ = fmt.Fprintf(w, "    %s (%s) %s:%d\n", s.Qualified, s.Kind, s.File, s.Line)
			}
			if g.Dropped > 0 {
				_, _ = fmt.Fprintf(w, "    … and %d more (raise --limit to see them)\n", g.Dropped)
			}
			_, _ = fmt.Fprintf(w, "    verify: %s\n", g.Verify)
		}
		_, _ = fmt.Fprintln(w)
	}

	_, _ = fmt.Fprintf(w, "%d dead, %d possibly dead, out of %d total symbols\n",
		resp.DeadCount, resp.PossiblyDeadCount, resp.TotalSymbols)
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
