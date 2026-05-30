package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/metrics"
	"github.com/luuuc/sense/internal/search"
)

const searchHelp = `usage: sense search <query> [flags]

Hybrid semantic + keyword search across all indexed symbols.

Flags:
  --limit N                 Maximum results (default 10)
  --language LANG           Filter by language (e.g. "ruby", "go")
  --min-score F             Minimum score threshold 0.0–1.0 (default 0.0)
  --mode MODE               Ranking mode: hybrid (default), semantic, keyword.
                            hybrid auto-detects query shape; semantic forces
                            concept ranking; keyword forces literal ranking.
  --json                    Emit JSON matching the sense_search MCP schema
  --no-color                Disable ANSI color (NO_COLOR env var is also respected)
  -h, --help                Show this help

Examples:
  sense search "how does auth work"
  sense search "payment error handling" --limit 5
  sense search "database migration" --language ruby
  sense search "auth flow" --json

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
  4  index corrupt (rebuild via 'rm .sense/index.db && sense scan')
`

type searchOptions struct {
	Query    string
	Limit    int
	Language string
	MinScore float64
	Mode     string
	JSON     bool
	NoColor  bool
}

func RunSearch(args []string, cio IO) int {
	opts, err := parseSearchArgs(args, cio.Stderr)
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

	engine, embedder, err := search.BuildEngine(ctx, adapter, cio.Dir)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense search:", err)
		return ExitGeneralError
	}
	if embedder != nil {
		defer func() { _ = embedder.Close() }()
	}

	results, meta, err := engine.Search(ctx, search.Options{
		Query:    opts.Query,
		Limit:    opts.Limit,
		Language: opts.Language,
		MinScore: opts.MinScore,
		Mode:     opts.Mode,
	})
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense search:", err)
		return ExitGeneralError
	}

	fileIDs := collectSearchFileIDs(results)
	pathByID, err := LoadFilePaths(ctx, adapter.DB(), fileIDs)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense search:", err)
		return ExitGeneralError
	}

	resp := buildSearchResponse(results, pathByID, meta)

	tracker := metrics.NewTracker(adapter.DB())
	defer tracker.Close()

	tf := search.NewTextFallback()
	if tf.Available() && len(resp.Results) < opts.Limit {
		textResults := tf.Search(ctx, opts.Query, cio.Dir, []string{"."}, opts.Limit)
		matches := make([]mcpio.TextMatch, len(textResults))
		for i, tr := range textResults {
			matches[i] = mcpio.TextMatch{File: tr.File, Line: tr.Line, Match: tr.Match}
		}
		textEntries, fired := mcpio.ConvertTextResults(matches, resp.Results)
		if fired {
			resp.Results = append(resp.Results, textEntries...)
			resp.SearchMode += "+text"
			resp.SenseMetrics.TextFallbackFired = true
		}
	}

	tracker.Record("sense_search", opts.Query,
		resp.SenseMetrics.EstimatedFileReadsAvoided, resp.SenseMetrics.EstimatedTokensSaved,
		resp.SenseMetrics.TextFallbackFired)

	if opts.JSON {
		out, merr := mcpio.MarshalSearch(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense search:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}
	RenderSearchHuman(cio.Stdout, resp)
	return ExitSuccess
}

func collectSearchFileIDs(results []search.Result) []int64 {
	seen := map[int64]struct{}{}
	var ids []int64
	for _, r := range results {
		if _, ok := seen[r.FileID]; !ok {
			seen[r.FileID] = struct{}{}
			ids = append(ids, r.FileID)
		}
	}
	return ids
}

func buildSearchResponse(results []search.Result, pathByID map[int64]string, meta search.SearchMeta) mcpio.SearchResponse {
	entries := make([]mcpio.SearchResultEntry, len(results))
	uniqueFiles := map[string]struct{}{}
	for i, r := range results {
		path := pathByID[r.FileID]
		entries[i] = mcpio.SearchResultEntry{
			Symbol:     r.Qualified,
			File:       path,
			Line:       r.LineStart,
			Kind:       r.Kind,
			Score:      mcpio.SearchScore(r.Score),
			Snippet:    r.Snippet,
			References: r.References,
			Source:     r.Source,
		}
		if path != "" {
			uniqueFiles[path] = struct{}{}
		}
	}
	filesAvoided := len(uniqueFiles)
	return mcpio.SearchResponse{
		Results:    entries,
		SearchMode: meta.Mode,
		FusionWeights: mcpio.FusionWeights{
			Keyword: meta.KeywordWeight,
			Vector:  meta.VectorWeight,
		},
		SenseMetrics: mcpio.SearchMetrics{
			SymbolsSearched:           meta.SymbolCount,
			EstimatedFileReadsAvoided: filesAvoided,
			EstimatedTokensSaved:      filesAvoided * mcpio.AvgTokensPerFile,
		},
	}
}

func parseSearchArgs(args []string, stderr io.Writer) (searchOptions, error) {
	fs := flag.NewFlagSet("sense search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, searchHelp) }

	var opts searchOptions
	fs.IntVar(&opts.Limit, "limit", 10, "maximum results")
	fs.StringVar(&opts.Language, "language", "", "filter by language")
	fs.Float64Var(&opts.MinScore, "min-score", 0.0, "minimum score threshold")
	fs.StringVar(&opts.Mode, "mode", search.ModeHybrid, "ranking mode: hybrid, semantic, or keyword")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON matching the sense_search MCP schema")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable ANSI color")

	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return searchOptions{}, err
	}

	switch opts.Mode {
	case search.ModeHybrid, search.ModeSemantic, search.ModeKeyword:
		// valid
	default:
		_, _ = fmt.Fprintf(stderr, "sense search: invalid --mode %q (want hybrid, semantic, or keyword)\n", opts.Mode)
		return searchOptions{}, fmt.Errorf("invalid mode: %s", opts.Mode)
	}

	if len(positional) < 1 {
		_, _ = fmt.Fprintln(stderr, "sense search: missing query argument")
		_, _ = fmt.Fprintln(stderr, "run 'sense search --help' for usage")
		return searchOptions{}, fmt.Errorf("missing query argument")
	}
	opts.Query = positional[0]

	return opts, nil
}
