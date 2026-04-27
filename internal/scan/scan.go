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
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	"golang.org/x/sync/errgroup"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/extract"
	_ "github.com/luuuc/sense/internal/extract/languages" // register every extractor
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/resolve"
	"github.com/luuuc/sense/internal/setup"
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
	EmbeddingsEnabled bool      // when true, embeddings are part of the index pipeline
	Embed             bool      // block until embeddings complete; requires EmbeddingsEnabled. When false, embeddings are deferred and a watermark is written for the MCP server to pick up.
}

// PhaseTiming records how long each scan phase took.
type PhaseTiming struct {
	Walk              time.Duration
	RemoveStale       time.Duration
	ResolveEdges      time.Duration
	SatisfyInterfaces time.Duration
	AssociateTests    time.Duration
	Temporal          time.Duration
	Embed             time.Duration
	BuildHNSW         time.Duration
}

// Result summarises one scan invocation.
type Result struct {
	Files          int // total files visited (regular files, not directories)
	Indexed        int // files that had a registered extractor and were processed
	Changed        int // files whose content hash changed (re-parsed)
	Skipped        int // files skipped (unchanged hash)
	Removed        int // files deleted from index (no longer on disk or now ignored)
	Symbols        int // symbols written to the index
	Edges          int // edges resolved and written to sense_edges
	Embedded       int // symbols whose embeddings were generated/updated
	EmbeddingDebt  int // symbols needing embeddings (deferred to background)
	Unresolved     int // edges whose target name matched no symbol; dropped
	Ambiguous      int // edges resolved via ambiguous (multi-match) fallback
	Warnings       int // per-file failures logged; scan continues past them
	DefaultIgnored int // directories skipped by default ignore patterns
	Duration       time.Duration
	Phases         PhaseTiming
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

	_, senseDirErr := os.Stat(senseDir)
	firstRun := os.IsNotExist(senseDirErr)

	if err := os.MkdirAll(senseDir, 0o755); err != nil {
		return nil, fmt.Errorf("create sense dir: %w", err)
	}

	if addSenseToGitignore(root) {
		_, _ = fmt.Fprintf(out, "added .sense/ to .gitignore\n")
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
	if idx.FTSMigrated {
		_, _ = fmt.Fprintf(out, "migrated fts index — keyword search will repopulate during this scan\n")
	}

	h := &harness{
		ctx:            ctx,
		idx:            idx,
		out:            out,
		warn:           warn,
		root:           root,
		parsers:        map[string]*sitter.Parser{},
		matcher:        matcher,
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
		maxFileSizeKB:  cfg.Scan.MaxFileSizeKB,
		seenPaths:      map[string]bool{},
	}
	defer h.closeParsers()

	start := time.Now()
	var phases PhaseTiming

	t0 := start
	if err := h.walkTree(root); err != nil {
		return nil, err
	}
	phases.Walk = time.Since(t0)

	t0 = time.Now()
	if err := h.removeStaleFiles(); err != nil {
		return nil, err
	}
	phases.RemoveStale = time.Since(t0)

	t0 = time.Now()
	if err := h.resolveAndWriteEdges(); err != nil {
		return nil, err
	}
	phases.ResolveEdges = time.Since(t0)

	t0 = time.Now()
	if err := h.satisfyInterfaces(); err != nil {
		return nil, err
	}
	phases.SatisfyInterfaces = time.Since(t0)

	t0 = time.Now()
	if err := h.associateTests(); err != nil {
		return nil, err
	}
	phases.AssociateTests = time.Since(t0)

	t0 = time.Now()
	if err := h.extractTemporalCoupling(); err != nil {
		return nil, err
	}
	phases.Temporal = time.Since(t0)

	if opts.EmbeddingsEnabled && opts.Embed {
		t0 = time.Now()
		if err := h.embedSymbols(); err != nil {
			return nil, err
		}

		// Backfill embeddings for symbols indexed in prior scans
		// that were never embedded (changedFileIDs was empty).
		if pending, perr := idx.EmbeddingDebtCount(ctx); perr == nil && pending > 0 {
			n, eerr := EmbedPending(ctx, idx, root, senseDir)
			if eerr != nil {
				return nil, fmt.Errorf("embed pending symbols: %w", eerr)
			}
			h.embedded += n
		}
		phases.Embed = time.Since(t0)

		t0 = time.Now()
		if err := h.buildHNSWIndex(senseDir); err != nil {
			_, _ = fmt.Fprintf(warn, "warn: hnsw index build failed: %v\n", err)
		}
		phases.BuildHNSW = time.Since(t0)

		if derr := idx.DeleteMeta(ctx, "embedding_watermark"); derr != nil {
			_, _ = fmt.Fprintf(warn, "warn: clear embedding watermark: %v\n", derr)
		}
	}

	var embeddingDebt int
	if opts.EmbeddingsEnabled && !opts.Embed && h.changed > 0 {
		ts := time.Now().UTC().Format(time.RFC3339)
		if err := idx.WriteMeta(ctx, "embedding_watermark", ts); err != nil {
			_, _ = fmt.Fprintf(warn, "warn: write embedding watermark: %v\n", err)
		}
		if debt, derr := idx.EmbeddingDebtCount(ctx); derr == nil {
			embeddingDebt = debt
		}
	}

	if err := idx.StampSchemaVersion(ctx); err != nil {
		return nil, err
	}
	elapsed := time.Since(start)

	res := &Result{
		Files:          h.files,
		Indexed:        h.indexed,
		Changed:        h.changed,
		Skipped:        h.skipped,
		Removed:        h.removed,
		Symbols:        h.symbols,
		Edges:          h.edges,
		Embedded:       h.embedded,
		EmbeddingDebt:  embeddingDebt,
		Unresolved:     h.unresolved,
		Ambiguous:      h.ambiguous,
		Warnings:       h.warnings,
		DefaultIgnored: h.defaultIgnored,
		Duration:       elapsed,
		Phases:         phases,
	}

	_, _ = fmt.Fprintf(out, "scanned %d files (%d indexed, %d changed, %d skipped) in %s\n",
		res.Files, res.Indexed, res.Changed, res.Skipped, elapsed)
	if res.Edges > 0 || res.Unresolved > 0 {
		_, _ = fmt.Fprintf(out, "edges: %d resolved, %d unresolved, %d ambiguous\n",
			res.Edges, res.Unresolved, res.Ambiguous)
	}
	if res.DefaultIgnored > 0 {
		_, _ = fmt.Fprintf(out, "skipped %d directories (default ignores: %s)\n",
			res.DefaultIgnored, strings.Join(ignore.DefaultPatterns(), ", "))
	}
	if embeddingDebt > 0 {
		_, _ = fmt.Fprintf(out, "graph, blast, and conventions ready — embeddings deferred (%d symbols)\n", embeddingDebt)
	}
	if elapsed > time.Second {
		printPhaseBreakdown(out, elapsed, phases)
	}

	if firstRun {
		if _, serr := setup.Run(root, out, &setup.Options{CurrentOnly: true}); serr != nil {
			_, _ = fmt.Fprintf(warn, "warn: AI tool setup failed: %v\n", serr)
		}
	}

	return res, nil
}

func printPhaseBreakdown(out io.Writer, total time.Duration, p PhaseTiming) {
	pct := func(d time.Duration) int {
		if total == 0 {
			return 0
		}
		return int(100 * d / total)
	}
	_, _ = fmt.Fprintf(out, "phases: walk %s (%d%%), stale %s (%d%%), edges %s (%d%%), interfaces %s (%d%%), tests %s (%d%%), temporal %s (%d%%)",
		p.Walk, pct(p.Walk),
		p.RemoveStale, pct(p.RemoveStale),
		p.ResolveEdges, pct(p.ResolveEdges),
		p.SatisfyInterfaces, pct(p.SatisfyInterfaces),
		p.AssociateTests, pct(p.AssociateTests),
		p.Temporal, pct(p.Temporal))
	if p.Embed > 0 || p.BuildHNSW > 0 {
		_, _ = fmt.Fprintf(out, ", embed %s (%d%%), hnsw %s (%d%%)",
			p.Embed, pct(p.Embed),
			p.BuildHNSW, pct(p.BuildHNSW))
	}
	_, _ = fmt.Fprintln(out)
}

func addSenseToGitignore(root string) bool {
	gi := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(gi)
	if err != nil {
		return false
	}

	s := bufio.NewScanner(strings.NewReader(string(data)))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == ".sense" || line == ".sense/" {
			return false
		}
	}
	if s.Err() != nil {
		return false
	}

	prefix := "\n"
	if len(data) > 0 && data[len(data)-1] == '\n' {
		prefix = ""
	}

	af, err := os.OpenFile(gi, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return false
	}
	_, err = fmt.Fprintf(af, "%s# Sense index (auto-generated by sense scan)\n.sense/\n", prefix)
	closeErr := af.Close()
	return err == nil && closeErr == nil
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
	root    string    // repository root directory
	parsers map[string]*sitter.Parser

	matcher        *ignore.Matcher
	defaultMatcher *ignore.Matcher
	maxFileSizeKB  int

	// symbolStmt is a prepared statement for WriteSymbol, created at
	// the start of walkTree and closed when walkTree returns.
	symbolStmt *sql.Stmt

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

	// removedSymbolIDs records symbol IDs from stale files before they
	// are cascade-deleted. Used for incremental HNSW index updates.
	removedSymbolIDs []int64

	// Tallies for Result.
	files          int
	indexed        int
	changed        int
	skipped        int
	removed        int
	symbols        int
	edges          int
	embedded       int
	unresolved     int
	ambiguous      int
	warnings       int
	defaultIgnored int
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

type walkEntry struct {
	path string
	rel  string
}

// walkTree walks root depth-first. Dot-prefixed directories (.git,
// .vscode) and the .sense directory are always skipped. Paths matched
// by the ignore matcher are skipped. Symlinks are not followed.
func (h *harness) walkTree(root string) error {
	stmt, err := h.idx.PrepareSymbolStmt(h.ctx)
	if err != nil {
		return fmt.Errorf("prepare symbol stmt: %w", err)
	}
	defer func() { _ = stmt.Close() }()
	h.symbolStmt = stmt
	defer func() { h.symbolStmt = nil }()

	// Phase 1: collect file paths.
	var entries []walkEntry
	werr := filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if cerr := h.ctx.Err(); cerr != nil {
			return cerr
		}
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
				if h.defaultMatcher.Match(rel, true) {
					h.defaultIgnored++
				}
				return fs.SkipDir
			}
			return nil
		}

		if h.matcher.Match(rel, false) {
			return nil
		}

		h.files++
		h.seenPaths[rel] = true
		entries = append(entries, walkEntry{path, rel})
		return nil
	})
	if werr != nil {
		return werr
	}

	if len(entries) == 0 {
		return nil
	}

	// Phase 2: pre-load file hashes for incremental skip.
	hashMap, err := h.idx.FileHashMap(h.ctx)
	if err != nil {
		return fmt.Errorf("load file hashes: %w", err)
	}

	// Phase 3: parallel parse+extract.
	results := make([]*fileResult, len(entries))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(h.ctx)
	g.SetLimit(runtime.NumCPU())

	for i, entry := range entries {
		g.Go(func() error {
			fr := parseFileStandalone(gctx, entry.path, entry.rel, hashMap, h.maxFileSizeKB, &mu, h.warn, &h.warnings)
			results[i] = fr
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}

	// Phase 4: serial accounting + batched write.
	var batch []*fileResult
	for i, fr := range results {
		rel := entries[i].rel
		ext := strings.ToLower(filepath.Ext(rel))
		ex := extract.ForExtension(ext)

		if fr == nil {
			if ex != nil {
				cached, ok := hashMap[rel]
				if ok && cached.ID > 0 {
					h.skipped++
					h.indexed++
					h.indexedFiles = append(h.indexedFiles, indexedFile{
						ID: cached.ID, Path: rel, Language: ex.Language(),
					})
				}
			}
			continue
		}

		batch = append(batch, fr)
		if len(batch) >= batchSize {
			if err := h.flushBatch(batch); err != nil {
				return err
			}
			batch = batch[:0]
		}
	}
	if len(batch) > 0 {
		return h.flushBatch(batch)
	}
	return nil
}

// flushBatch writes a batch of parsed file results in a single transaction.
// If any file's write fails, the entire batch rolls back and the failing
// file is excluded; remaining files are retried in a new batch.
func (h *harness) flushBatch(batch []*fileResult) error {
	// Snapshot slice lengths so we can undo in-memory appends on rollback.
	snapEdges := len(h.pendingEdges)
	snapIndexed := len(h.indexedFiles)
	snapChanged := len(h.changedFileIDs)

	var totalSyms int
	failedIdx := -1
	err := h.idx.InTx(h.ctx, func() error {
		for i, fr := range batch {
			syms, err := h.writeFileInner(fr)
			if err != nil {
				failedIdx = i
				return fmt.Errorf("%s: %w", fr.Rel, err)
			}
			totalSyms += syms
		}
		return nil
	})
	if err != nil && failedIdx >= 0 {
		// Transaction rolled back — undo in-memory appends from writeFileInner.
		h.pendingEdges = h.pendingEdges[:snapEdges]
		h.indexedFiles = h.indexedFiles[:snapIndexed]
		h.changedFileIDs = h.changedFileIDs[:snapChanged]

		h.warnf("%s: write failed, retrying batch without it: %v", batch[failedIdx].Rel, err)
		retry := make([]*fileResult, 0, len(batch)-1)
		for i, fr := range batch {
			if i != failedIdx {
				retry = append(retry, fr)
			}
		}
		if len(retry) > 0 {
			return h.flushBatch(retry)
		}
		return nil
	}
	if err != nil {
		return err
	}
	h.symbols += totalSyms
	h.indexed += len(batch)
	h.changed += len(batch)
	return nil
}

// fileResult holds the output of parseFile — everything needed to
// persist a file's symbols and edges without re-reading or re-parsing.
type fileResult struct {
	Rel       string
	Language  string
	Source    []byte
	Hash     string
	Symbols  []extract.EmittedSymbol
	Edges    []extract.EmittedEdge
}

// 100 files per SQLite transaction amortizes BEGIN/COMMIT overhead (~10x
// throughput gain) without risking large rollbacks on mid-batch failure.
const batchSize = 100

// processFile is the per-file pipeline: detect language, check size cap,
// compare hash for incremental skip, parse, run the extractor, and write.
// All per-file failures are soft — they bump h.warnings and return.
// Used by RunIncremental for small change sets.
func (h *harness) processFile(path, rel string) {
	h.seenPaths[rel] = true
	fr := h.parseFile(path, rel)
	if fr == nil {
		return
	}
	if err := h.writeFileResult(fr); err != nil {
		h.warnf("%s: write failed: %v", rel, err)
		return
	}
	h.indexed++
	h.changed++
}

// parseOpts controls how parseFileCore reports warnings and obtains a
// tree-sitter parser. The two callers (parallel walkTree, sequential
// RunIncremental) supply different implementations.
type parseOpts struct {
	ctx           context.Context
	maxFileSizeKB int
	warnf         func(string, ...any)
	parserFor     func(extract.Extractor) (*sitter.Parser, bool)
}

// parseFileCore is the shared parse+extract body. Returns nil for files
// that should be skipped (unknown language, read/parse/extract failure,
// or when skip returns true for the computed hash).
func parseFileCore(po parseOpts, path, rel string, skip func(hash string) bool) *fileResult {
	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)
	if ex == nil {
		return nil
	}

	if po.maxFileSizeKB > 0 {
		info, err := os.Stat(path)
		if err != nil {
			po.warnf("%s: stat failed: %v", rel, err)
			return nil
		}
		if info.Size() > int64(po.maxFileSizeKB)*1024 {
			po.warnf("%s: skipped (%d KB > %d KB max)", rel, info.Size()/1024, po.maxFileSizeKB)
			return nil
		}
	}

	if po.ctx.Err() != nil {
		return nil
	}

	source, err := os.ReadFile(path)
	if err != nil {
		po.warnf("%s: read failed: %v", rel, err)
		return nil
	}

	newHash := hashSource(source)
	if skip(newHash) {
		return nil
	}

	collected := &collector{}

	if raw, ok := ex.(extract.RawExtractor); ok {
		if err := safeExtractRaw(raw, source, rel, collected); err != nil {
			po.warnf("%s: extract failed: %v", rel, err)
			return nil
		}
	} else {
		parser, owned := po.parserFor(ex)
		if parser == nil {
			return nil
		}
		if owned {
			defer parser.Close()
		}

		tree := parser.Parse(source, nil)
		if tree == nil {
			po.warnf("%s: parse returned nil tree", rel)
			return nil
		}
		defer tree.Close()

		if tree.RootNode().HasError() {
			po.warnf("%s: parse errors present, extracting best-effort", rel)
		}

		if err := safeExtract(ex, tree, source, rel, collected); err != nil {
			po.warnf("%s: extract failed: %v", rel, err)
			return nil
		}
	}

	sort.SliceStable(collected.symbols, func(i, j int) bool {
		return len(collected.symbols[i].Qualified) < len(collected.symbols[j].Qualified)
	})

	return &fileResult{
		Rel:      rel,
		Language: ex.Language(),
		Source:   source,
		Hash:     newHash,
		Symbols:  collected.symbols,
		Edges:    collected.edges,
	}
}

// parseFileStandalone is the goroutine-safe parse function used by the
// parallel walkTree. It creates a fresh parser per call (no shared state).
func parseFileStandalone(
	ctx context.Context,
	path, rel string,
	hashMap map[string]sqlite.CachedFile,
	maxFileSizeKB int,
	warnMu *sync.Mutex,
	warnOut io.Writer,
	warnCount *int,
) *fileResult {
	wf := func(format string, args ...any) {
		warnMu.Lock()
		*warnCount++
		_, _ = fmt.Fprintf(warnOut, "warn: "+format+"\n", args...)
		warnMu.Unlock()
	}
	po := parseOpts{
		ctx:           ctx,
		maxFileSizeKB: maxFileSizeKB,
		warnf:         wf,
		parserFor: func(ex extract.Extractor) (*sitter.Parser, bool) {
			p := sitter.NewParser()
			if err := p.SetLanguage(ex.Grammar()); err != nil {
				p.Close()
				wf("%s: parser setup failed: %v", rel, err)
				return nil, false
			}
			return p, true // caller owns — parseFileCore will defer Close
		},
	}
	return parseFileCore(po, path, rel, func(hash string) bool {
		cached, ok := hashMap[rel]
		return ok && cached.Hash == hash
	})
}

// parseFile is the sequential parse function used by RunIncremental.
// It uses the harness's cached parsers and per-file DB lookups.
func (h *harness) parseFile(path, rel string) *fileResult {
	po := parseOpts{
		ctx:           h.ctx,
		maxFileSizeKB: h.maxFileSizeKB,
		warnf:         h.warnf,
		parserFor: func(ex extract.Extractor) (*sitter.Parser, bool) {
			p, err := h.parserFor(ex)
			if err != nil {
				h.warnf("%s: parser setup failed: %v", rel, err)
				return nil, false
			}
			return p, false // harness owns — do not close
		},
	}

	ext := strings.ToLower(filepath.Ext(path))
	ex := extract.ForExtension(ext)

	return parseFileCore(po, path, rel, func(hash string) bool {
		fileID, oldHash, metaErr := h.idx.FileMeta(h.ctx, rel)
		if metaErr != nil {
			h.warnf("%s: file meta lookup failed: %v", rel, metaErr)
			return false
		}
		if oldHash != hash {
			return false
		}
		h.skipped++
		h.indexed++
		if fileID > 0 && ex != nil {
			h.indexedFiles = append(h.indexedFiles, indexedFile{ID: fileID, Path: rel, Language: ex.Language()})
		}
		return true
	})
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

// writeFileResult wraps writeFileInner in its own transaction.
// Used by processFile (RunIncremental) where per-file transactions
// are appropriate for small change sets.
func (h *harness) writeFileResult(fr *fileResult) error {
	var symsWritten int
	err := h.idx.InTx(h.ctx, func() error {
		n, err := h.writeFileInner(fr)
		if err != nil {
			return err
		}
		symsWritten = n
		return nil
	})
	if err != nil {
		return err
	}
	h.symbols += symsWritten
	return nil
}

// writeFileInner persists one file's symbols and buffers its edges.
// Must be called inside an active transaction. Returns the number of
// symbols written.
func (h *harness) writeFileInner(fr *fileResult) (int, error) {
	fileID, err := h.idx.WriteFile(h.ctx, &model.File{
		Path:      fr.Rel,
		Language:  fr.Language,
		Hash:      fr.Hash,
		Symbols:   len(fr.Symbols),
		IndexedAt: time.Now().UTC(),
	})
	if err != nil {
		return 0, fmt.Errorf("write file: %w", err)
	}

	idByQualified := make(map[string]int64, len(fr.Symbols))
	parentByQualified := make(map[string]string, len(fr.Symbols))
	var symsWritten int
	for _, s := range fr.Symbols {
		var parentID *int64
		if s.ParentQualified != "" {
			if pid, ok := idByQualified[s.ParentQualified]; ok {
				parentID = &pid
			}
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
			Snippet:    snippetForLine(fr.Source, s.LineStart),
		}
		var id int64
		var werr error
		if h.symbolStmt != nil {
			id, werr = sqlite.ExecSymbolStmt(h.ctx, h.symbolStmt, row)
		} else {
			id, werr = h.idx.WriteSymbol(h.ctx, row)
		}
		if werr != nil {
			return 0, fmt.Errorf("write symbol %q: %w", s.Qualified, werr)
		}
		idByQualified[s.Qualified] = id
		parentByQualified[s.Qualified] = s.ParentQualified
		symsWritten++
	}

	for _, e := range fr.Edges {
		sourceID := idByQualified[e.SourceQualified]
		h.pendingEdges = append(h.pendingEdges, pendingEdge{
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

	h.indexedFiles = append(h.indexedFiles, indexedFile{ID: fileID, Path: fr.Rel, Language: fr.Language})
	h.changedFileIDs = append(h.changedFileIDs, fileID)
	return symsWritten, nil
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
	symIDs, err := h.idx.SymbolIDsForPaths(h.ctx, stale)
	if err != nil {
		return fmt.Errorf("collect stale symbol IDs: %w", err)
	}
	h.removedSymbolIDs = symIDs
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

	var written, unresolved, ambiguous int
	err = h.idx.InTx(h.ctx, func() error {
		edgeStmt, serr := h.idx.PrepareEdgeStmt(h.ctx)
		if serr != nil {
			return fmt.Errorf("prepare edge stmt: %w", serr)
		}
		defer func() { _ = edgeStmt.Close() }()

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
				unresolved++
				continue
			}
			if r.Ambiguous {
				ambiguous++
			}
			edge := &model.Edge{
				SourceID:   int64Ptr(pe.SourceID),
				TargetID:   r.SymbolID,
				Kind:       pe.Kind,
				FileID:     pe.FileID,
				Line:       pe.Line,
				Confidence: r.Confidence,
			}
			if edge.SourceID != nil {
				if _, werr := sqlite.ExecEdgeStmt(h.ctx, edgeStmt, edge); werr != nil {
					return fmt.Errorf("write edge source=%d target=%s: %w", pe.SourceID, pe.TargetName, werr)
				}
			} else {
				if _, werr := h.idx.WriteEdge(h.ctx, edge); werr != nil {
					return fmt.Errorf("write edge source=%d target=%s: %w", pe.SourceID, pe.TargetName, werr)
				}
			}
			written++
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.edges += written
	h.unresolved += unresolved
	h.ambiguous += ambiguous
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
