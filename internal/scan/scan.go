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
	"strconv"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/extract"
	_ "github.com/luuuc/sense/internal/extract/languages" // register every extractor
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
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
	Root              string    // working-tree root (default: ".")
	Sense             string    // sense dir (default: "<Root>/.sense")
	Output            io.Writer // summary-line sink (default: os.Stderr)
	Warnings          io.Writer // per-file warning sink (default: os.Stderr)
	EmbeddingsEnabled bool      // when true, pass 3 generates embeddings for changed symbols
}

// Result summarises one scan invocation.
type Result struct {
	Files      int // total files visited (regular files, not directories)
	Indexed    int // files that had a registered extractor and were processed
	Changed    int // files whose content hash changed (re-parsed)
	Skipped    int // files skipped (unchanged hash)
	Removed    int // files deleted from index (no longer on disk or now ignored)
	Symbols    int // symbols written to the index
	Edges      int // edges resolved and written to sense_edges
	Embedded   int // symbols whose embeddings were generated/updated
	Unresolved int // edges whose target name matched no symbol; dropped
	Warnings   int // per-file failures logged; scan continues past them
	Duration   time.Duration
}

// snippetMaxBytes caps the single-line snippet we store per symbol.
// Large minified lines (bundled JS, generated protos) would otherwise
// balloon the index with source text that nobody reads.
const snippetMaxBytes = 200

// maxUnresolvedWarnings caps the per-edge unresolved-target warnings
// printed to the Warnings sink. A real repo produces thousands of
// unresolved edges (stdlib calls, third-party imports, dynamic
// dispatch) — printing one line each drowns the legitimate
// parse-error warnings users need to see. The accurate count stays
// available on Result.Unresolved; after the cap is reached a single
// "... and N more omitted" summary line closes the stream.
const maxUnresolvedWarnings = 20

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
		if env := os.Getenv("SENSE_DIR"); env != "" {
			senseDir = env
		} else {
			senseDir = filepath.Join(root, ".sense")
		}
	}
	out := opts.Output
	if out == nil {
		out = os.Stderr
	}
	warn := opts.Warnings
	if warn == nil {
		warn = os.Stderr
	}

	cfg, err := config.Load(root)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if env := os.Getenv("SENSE_MAX_FILE_SIZE"); env != "" {
		if v, err := strconv.Atoi(env); err == nil && v > 0 {
			cfg.Scan.MaxFileSizeKB = v
		}
	}

	matcher, err := ignore.Build(root, cfg.Ignore)
	if err != nil {
		return nil, fmt.Errorf("build ignore matcher: %w", err)
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

	if idx.Rebuilt {
		_, _ = fmt.Fprintf(out, "schema version mismatch — rebuilding index from source\n")
	}

	h := &harness{
		ctx:           ctx,
		idx:           idx,
		out:           out,
		warn:          warn,
		parsers:       map[string]*sitter.Parser{},
		matcher:       matcher,
		maxFileSizeKB: cfg.Scan.MaxFileSizeKB,
		seenPaths:     map[string]bool{},
	}
	defer h.closeParsers()

	start := time.Now()
	if err := h.walkTree(root); err != nil {
		return nil, err
	}
	if err := h.removeStaleFiles(); err != nil {
		return nil, err
	}
	if err := h.resolveAndWriteEdges(); err != nil {
		return nil, err
	}
	if err := h.satisfyInterfaces(); err != nil {
		return nil, err
	}
	if err := h.associateTests(); err != nil {
		return nil, err
	}
	if opts.EmbeddingsEnabled {
		if err := h.embedSymbols(); err != nil {
			return nil, err
		}
	}
	if err := idx.StampSchemaVersion(ctx); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	res := &Result{
		Files:      h.files,
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

	_, _ = fmt.Fprintf(out, "scanned %d files (%d changed, %d skipped) in %s\n",
		res.Files, res.Changed, res.Skipped, elapsed)

	return res, nil
}

// ---- harness ----

// harness holds the per-scan state that would otherwise be passed as
// half a dozen arguments through every helper. It is not exported;
// callers stay inside Run.
//
// Scan runs as a two-phase state machine over the harness:
//
//  1. Walk phase (walkTree + writeFile): every file is parsed and its
//     symbols persisted in a per-file SQLite transaction. Each file's
//     emitted edges are buffered into pendingEdges with their source
//     id resolved locally (the source always lives in the emitting
//     file) and their target left as the qualified-name string the
//     extractor wrote.
//
//  2. Resolve phase (resolveAndWriteEdges): after every file has been
//     walked, pendingEdges is drained in one global transaction.
//     Each target name is looked up in the now-complete symbol index
//     and the resolved edge is written. Unresolved edges are dropped.
//
// This differs from the "each file's writes are one atomic unit" shape
// pitch 01-02 described: edges are no longer scoped to their emitting
// file's transaction because most of them point at targets in other
// files. The consequence is called out on resolveAndWriteEdges.
type harness struct {
	ctx     context.Context
	idx     *sqlite.Adapter
	out     io.Writer // summary-line sink
	warn    io.Writer // per-file warning sink
	parsers map[string]*sitter.Parser

	matcher       *ignore.Matcher
	maxFileSizeKB int

	// pendingEdges holds walk-phase output for the resolve phase.
	// Empty at start, filled by writeFile, drained by
	// resolveAndWriteEdges.
	pendingEdges []pendingEdge

	// indexedFiles lists every file successfully processed during
	// the walk, in visit order. The test-association post-pass
	// iterates this to pair test files with their implementation
	// files by naming convention without re-querying sense_files.
	indexedFiles []indexedFile

	// seenPaths tracks every file path visited this walk so stale
	// entries can be detected and removed after the walk.
	seenPaths map[string]bool

	// changedFileIDs collects file IDs that were re-indexed this scan
	// (new or hash-changed). Used by pass 3 to scope embedding work.
	changedFileIDs []int64

	// Tallies for Result.
	files      int
	indexed    int
	changed    int
	skipped    int
	removed    int
	symbols    int
	edges      int
	embedded   int
	unresolved int
	warnings   int
}

// indexedFile is the minimum the test-association pass needs to know
// about each successfully-written file: its id, relative path, and
// the language the extractor reported. Buffering these in-memory
// during the walk avoids a `SELECT * FROM sense_files` round trip
// after the fact.
type indexedFile struct {
	ID       int64
	Path     string
	Language string
}

// pendingEdge is the pre-resolution shape held in harness.pendingEdges.
// SourceID is the numeric symbol id inside the emitting file; it's
// resolved eagerly during writeFile because the source always lives
// in the file being scanned. SourceQualified + SourceParentQualified
// ride along so the resolver can apply receiver rewrites
// (`self.foo` ⇒ `Parent.foo`) without a second DB round trip.
// TargetName is the qualified-name text the extractor wrote — global
// lookup happens in resolveAndWriteEdges.
type pendingEdge struct {
	SourceID              int64
	SourceQualified       string
	SourceParentQualified string
	TargetName            string
	Kind                  model.EdgeKind
	FileID                int64
	Line                  *int
	Confidence            float64
}

func (h *harness) closeParsers() {
	for _, p := range h.parsers {
		p.Close()
	}
}

// int64Ptr returns a pointer to v, or nil if v is 0 (sentinel for file-level edges).
func int64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// warnf logs a per-file warning to h.warn and increments the counter.
// Warnings are non-fatal: scan continues past them.
func (h *harness) warnf(format string, args ...any) {
	h.warnings++
	_, _ = fmt.Fprintf(h.warn, "warn: "+format+"\n", args...)
}

// walkTree walks root depth-first. Dot-prefixed directories (.git,
// .vscode) and the .sense directory are always skipped. Paths matched
// by the ignore matcher are skipped. Symlinks are not followed.
func (h *harness) walkTree(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if cerr := h.ctx.Err(); cerr != nil {
			return cerr
		}

		// Never follow symlinks — avoids infinite loops from cyclic links.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}

		if d.IsDir() {
			if path == root {
				return nil
			}
			name := d.Name()
			if strings.HasPrefix(name, ".") {
				return fs.SkipDir
			}
			if h.matcher.Match(rel, true) {
				return fs.SkipDir
			}
			return nil
		}

		if h.matcher.Match(rel, false) {
			return nil
		}

		h.files++
		h.processFile(path, rel)
		return nil
	})
}

// processFile is the per-file pipeline: detect language, check size cap,
// compare hash for incremental skip, parse, run the extractor, and write.
// All per-file failures are soft — they bump h.warnings and return.
func (h *harness) processFile(path, rel string) {
	h.seenPaths[rel] = true

	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)
	if ex == nil {
		return // not a language we know; just counted in h.files
	}

	// Size cap — skip files above the configured max.
	if h.maxFileSizeKB > 0 {
		info, err := os.Stat(path)
		if err != nil {
			h.warnf("%s: stat failed: %v", rel, err)
			return
		}
		if info.Size() > int64(h.maxFileSizeKB)*1024 {
			h.warnf("%s: skipped (%d KB > %d KB max)", rel, info.Size()/1024, h.maxFileSizeKB)
			return
		}
	}

	source, err := os.ReadFile(path)
	if err != nil {
		h.warnf("%s: read failed: %v", rel, err)
		return
	}

	// Incremental: compare hash to skip unchanged files.
	newHash := hashSource(source)
	fileID, oldHash, metaErr := h.idx.FileMeta(h.ctx, rel)
	if metaErr != nil {
		h.warnf("%s: file meta lookup failed: %v", rel, metaErr)
		// Fall through to re-parse on error.
	} else if oldHash == newHash {
		h.skipped++
		h.indexed++
		if fileID > 0 {
			h.indexedFiles = append(h.indexedFiles, indexedFile{ID: fileID, Path: rel, Language: ex.Language()})
		}
		return
	}

	collected := &collector{}

	if raw, ok := ex.(extract.RawExtractor); ok {
		if err := safeExtractRaw(raw, source, rel, collected); err != nil {
			h.warnf("%s: extract failed: %v", rel, err)
			return
		}
	} else {
		parser, err := h.parserFor(ex)
		if err != nil {
			h.warnf("%s: parser setup failed: %v", rel, err)
			return
		}

		tree := parser.Parse(source, nil)
		if tree == nil {
			h.warnf("%s: parse returned nil tree", rel)
			return
		}
		defer tree.Close()

		if tree.RootNode().HasError() {
			h.warnf("%s: parse errors present, extracting best-effort", rel)
		}

		if err := safeExtract(ex, tree, source, rel, collected); err != nil {
			h.warnf("%s: extract failed: %v", rel, err)
			return
		}
	}

	if err := h.writeFile(rel, ex.Language(), source, newHash, collected); err != nil {
		h.warnf("%s: write failed: %v", rel, err)
		return
	}
	h.indexed++
	h.changed++
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

// writeFile persists one file's symbols atomically and buffers its
// edges for the post-walk resolution pass.
//
// The per-file SQLite transaction still wraps the symbol writes so a
// mid-file failure (write error, context cancellation) rolls back
// cleanly — the index never contains a half-written file's symbols.
// Edges are deliberately excluded from the file-scoped transaction:
// they're cross-file by nature (the pitch-01-03 design point), so
// their transaction boundary is the scan as a whole, handled by
// resolveAndWriteEdges after walkTree completes.
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
//  4. For each edge, look up the source qualified name in the
//     file-local map — the source always lives in the emitting file,
//     so a local lookup is definitive. Buffer the edge into
//     h.pendingEdges with its target name still as a string.
//     resolveAndWriteEdges will turn that string into a symbol_id
//     once every file has been scanned and global names are visible.
//
// Counters for Result.Symbols update after the transaction commits;
// Result.Edges is updated later when resolveAndWriteEdges writes.
func (h *harness) writeFile(rel, lang string, source []byte, fileHash string, c *collector) error {
	var symsWritten int
	var pending []pendingEdge
	var fileID int64
	err := h.idx.InTx(h.ctx, func() error {
		var err error
		fileID, err = h.idx.WriteFile(h.ctx, &model.File{
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
		parentByQualified := make(map[string]string, len(c.symbols))
		for _, s := range c.symbols {
			var parentID *int64
			if s.ParentQualified != "" {
				if pid, ok := idByQualified[s.ParentQualified]; ok {
					parentID = &pid
				}
				// parent not found → leave nil. Cross-file parents
				// stay unresolved in Tier-Basic; a later card can
				// backfill them through the global symbol table.
			}

			row := &model.Symbol{
				FileID:     fileID,
				Name:       s.Name,
				Qualified:  s.Qualified,
				Kind:       s.Kind,
				Visibility: s.Visibility,
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
			parentByQualified[s.Qualified] = s.ParentQualified
			symsWritten++
		}

		for _, e := range c.edges {
			sourceID := idByQualified[e.SourceQualified] // 0 if not found (file-level edge)
			pending = append(pending, pendingEdge{
				SourceID:              sourceID,
				SourceQualified:       e.SourceQualified,
				SourceParentQualified: parentByQualified[e.SourceQualified],
				TargetName:            e.TargetQualified,
				Kind:                  e.Kind,
				FileID:                fileID,
				Line:                  e.Line,
				Confidence:            e.Confidence,
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.symbols += symsWritten
	h.pendingEdges = append(h.pendingEdges, pending...)
	h.indexedFiles = append(h.indexedFiles, indexedFile{ID: fileID, Path: rel, Language: lang})
	h.changedFileIDs = append(h.changedFileIDs, fileID)
	return nil
}

// removeStaleFiles deletes index entries for files that were not seen
// during this walk (deleted from disk, or now excluded by ignore rules).
// FK CASCADE on sense_symbols cleans up symbols; edges referencing those
// symbols are also cascaded.
func (h *harness) removeStaleFiles() error {
	tracked, err := h.idx.FilePaths(h.ctx)
	if err != nil {
		return fmt.Errorf("list tracked files: %w", err)
	}
	var stale []string
	for _, p := range tracked {
		if !h.seenPaths[p] {
			stale = append(stale, p)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	err = h.idx.InTx(h.ctx, func() error {
		for _, p := range stale {
			if err := h.idx.DeleteFile(h.ctx, p); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("remove stale files: %w", err)
	}
	h.removed = len(stale)
	return nil
}

// resolveAndWriteEdges is the resolve phase: after walkTree has
// visited every file and written every symbol, drain pendingEdges
// into sense_edges by feeding each pending edge through the
// resolve.Index.
//
// The resolver does the heavy lifting: exact qualified lookup,
// same-file scope preference on ambiguity, receiver rewrites for
// `self.` / `Self::` targets, and a calls-only unqualified fallback.
// This function is the glue that loads the name index once, iterates
// the buffer, and persists resolved matches in one commit.
//
// Operational contract: the entire drain runs in one SQLite
// transaction. A crash or cancellation between the symbol-write
// phase and the commit here leaves symbols persisted and edges
// missing; because `sense scan` is idempotent, a re-run converges.
// This is the trade-off for cross-file resolution — the per-file
// edge atomicity that pitch 01-02 described no longer holds, and
// most edges couldn't honour it anyway since their targets live
// outside the emitting file.
//
// Scale assumption: the whole pending buffer fits in memory and the
// single commit is reasonable at pitch-target sizes (≤30K symbols,
// ≤120K edges ⇒ low-MB range, commits in milliseconds). Much larger
// repos will want a batched commit strategy; not this card's job.
func (h *harness) resolveAndWriteEdges() error {
	if len(h.pendingEdges) == 0 {
		return nil
	}
	refs, err := h.idx.SymbolRefs(h.ctx)
	if err != nil {
		return fmt.Errorf("load symbols for edge resolution: %w", err)
	}
	resolver := resolve.NewIndex(refs)

	var written, unresolved int
	err = h.idx.InTx(h.ctx, func() error {
		for _, pe := range h.pendingEdges {
			r, ok := resolver.Resolve(resolve.Request{
				Target:                pe.TargetName,
				Kind:                  pe.Kind,
				SourceFileID:          pe.FileID,
				SourceQualified:       pe.SourceQualified,
				SourceParentQualified: pe.SourceParentQualified,
				BaseConfidence:        pe.Confidence,
			})
			if !ok {
				// Unresolved edge: the extractor emitted it but no
				// symbol matched the target name. Typical causes are
				// external calls (stdlib, imported packages) or
				// dynamic dispatch we can't statically resolve. Log
				// the first N so a user debugging a sparse graph can
				// see a sample; the rest roll into the count only
				// (surfaced via Result.Unresolved).
				if unresolved < maxUnresolvedWarnings {
					_, _ = fmt.Fprintf(h.warn,
						"warn: unresolved target %q from %q\n",
						pe.TargetName, pe.SourceQualified,
					)
				}
				unresolved++
				continue
			}
			if r.Ambiguous {
				// Ambiguous resolution picked a candidate from more
				// than one option at the same qualified-name key.
				// The pitch requires a warning so a user debugging
				// surprising graph output can see which edges were
				// guess-resolved. The Warnings counter deliberately
				// is NOT bumped — that counter tracks per-file
				// failures; ambiguity is a successful resolution
				// with reduced confidence, not a failure.
				_, _ = fmt.Fprintf(h.warn,
					"warn: ambiguous target %q from %q → picked id=%d confidence=%.2f\n",
					pe.TargetName, pe.SourceQualified, r.SymbolID, r.Confidence,
				)
			}
			edge := &model.Edge{
				SourceID:   int64Ptr(pe.SourceID),
				TargetID:   r.SymbolID,
				Kind:       pe.Kind,
				FileID:     pe.FileID,
				Line:       pe.Line,
				Confidence: r.Confidence,
			}
			if _, werr := h.idx.WriteEdge(h.ctx, edge); werr != nil {
				return fmt.Errorf("write edge source=%d target=%s: %w", pe.SourceID, pe.TargetName, werr)
			}
			written++
		}
		return nil
	})
	if err != nil {
		return err
	}
	if extra := unresolved - maxUnresolvedWarnings; extra > 0 {
		_, _ = fmt.Fprintf(h.warn,
			"warn: ... and %d more unresolved targets omitted\n", extra,
		)
	}
	h.edges += written
	h.unresolved += unresolved
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

func safeExtractRaw(ex extract.RawExtractor, source []byte, rel string, c *collector) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("extractor panicked: %v", r)
		}
	}()
	return ex.ExtractRaw(source, rel, c)
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
