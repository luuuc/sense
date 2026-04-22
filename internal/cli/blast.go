package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/luuuc/sense/internal/blast"
	"github.com/luuuc/sense/internal/mcpio"
)

// blastHelp mirrors the `sense blast` example block in
// .doc/definition/06-mcp-and-cli.md — the acceptance criterion for
// card 1 is that --help lists the two invocation forms (symbol / diff)
// and the documented flag set.
const blastHelp = `usage: sense blast <symbol> [flags]
       sense blast --diff <ref> [flags]

Compute blast radius: the set of symbols affected if <symbol> changes,
or the union radius for every symbol touched by <ref>.

Flags:
  --max-hops N              Traversal depth (default 3)
  --min-confidence F        Edge-confidence threshold 0.0–1.0 (default 0.7)
  --include-tests           Include affected test files (default true)
  --diff REF                Compute blast for symbols modified in git diff REF
  --file PATH               Disambiguate by file path substring
  --language LANG           Disambiguate by language (e.g. "ruby", "go")
  --json                    Emit JSON matching the sense.blast MCP schema
  --no-color                Disable ANSI color (NO_COLOR env var is also respected)
  -h, --help                Show this help

Examples:
  sense blast "User#email_verified?"
  sense blast "CheckoutService" --max-hops 3
  sense blast "Project" --language ruby
  sense blast --diff HEAD~1
  sense blast --diff main..feature-branch
  sense blast --diff HEAD~1 --json

Exit codes:
  0  success
  1  general error
  2  symbol not found or ambiguous
  3  index missing (run 'sense scan' first)
  4  index corrupt (rebuild via 'rm .sense/index.db && sense scan';
     'sense scan --force' lands in pitch 01-06)
`

// blastOptions is the parsed flag shape for the blast subcommand.
// Either Symbol or Diff is set; parseBlastArgs rejects calls that set
// both or neither.
type blastOptions struct {
	Symbol        string
	Diff          string
	MaxHops       int
	MinConfidence float64
	IncludeTests  bool
	File          string
	Language      string
	JSON          bool
	NoColor       bool
}

// RunBlast is the entrypoint for `sense blast`. Dispatches to the
// symbol form (this card) or the --diff form (card 7). Exit codes
// mirror RunGraph's table: 2 for symbol issues, 3 for missing
// index, 4 for corrupt index, 1 for anything else, 0 for success.
func RunBlast(args []string, cio IO) int {
	opts, err := parseBlastArgs(args, cio.Stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}

	if opts.Diff != "" {
		return runBlastDiff(cio, opts)
	}
	return runBlastSymbol(cio, opts)
}

// runBlastDiff executes the diff form: git diff --name-only <ref>
// → file→symbol resolution → blast.Compute per modified symbol →
// union blast radius → render/marshal.
//
// Limitation called out in the pitch: this is approximate blast
// radius — the query runs against the current index (what Sense
// knows *now*) while the diff describes a historical change. A
// symbol deleted in HEAD~1 has no current row to blast from; a
// symbol renamed has its old path missing from the index. True
// multi-branch awareness is out of scope for this release.
func runBlastDiff(cio IO, opts blastOptions) int {
	// context.Background() is intentional for this release; see
	// RunGraph for the cancellation-deferred-to-01-05 rationale.
	ctx := context.Background()

	paths, err := GitDiffFiles(ctx, cio.Dir, opts.Diff)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
		return ExitGeneralError
	}

	adapter, err := OpenIndex(ctx, cio.Dir)
	if err != nil {
		return handleIndexOpenError(cio.Stderr, err)
	}
	defer func() { _ = adapter.Close() }()

	symbolIDs, err := SymbolsInFiles(ctx, adapter.DB(), paths)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
		return ExitGeneralError
	}

	results := make([]blast.Result, 0, len(symbolIDs))
	for _, sid := range symbolIDs {
		r, cerr := blast.Compute(ctx, adapter.DB(), sid, blast.Options{
			MaxHops:       opts.MaxHops,
			MinConfidence: opts.MinConfidence,
			IncludeTests:  opts.IncludeTests,
		})
		if cerr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", cerr)
			return ExitGeneralError
		}
		results = append(results, r)
	}

	fileIDs := CollectDiffFileIDs(results)
	pathByID, err := LoadFilePaths(ctx, adapter.DB(), fileIDs)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
		return ExitGeneralError
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	resp := mcpio.BuildDiffBlastResponse(opts.Diff, results, lookup)

	if opts.JSON {
		out, merr := mcpio.MarshalBlast(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}
	RenderBlastHuman(cio.Stdout, resp)
	return ExitSuccess
}

// collectDiffFileIDs returns unique file ids referenced across many
// blast.Result records — shared prologue for the diff path's single
// file-path hydration call.
func CollectDiffFileIDs(results []blast.Result) []int64 {
	seen := map[int64]struct{}{}
	var ids []int64
	note := func(fileID int64) {
		if _, ok := seen[fileID]; !ok {
			seen[fileID] = struct{}{}
			ids = append(ids, fileID)
		}
	}
	for _, r := range results {
		note(r.Symbol.FileID)
		for _, c := range r.DirectCallers {
			note(c.FileID)
		}
		for _, hop := range r.IndirectCallers {
			note(hop.Symbol.FileID)
		}
	}
	return ids
}

// runBlastSymbol executes the symbol form: open index → lookup →
// blast.Compute → build response → render/marshal. Split from
// RunBlast so card 7's --diff path stays readable alongside.
func runBlastSymbol(cio IO, opts blastOptions) int {
	// context.Background() is intentional for this release; see
	// RunGraph for the cancellation-deferred-to-01-05 rationale.
	ctx := context.Background()

	adapter, err := OpenIndex(ctx, cio.Dir)
	if err != nil {
		return handleIndexOpenError(cio.Stderr, err)
	}
	defer func() { _ = adapter.Close() }()

	matches, err := Lookup(ctx, adapter.DB(), opts.Symbol)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
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
		PrintDisambiguation(cio.Stderr, opts.Symbol, "sense blast", matches)
		return ExitSymbolIssue
	}

	result, err := blast.Compute(ctx, adapter.DB(), matches[0].ID, blast.Options{
		MaxHops:       opts.MaxHops,
		MinConfidence: opts.MinConfidence,
		IncludeTests:  opts.IncludeTests,
	})
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
		return ExitGeneralError
	}

	fileIDs := CollectBlastFileIDs(result)
	pathByID, err := LoadFilePaths(ctx, adapter.DB(), fileIDs)
	if err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", err)
		return ExitGeneralError
	}
	lookup := func(id int64) (string, bool) {
		p, ok := pathByID[id]
		return p, ok
	}

	resp := mcpio.BuildBlastResponse(result, lookup)

	if opts.JSON {
		out, merr := mcpio.MarshalBlast(resp)
		if merr != nil {
			_, _ = fmt.Fprintln(cio.Stderr, "sense blast:", merr)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
		return ExitSuccess
	}
	RenderBlastHuman(cio.Stdout, resp)
	return ExitSuccess
}

// collectBlastFileIDs returns the unique file ids referenced by the
// blast Result's direct + indirect callers. Batched so the caller
// hydrates paths in one query.
func CollectBlastFileIDs(r blast.Result) []int64 {
	seen := map[int64]struct{}{r.Symbol.FileID: {}}
	ids := []int64{r.Symbol.FileID}
	for _, c := range r.DirectCallers {
		if _, ok := seen[c.FileID]; !ok {
			seen[c.FileID] = struct{}{}
			ids = append(ids, c.FileID)
		}
	}
	for _, hop := range r.IndirectCallers {
		if _, ok := seen[hop.Symbol.FileID]; !ok {
			seen[hop.Symbol.FileID] = struct{}{}
			ids = append(ids, hop.Symbol.FileID)
		}
	}
	return ids
}

func parseBlastArgs(args []string, stderr io.Writer) (blastOptions, error) {
	fs := flag.NewFlagSet("sense blast", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, blastHelp) }

	var opts blastOptions
	fs.IntVar(&opts.MaxHops, "max-hops", 3, "traversal depth")
	fs.Float64Var(&opts.MinConfidence, "min-confidence", 0.7, "edge-confidence threshold 0.0–1.0")
	fs.BoolVar(&opts.IncludeTests, "include-tests", true, "include affected test files")
	fs.StringVar(&opts.Diff, "diff", "", "compute blast for symbols modified in git diff REF")
	fs.StringVar(&opts.File, "file", "", "disambiguate by file path substring")
	fs.StringVar(&opts.Language, "language", "", "disambiguate by language")
	fs.BoolVar(&opts.JSON, "json", false, "emit JSON matching the sense.blast MCP schema")
	fs.BoolVar(&opts.NoColor, "no-color", false, "disable ANSI color")

	positional, err := parseInterleaved(fs, args)
	if err != nil {
		return blastOptions{}, err
	}

	switch {
	case opts.Diff != "" && len(positional) > 0:
		_, _ = fmt.Fprintln(stderr, "sense blast: pass either <symbol> or --diff, not both")
		_, _ = fmt.Fprintln(stderr, "run 'sense blast --help' for usage")
		return blastOptions{}, fmt.Errorf("cannot mix <symbol> and --diff")
	case opts.Diff == "" && len(positional) < 1:
		_, _ = fmt.Fprintln(stderr, "sense blast: missing symbol argument (or --diff)")
		_, _ = fmt.Fprintln(stderr, "run 'sense blast --help' for usage")
		return blastOptions{}, fmt.Errorf("missing symbol argument")
	}

	if len(positional) >= 1 {
		opts.Symbol = positional[0]
	}

	if opts.MinConfidence < 0 || opts.MinConfidence > 1 {
		_, _ = fmt.Fprintf(stderr, "sense blast: --min-confidence must be between 0.0 and 1.0 (got %v)\n", opts.MinConfidence)
		_, _ = fmt.Fprintln(stderr, "run 'sense blast --help' for usage")
		return blastOptions{}, fmt.Errorf("invalid --min-confidence value: %v", opts.MinConfidence)
	}

	return opts, nil
}
