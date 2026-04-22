package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/search"
)

const searchHelp = `usage: sense search <query> [flags]

Hybrid semantic + keyword search across all indexed symbols.

Flags:
  --limit N                 Maximum results (default 10)
  --language LANG           Filter by language (e.g. "ruby", "go")
  --min-score F             Minimum score threshold 0.0–1.0 (default 0.0)
  --json                    Emit JSON matching the sense.search MCP schema
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

	var vectorIdx search.VectorIndex
	var embedder embed.Embedder

	if EmbeddingsEnabled(cio.Dir) {
		hnswPath := filepath.Join(cio.Dir, ".sense", "hnsw.bin")
		idx, loadErr := search.LoadHNSWIndex(hnswPath)
		if loadErr == nil && idx != nil {
			vectorIdx = idx
		} else {
			embeddings, err := adapter.LoadEmbeddings(ctx)
			if err != nil {
				_, _ = fmt.Fprintln(cio.Stderr, "sense search:", err)
				return ExitGeneralError
			}
			if len(embeddings) > 0 {
				vectorIdx = search.BuildHNSWIndex(embeddings)
			}
		}
		if vectorIdx != nil && vectorIdx.Len() > 0 {
			embedder, err = embed.NewBundledEmbedder()
			if err != nil {
				_, _ = fmt.Fprintln(cio.Stderr, "sense search:", err)
				return ExitGeneralError
			}
			defer func() { _ = embedder.Close() }()
		}
	}

	engine := search.NewEngine(adapter, vectorIdx, embedder)
	results, symbolCount, err := engine.Search(ctx, search.Options{
		Query:    opts.Query,
		Limit:    opts.Limit,
		Language: opts.Language,
		MinScore: opts.MinScore,
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

	resp := buildSearchResponse(results, pathByID, symbolCount)

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

func buildSearchResponse(results []search.Result, pathByID map[int64]string, symbolCount int) mcpio.SearchResponse {
	entries := make([]mcpio.SearchResultEntry, len(results))
	uniqueFiles := map[string]struct{}{}
	for i, r := range results {
		path := pathByID[r.FileID]
		entries[i] = mcpio.SearchResultEntry{
			Symbol:  r.Qualified,
			File:    path,
			Line:    r.LineStart,
			Kind:    r.Kind,
			Score:   mcpio.SearchScore(r.Score),
			Snippet: r.Snippet,
		}
		if path != "" {
			uniqueFiles[path] = struct{}{}
		}
	}
	filesAvoided := len(uniqueFiles)
	return mcpio.SearchResponse{
		Results: entries,
		SenseMetrics: mcpio.SearchMetrics{
			SymbolsSearched:           symbolCount,
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
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON matching the sense.search MCP schema")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable ANSI color")

	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return searchOptions{}, err
	}

	if len(positional) < 1 {
		_, _ = fmt.Fprintln(stderr, "sense search: missing query argument")
		_, _ = fmt.Fprintln(stderr, "run 'sense search --help' for usage")
		return searchOptions{}, fmt.Errorf("missing query argument")
	}
	opts.Query = positional[0]

	return opts, nil
}
