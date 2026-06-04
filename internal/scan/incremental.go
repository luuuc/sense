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
	"github.com/luuuc/sense/internal/summary"
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
	Changed           []string     // relative paths of modified/created files
	Removed           []string     // relative paths of deleted files
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
		progress:      newProgress(out, true),
		collector:     newWarningCollector(),
		parsers:       parsers.parsers,
		matcher:       opts.Matcher,
		maxFileSizeKB: opts.MaxFileSizeKB,
		seenPaths:     map[string]bool{},
	}
	if ownParsers {
		defer parsers.Close()
	}

	start := time.Now()
	var phases PhaseTiming

	symStmt, err := opts.Idx.PrepareSymbolStmt(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare symbol stmt: %w", err)
	}
	defer func() { _ = symStmt.Close() }()
	h.symbolStmt = symStmt
	defer func() { h.symbolStmt = nil }()

	t0 := time.Now()
	for _, rel := range opts.Changed {
		abs := filepath.Join(opts.Root, rel)
		h.processFile(abs, rel)
	}
	phases.Walk = time.Since(t0)

	if len(opts.Removed) > 0 {
		t0 = time.Now()
		if err := h.removeDeleted(opts.Removed); err != nil {
			return nil, err
		}
		h.removed = len(opts.Removed)
		phases.RemoveStale = time.Since(t0)
	}

	if err := h.deriveIncremental(opts.EmbeddingsEnabled, &phases); err != nil {
		return nil, err
	}

	if h.changed > 0 || h.removed > 0 {
		senseDir := filepath.Join(opts.Root, ".sense")
		if serr := summary.Generate(ctx, opts.Idx, senseDir, opts.Root); serr != nil {
			_, _ = fmt.Fprintf(warn, "warn: generate summary: %v\n", serr)
		}
	}

	return buildIncrementalResult(h, opts, time.Since(start), phases), nil
}

// removeDeleted deletes the given relative paths from the index in one
// transaction; FK CASCADE removes their symbols and edges.
func (h *harness) removeDeleted(removed []string) error {
	err := h.idx.InTx(h.ctx, func() error {
		for _, rel := range removed {
			if err := h.idx.DeleteFile(h.ctx, rel); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove deleted files: %w", err)
	}
	return nil
}

// deriveIncremental runs the post-walk edge passes an incremental scan needs —
// resolve, associate tests, naming conventions, and (when enabled) embedding —
// recording each phase's timing.
func (h *harness) deriveIncremental(embeddingsEnabled bool, phases *PhaseTiming) error {
	t0 := time.Now()
	if err := h.resolveAndWriteEdges(); err != nil {
		return fmt.Errorf("resolve edges: %w", err)
	}
	phases.ResolveEdges = time.Since(t0)

	t0 = time.Now()
	if err := h.associateTests(); err != nil {
		return fmt.Errorf("associate tests: %w", err)
	}
	phases.AssociateTests = time.Since(t0)

	t0 = time.Now()
	if err := h.namingConventionEdges(); err != nil {
		return fmt.Errorf("naming convention edges: %w", err)
	}
	phases.NamingConventions = time.Since(t0)

	if embeddingsEnabled {
		t0 = time.Now()
		if err := h.embedSymbols(); err != nil {
			return fmt.Errorf("embed symbols: %w", err)
		}
		phases.Embed = time.Since(t0)
	}
	return nil
}

// buildIncrementalResult assembles the incremental run summary from the harness
// tallies. Files counts only the changed set an incremental scan was handed.
func buildIncrementalResult(h *harness, opts IncrementalOptions, elapsed time.Duration, phases PhaseTiming) *Result {
	return &Result{
		Files:      len(opts.Changed),
		Indexed:    h.indexed,
		Changed:    h.changed,
		Skipped:    h.skipped,
		Removed:    h.removed,
		Symbols:    h.symbols,
		Edges:      h.edges,
		Embedded:   h.embedded,
		Unresolved: h.unresolved,
		Warnings:   h.collector.count(),
		Duration:   elapsed,
		Phases:     phases,
	}
}
