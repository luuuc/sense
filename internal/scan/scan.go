// Package scan walks a working tree and materialises the sense index on
// disk. This bootstrap pitch runs a no-op walker — the .sense/ directory
// is created, the SQLite schema is applied, the file count is reported,
// and nothing is written. Tree-sitter extraction lands in pitch 01-02.
package scan

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luuuc/sense/internal/sqlite"
)

// Options bounds a scan run. Zero values select sensible defaults.
type Options struct {
	Root   string    // working-tree root (default: ".")
	Sense  string    // sense dir (default: "<Root>/.sense")
	Output io.Writer // summary-line sink (default: os.Stderr)
}

// Result summarises one scan invocation.
type Result struct {
	Files    int
	Duration time.Duration
}

// Run ensures the .sense directory and index.db exist, walks the working
// tree, counts the files visited, and reports timing. Returns the summary
// and any error encountered during directory creation, index open, or
// walk.
func Run(ctx context.Context, opts Options) (*Result, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}
	senseDir := opts.Sense
	if senseDir == "" {
		senseDir = filepath.Join(root, ".sense")
	}
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}

	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sense dir: %w", err)
	}

	dbPath := filepath.Join(senseDir, "index.db")
	idx, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("open index: %w", err)
	}
	defer func() { _ = idx.Close() }()

	start := time.Now()
	files, err := walkTree(ctx, root)
	if err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	// Result delivery is best-effort — if the summary line can't be written,
	// the scan still succeeded and the Result return value carries the data.
	// elapsed is printed raw so time.Duration.String picks the right unit
	// (µs → ms → s); rounding to ms would display "0s" for fast scans.
	_, _ = fmt.Fprintf(out, "%d files in %s\n", files, elapsed)

	return &Result{Files: files, Duration: elapsed}, nil
}

// walkTree returns the number of regular files under root. Directories
// whose name begins with "." are skipped — this covers .sense, .git, and
// editor metadata without pretending to be a .gitignore loader (that
// lands in a later cycle).
func walkTree(ctx context.Context, root string) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if d.IsDir() {
			name := d.Name()
			// Don't skip the walk root itself (typically "." or an absolute path).
			if path != root && strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("walk: %w", err)
	}
	return count, nil
}
