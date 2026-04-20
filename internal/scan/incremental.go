package scan

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/sqlite"
)

// IncrementalOptions controls an incremental re-index.
type IncrementalOptions struct {
	Root              string
	Idx               *sqlite.Adapter
	Matcher           *ignore.Matcher
	MaxFileSizeKB     int
	EmbeddingsEnabled bool
	Output            io.Writer
	Warnings          io.Writer
	Changed           []string // relative paths of modified/created files
	Removed           []string // relative paths of deleted files
	Parsers           *ParserCache // reusable parser cache; nil creates a temporary one
}

// RunIncremental re-indexes a specific set of changed files and removes
// deleted ones. It uses the same per-file processing, edge resolution,
// and embedding logic as the full scan but scoped to the provided paths.
func RunIncremental(ctx context.Context, opts IncrementalOptions) (*Result, error) {
	out := opts.Output
	if out == nil {
		out = io.Discard
	}
	warn := opts.Warnings
	if warn == nil {
		warn = os.Stderr
	}

	parsers := opts.Parsers
	ownParsers := parsers == nil
	if ownParsers {
		parsers = NewParserCache()
	}

	h := &harness{
		ctx:           ctx,
		idx:           opts.Idx,
		out:           out,
		warn:          warn,
		parsers:       parsers.parsers,
		matcher:       opts.Matcher,
		maxFileSizeKB: opts.MaxFileSizeKB,
		seenPaths:     map[string]bool{},
	}
	if ownParsers {
		defer parsers.Close()
	}

	start := time.Now()

	for _, rel := range opts.Changed {
		abs := filepath.Join(opts.Root, rel)
		h.processFile(abs, rel)
	}

	if len(opts.Removed) > 0 {
		err := h.idx.InTx(ctx, func() error {
			for _, rel := range opts.Removed {
				if err := h.idx.DeleteFile(ctx, rel); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("remove deleted files: %w", err)
		}
		h.removed = len(opts.Removed)
	}

	if err := h.resolveAndWriteEdges(); err != nil {
		return nil, fmt.Errorf("resolve edges: %w", err)
	}

	if err := h.associateTests(); err != nil {
		return nil, fmt.Errorf("associate tests: %w", err)
	}

	if opts.EmbeddingsEnabled {
		if err := h.embedSymbols(); err != nil {
			return nil, fmt.Errorf("embed symbols: %w", err)
		}
	}

	elapsed := time.Since(start)

	res := &Result{
		Files:      len(opts.Changed),
		Indexed:    h.indexed,
		Changed:    h.changed,
		Skipped:    h.skipped,
		Removed:    h.removed,
		Symbols:    h.symbols,
		Edges:      h.edges,
		Embedded:   h.embedded,
		Unresolved: h.unresolved,
		Warnings:   h.warnings,
		Duration:   elapsed,
	}

	return res, nil
}
