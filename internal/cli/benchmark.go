package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/luuuc/sense/internal/benchmark"
)

const benchmarkHelp = `usage: sense benchmark [flags]

Run performance benchmarks against the current project's index.

Measures query latency (graph, search, blast, conventions, status),
scan throughput, index size, and memory usage.

Flags:
  --iterations N            Number of query iterations (default 100)
  --json                    Emit machine-readable JSON report
  -h, --help                Show this help

Examples:
  sense benchmark
  sense benchmark --iterations 50
  sense benchmark --json

Exit codes:
  0  success
  1  general error
  3  index missing (run 'sense scan' first)
`

func RunBenchmark(args []string, cio IO) int {
	fs := flag.NewFlagSet("sense benchmark", flag.ContinueOnError)
	fs.SetOutput(cio.Stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(cio.Stderr, benchmarkHelp) }

	iterations := fs.Int("iterations", 100, "number of query iterations")
	jsonFlag := fs.Bool("json", false, "emit machine-readable JSON report")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitSuccess
		}
		return ExitGeneralError
	}

	ctx := context.Background()

	dbPath := filepath.Join(cio.Dir, ".sense", "index.db")
	if _, err := os.Stat(dbPath); err != nil {
		_, _ = fmt.Fprintln(cio.Stderr, "sense: no index found. Run 'sense scan' to build one.")
		return ExitIndexMissing
	}

	binPath, _ := exec.LookPath("sense")

	report, err := benchmark.Run(ctx, cio.Dir, benchmark.Options{
		Iterations: *iterations,
		Dir:        cio.Dir,
		Binary:     binPath,
	})
	if err != nil {
		_, _ = fmt.Fprintf(cio.Stderr, "sense benchmark: %v\n", err)
		return ExitGeneralError
	}

	if *jsonFlag {
		out, err := benchmark.MarshalJSON(report)
		if err != nil {
			_, _ = fmt.Fprintf(cio.Stderr, "sense benchmark: %v\n", err)
			return ExitGeneralError
		}
		_, _ = fmt.Fprintln(cio.Stdout, string(out))
	} else {
		benchmark.WriteHuman(cio.Stdout, report)
	}

	return ExitSuccess
}
