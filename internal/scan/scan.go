// Package scan walks a working tree and materialises the Sense index
// on disk. For each file it detects the language by extension, parses
// with the appropriate tree-sitter grammar, runs the language's
// extractor, and writes the resulting symbols + intra-file edges into
// the SQLite adapter.
//
// Scan is deliberately single-threaded today: one parser per language
// (cached across files), one SQLite connection (serialised writes).
// Concurrency lands when profiles show contention worth paying for.
package scan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	_ "github.com/luuuc/sense/internal/extract/languages" // register every extractor
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// Options bounds a scan run. Zero values select sensible defaults.
//
// Output and Warnings are distinct sinks so per-file diagnostics don't
// interleave with the one-line summary:
//   - Summary (one line per run, machine-parseable) goes to Output.
//   - Per-file warnings (parse errors, read failures, etc.) go to Warnings.
// A caller that cares only about the summary can leave Warnings at its
// default (os.Stderr) and pipe Output on its own; callers that want a
// quiet run can redirect Warnings to io.Discard.
type Options struct {
	Root     string    // working-tree root (default: ".")
	Sense    string    // sense dir (default: "<Root>/.sense")
	Output   io.Writer // summary-line sink (default: os.Stderr)
	Warnings io.Writer // per-file warning sink (default: os.Stderr)
}

// Result summarises one scan invocation.
type Result struct {
	Files    int // total files visited (regular files, not directories)
	Indexed  int // files that had a registered extractor and were processed
	Symbols  int // symbols written to the index
	Edges    int // edges written to the index
	Warnings int // per-file failures logged; scan continues past them
	Duration time.Duration
}

// snippetMaxBytes caps the single-line snippet we store per symbol.
// Large minified lines (bundled JS, generated protos) would otherwise
// balloon the index with source text that nobody reads.
const snippetMaxBytes = 200

// Run ensures the .sense directory and index.db exist, walks the
// working tree, parses each file with a registered extractor, and
// writes symbols + intra-file edges into the index. Returns the
// summary and any fatal error. Per-file parse/extract errors are
// non-fatal: a warning is logged, the scan continues, and the result's
// Warnings counter is incremented.
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
	warn := opts.Warnings
	if warn == nil {
		warn = os.Stderr
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

	h := &harness{
		ctx:     ctx,
		idx:     idx,
		out:     out,
		warn:    warn,
		parsers: map[string]*sitter.Parser{},
	}
	defer h.closeParsers()

	start := time.Now()
	if err := h.walkTree(root); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	res := &Result{
		Files:    h.files,
		Indexed:  h.indexed,
		Symbols:  h.symbols,
		Edges:    h.edges,
		Warnings: h.warnings,
		Duration: elapsed,
	}

	// Summary line follows the "<data> in <duration>" shape from
	// bootstrap so the CLI's stderr reads the same across pitches —
	// just carrying more data now. elapsed is printed raw so
	// time.Duration.String picks the right unit.
	_, _ = fmt.Fprintf(out, "%d files, %d indexed, %d symbols, %d edges in %s\n",
		res.Files, res.Indexed, res.Symbols, res.Edges, elapsed)

	return res, nil
}

// ---- harness ----

// harness holds the per-scan state that would otherwise be passed as
// half a dozen arguments through every helper. It is not exported;
// callers stay inside Run.
type harness struct {
	ctx     context.Context
	idx     *sqlite.Adapter
	out     io.Writer // summary-line sink
	warn    io.Writer // per-file warning sink
	parsers map[string]*sitter.Parser

	// Tallies for Result.
	files    int
	indexed  int
	symbols  int
	edges    int
	warnings int
}

func (h *harness) closeParsers() {
	for _, p := range h.parsers {
		p.Close()
	}
}

// warnf logs a per-file warning to h.warn and increments the counter.
// Warnings are non-fatal: scan continues past them.
func (h *harness) warnf(format string, args ...any) {
	h.warnings++
	_, _ = fmt.Fprintf(h.warn, "warn: "+format+"\n", args...)
}

// walkTree walks root depth-first. Dot-prefixed directories (.git,
// .sense, .vscode) are skipped — same policy as bootstrap.
func (h *harness) walkTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if cerr := h.ctx.Err(); cerr != nil {
			return cerr
		}
		if d.IsDir() {
			if path != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		h.files++
		h.processFile(root, path)
		return nil
	})
}

// processFile is the per-file pipeline: detect language, parse, run
// the extractor, resolve qualified names to IDs, and write. All
// per-file failures are soft — they bump h.warnings and return.
func (h *harness) processFile(root, path string) {
	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)
	if ex == nil {
		return // not a language we know; just counted in h.files
	}

	source, err := os.ReadFile(path)
	if err != nil {
		h.warnf("%s: read failed: %v", path, err)
		return
	}

	parser, err := h.parserFor(ex)
	if err != nil {
		h.warnf("%s: parser setup failed: %v", path, err)
		return
	}

	tree := parser.Parse(source, nil)
	if tree == nil {
		// ParseCtx returns nil only when tree-sitter gives up — rare
		// but possible with pathological inputs or cancelled context.
		h.warnf("%s: parse returned nil tree", path)
		return
	}
	defer tree.Close()

	// tree-sitter is error-tolerant: even files with syntax errors
	// produce a usable tree with ERROR nodes. We emit what we can and
	// note the condition — a user editing a half-written file still
	// gets meaningful results from `sense scan`.
	if tree.RootNode().HasError() {
		h.warnf("%s: parse errors present, extracting best-effort", path)
	}

	rel, relErr := filepath.Rel(root, path)
	if relErr != nil {
		rel = path
	}

	collected := &collector{}
	if err := safeExtract(ex, tree, source, rel, collected); err != nil {
		h.warnf("%s: extract failed: %v", rel, err)
		return
	}

	if err := h.writeFile(rel, ex.Language(), source, collected); err != nil {
		h.warnf("%s: write failed: %v", rel, err)
		return
	}
	h.indexed++
}

// parserFor returns a cached parser for the extractor's language. The
// parser keeps its SetLanguage binding across calls — subsequent files
// in the same language reuse it without re-binding.
func (h *harness) parserFor(ex extract.Extractor) (*sitter.Parser, error) {
	if p, ok := h.parsers[ex.Language()]; ok {
		return p, nil
	}
	p := sitter.NewParser()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		p.Close()
		return nil, err
	}
	h.parsers[ex.Language()] = p
	return p, nil
}

// writeFile persists one file's worth of symbols and edges atomically.
// All writes for a given source file run inside a single SQLite
// transaction so a mid-file failure (write error, context cancellation)
// rolls back cleanly — the index never contains a half-written file's
// symbols with its edges missing. Re-scans still converge regardless;
// the transaction just reduces the window in which partial state is
// observable.
//
// The steps inside the transaction:
//
//  1. Upsert the sense_files row; the symbols column is set to the
//     extracted symbol count so status queries don't need a COUNT(*).
//  2. Sort symbols so parents precede children (by Qualified length).
//     This guarantees every ParentQualified lookup finds its target
//     in the already-inserted map without a second pass.
//  3. For each symbol, resolve ParentQualified → ParentID (may be nil
//     when the parent lives in another file — legitimate state, not
//     an error) and WriteSymbol.
//  4. For each edge, resolve Source/Target qualified names to symbol
//     IDs inside this file. Edges whose endpoint isn't local are
//     dropped — 01-03 backfills cross-file edges.
//
// Counters update only after the transaction commits, so Result.Symbols
// and Result.Edges always reflect what's actually in the index.
func (h *harness) writeFile(rel, lang string, source []byte, c *collector) error {
	var (
		symsWritten  int
		edgesWritten int
	)
	err := h.idx.InTx(h.ctx, func() error {
		fileHash := hashSource(source)
		fileID, err := h.idx.WriteFile(h.ctx, &model.File{
			Path:      rel,
			Language:  lang,
			Hash:      fileHash,
			Symbols:   len(c.symbols),
			IndexedAt: time.Now().UTC(),
		})
		if err != nil {
			return fmt.Errorf("write file: %w", err)
		}

		// Stable: at equal length, preserve extraction order so repeated
		// scans write in the same order (determinism helps debugging).
		sort.SliceStable(c.symbols, func(i, j int) bool {
			return len(c.symbols[i].Qualified) < len(c.symbols[j].Qualified)
		})

		idByQualified := make(map[string]int64, len(c.symbols))
		for _, s := range c.symbols {
			var parentID *int64
			if s.ParentQualified != "" {
				if pid, ok := idByQualified[s.ParentQualified]; ok {
					parentID = &pid
				}
				// parent not found → leave nil. 01-03 will resolve the
				// cross-file parent links; no warning here, this is
				// the expected steady state for Go method receivers
				// that live in a sibling file of the same package.
			}

			row := &model.Symbol{
				FileID:     fileID,
				Name:       s.Name,
				Qualified:  s.Qualified,
				Kind:       s.Kind,
				Visibility: "public", // placeholder; pitch defers real extraction
				ParentID:   parentID,
				LineStart:  s.LineStart,
				LineEnd:    s.LineEnd,
				Snippet:    snippetForLine(source, s.LineStart),
			}
			id, werr := h.idx.WriteSymbol(h.ctx, row)
			if werr != nil {
				return fmt.Errorf("write symbol %q: %w", s.Qualified, werr)
			}
			idByQualified[s.Qualified] = id
			symsWritten++
		}

		for _, e := range c.edges {
			sourceID, ok := idByQualified[e.SourceQualified]
			if !ok {
				continue // source unknown to this file; defensive, shouldn't happen.
			}
			targetID, ok := idByQualified[e.TargetQualified]
			if !ok {
				continue // cross-file edge; 01-03 will backfill.
			}
			edge := &model.Edge{
				SourceID:   sourceID,
				TargetID:   targetID,
				Kind:       e.Kind,
				FileID:     fileID,
				Line:       e.Line,
				Confidence: e.Confidence,
			}
			if _, werr := h.idx.WriteEdge(h.ctx, edge); werr != nil {
				return fmt.Errorf("write edge %s→%s: %w", e.SourceQualified, e.TargetQualified, werr)
			}
			edgesWritten++
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.symbols += symsWritten
	h.edges += edgesWritten
	return nil
}

// safeExtract wraps ex.Extract in a recover() so a bad extractor
// panicking on a weird CST node fails just this file, not the scan.
// Same posture as the fixture harness.
func safeExtract(ex extract.Extractor, tree *sitter.Tree, source []byte, rel string, c *collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("extractor panicked: %v", r)
		}
	}()
	return ex.Extract(tree, source, rel, c)
}

// ---- collector (per-file Emitter) ----

type collector struct {
	symbols []extract.EmittedSymbol
	edges   []extract.EmittedEdge
}

func (c *collector) Symbol(s extract.EmittedSymbol) error { c.symbols = append(c.symbols, s); return nil }
func (c *collector) Edge(e extract.EmittedEdge) error     { c.edges = append(c.edges, e); return nil }

// ---- helpers ----

// hashSource returns the first 16 hex chars of SHA-256 of the source.
// Truncation is fine for our purposes (change detection, not
// cryptographic identity); 8 bytes of hash are enough for billions of
// files before a meaningful collision.
func hashSource(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:8])
}

// snippetForLine returns the trimmed content of the given 1-indexed
// line, capped at snippetMaxBytes. Missing / out-of-range lines
// return "". No unicode-aware slicing: the cap is a byte budget, and
// tree-sitter-extracted line numbers are always in the input so the
// range walk terminates.
func snippetForLine(source []byte, line int) string {
	if line <= 0 {
		return ""
	}
	start := 0
	current := 1
	for i := 0; i < len(source) && current < line; i++ {
		if source[i] == '\n' {
			current++
			start = i + 1
		}
	}
	if current < line {
		return ""
	}
	end := start
	for end < len(source) && source[end] != '\n' {
		end++
	}
	s := strings.TrimSpace(string(source[start:end]))
	if len(s) > snippetMaxBytes {
		s = s[:snippetMaxBytes]
	}
	return s
}
