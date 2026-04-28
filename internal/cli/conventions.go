package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/luuuc/sense/internal/conventions"
	"github.com/luuuc/sense/internal/mcpio"
)

const conventionsHelp = `usage: sense conventions [flags]

Detect and display project conventions from the indexed graph.

Flags:
  --domain DOMAIN           Scope to files matching DOMAIN (e.g. "models", "controllers")
  --min-strength F          Minimum strength threshold 0.0–1.0 (default 0.5)
  --json                    Emit JSON matching the sense.conventions MCP schema
  --no-color                Disable ANSI color (NO_COLOR env var is also respected)
  -h, --help                Show this help

Examples:
  sense conventions
  sense conventions --domain models
  sense conventions --domain controllers
  sense conventions --min-strength 0.8
  sense conventions --json

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
  4  index corrupt (rebuild via 'rm .sense/index.db && sense scan')
`

type conventionsOptions struct {
	Domain      string
	MinStrength float64
	JSON        bool
	NoColor     bool
}

func RunConventions(args []string, cio IO) int {
	opts, err := parseConventionsArgs(args, cio.Stderr)
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

	results, symbolCount, err := conventions.Detect(ctx, adapter.DB(), conventions.Options{
		Domain:      opts.Domain,
		MinStrength: opts.MinStrength,
	})
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense conventions: %v\n", err)
		return ExitGeneralError
	}

	if opts.JSON {
		filesAvoided := min(symbolCount/5, 30)
		resp := mcpio.ConventionsResponse{
			Conventions: make([]mcpio.ConventionEntry, len(results)),
			SenseMetrics: mcpio.ConventionsMetrics{
				SymbolsAnalyzed:           symbolCount,
				EstimatedFileReadsAvoided: filesAvoided,
				EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
			},
		}
		for i, c := range results {
			resp.Conventions[i] = mcpio.ConventionEntry{
				Category:       string(c.Category),
				Description:    c.Description,
				Strength:       mcpio.Confidence(c.Strength),
				Instances:      conventions.PickRepresentatives(c.Examples, 3),
				TotalInstances: c.Instances,
			}
		}
		mcpio.BuildConventionsSummary(&resp)
		out, err := mcpio.MarshalConventions(resp)
		if err != nil {
			_, _ = fmt.Fprintf(cio.Stderr, "sense conventions: %v\n", err)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
	} else {
		RenderConventionsHuman(cio.Stdout, results)
	}

	return ExitSuccess
}

func RenderConventionsHuman(w io.Writer, conventions []conventions.Convention) {
	if len(conventions) == 0 {
		_, _ = fmt.Fprintln(w, "no conventions detected")
		return
	}

	currentCategory := conventions[0].Category
	_, _ = fmt.Fprintf(w, "%s\n", currentCategory)
	for _, c := range conventions {
		if c.Category != currentCategory {
			currentCategory = c.Category
			_, _ = fmt.Fprintf(w, "\n%s\n", currentCategory)
		}
		_, _ = fmt.Fprintf(w, "  %s  (%.0f%%, %d/%d)\n", c.Description, c.Strength*100, c.Instances, c.Total)
	}
}

func parseConventionsArgs(args []string, stderr io.Writer) (conventionsOptions, error) {
	fs := flag.NewFlagSet("sense conventions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, conventionsHelp) }

	var opts conventionsOptions
	fs.StringVar(&opts.Domain, "domain", "", "")
	fs.Float64Var(&opts.MinStrength, "min-strength", 0.0, "")
	fs.BoolVar(&opts.JSON, "json", false, "")
	fs.BoolVar(&opts.NoColor, "no-color", false, "")

	if _, err := parseInterleaved(fs, args); err != nil {
		return opts, err
	}
	return opts, nil
}
