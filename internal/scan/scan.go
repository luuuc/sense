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
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"
	"golang.org/x/sync/errgroup"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/extract"
	_ "github.com/luuuc/sense/internal/extract/languages" // register every extractor
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/profile"
	"github.com/luuuc/sense/internal/resolve"
	"github.com/luuuc/sense/internal/setup"
	"github.com/luuuc/sense/internal/sqlite"
	"github.com/luuuc/sense/internal/summary"
)

// Options bounds a scan run. Zero values select sensible defaults.
//
// Output and Warnings are distinct sinks so per-file diagnostics don't
// interleave with the one-line summary:
//   - Summary (one line per run, machine-parseable) goes to Output.
//   - Per-file warnings (parse errors, read failures, etc.) go to Warnings.
//
// A caller that cares only about the summary can leave Warnings at its
// default (os.Stderr) and pipe Output on its own; callers that want a
// quiet run can redirect Warnings to io.Discard.
type Options struct {
	Root              string    // working-tree root (default: ".")
	Sense             string    // sense dir (default: "<Root>/.sense")
	Output            io.Writer // summary-line sink (default: os.Stderr)
	Warnings          io.Writer // per-file warning sink (default: os.Stderr)
	Quiet             bool      // suppress progress display and warning hints; forces non-TTY behavior
	EmbeddingsEnabled bool      // when true, embeddings are part of the index pipeline
	Embed             bool      // block until embeddings complete; requires EmbeddingsEnabled. When false, embeddings are deferred and a watermark is written for the MCP server to pick up.
	Rebuild           bool      // drop and recreate the index from source (preserving lifetime metrics) before walking, so every file is re-parsed and re-resolved even when its hash is unchanged
}

// PhaseTiming records how long each scan phase took.
type PhaseTiming struct {
	Walk              time.Duration
	RemoveStale       time.Duration
	ResolveEdges      time.Duration
	SatisfyInterfaces time.Duration
	AssociateTests    time.Duration
	NamingConventions time.Duration
	Temporal          time.Duration
	Embed             time.Duration
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

// Run ensures the .sense directory and index.db exist, walks the
// working tree, parses each file with a registered extractor, and
// writes symbols + intra-file edges into the index. Returns the
// summary and any fatal error. Per-file parse/extract errors are
// non-fatal: a warning is logged, the scan continues, and the result's
// Warnings counter is incremented.
//
//nolint:gocyclo,gocognit // 27-06: retired by the scan-pipeline split
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

	absRoot, _ := filepath.Abs(root)
	if opts.Embed {
		_, _ = fmt.Fprintf(out, "Indexing %s (with embeddings)...\n", absRoot)
	} else {
		_, _ = fmt.Fprintf(out, "Indexing %s...\n", absRoot)
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

	// --rebuild drops the derived tables (preserving lifetime metrics) before
	// the walk, so the emptied sense_files forces every file to re-parse and
	// re-resolve even when its hash is unchanged. Skipped when Open already
	// rebuilt for a schema mismatch — that path left the same empty state.
	if opts.Rebuild && !idx.Rebuilt {
		if err := idx.Rebuild(ctx); err != nil {
			return nil, fmt.Errorf("rebuild index: %w", err)
		}
		_, _ = fmt.Fprintf(out, "rebuilding index from source (lifetime metrics preserved)\n")
	}

	prog := newProgress(out, opts.Quiet)
	wc := newWarningCollector()

	h := &harness{
		ctx:            ctx,
		idx:            idx,
		out:            out,
		warn:           warn,
		root:           root,
		progress:       prog,
		collector:      wc,
		parsers:        map[string]*sitter.Parser{},
		matcher:        matcher,
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
		maxFileSizeKB:  cfg.Scan.MaxFileSizeKB,
		seenPaths:      map[string]bool{},
		newEmbedder:    defaultEmbedderFactory,
	}
	defer h.closeParsers()

	prog.start()
	defer prog.stop()

	start := time.Now()
	var phases PhaseTiming

	t0 := start
	if err := h.walkTree(root); err != nil {
		return nil, err
	}
	phases.Walk = time.Since(t0)

	if fw := detectFrameworks(root); len(fw) > 0 {
		if err := idx.WriteMeta(ctx, "frameworks", frameworksJSON(fw)); err != nil {
			_, _ = fmt.Fprintf(warn, "warn: write frameworks meta: %v\n", err)
		}
	} else {
		_ = idx.DeleteMeta(ctx, "frameworks")
	}

	// Each language is an independent sense_meta key, so map-iteration order is
	// irrelevant — no write depends on another.
	for lang, set := range h.dispatchNames {
		warnMetaWrite(warn, "dispatch-names:"+lang, writeDispatchNames(ctx, idx, lang, set))
	}
	for lang, set := range h.mentionedNames {
		warnMetaWrite(warn, "mentioned-names:"+lang, writeMentionedNames(ctx, idx, lang, set))
	}
	warnMetaWrite(warn, "harvested-langs", writeHarvestedLangs(ctx, idx, h.harvestedLangs))
	warnMetaWrite(warn, "cgo-exports", writeCgoExports(ctx, idx, h.cgoExports))
	warnMetaWrite(warn, "rust-exports", writeRustExports(ctx, idx, h.rustExports))
	warnMetaWrite(warn, "rust-test-symbols", writeRustTestSymbols(ctx, idx, h.rustTestSymbols))
	warnMetaWrite(warn, "rust-trait-impl-methods", writeRustTraitImplMethods(ctx, idx, h.rustTraitMethods))
	warnMetaWrite(warn, "rust-allow-dead", writeRustAllowDead(ctx, idx, h.rustAllowDead))
	warnMetaWrite(warn, "ts-decorated", writeTSDecorated(ctx, idx, h.tsDecorated))
	warnMetaWrite(warn, "ts-default-exports", writeTSDefaultExports(ctx, idx, h.tsDefaultExports))
	warnMetaWrite(warn, "py-decorated", writePythonDecorated(ctx, idx, h.pyDecorated))
	warnMetaWrite(warn, "py-routes", writePythonRoutes(ctx, idx, h.pyRoutes))
	warnMetaWrite(warn, "py-django", writePythonDjango(ctx, idx, h.pyDjango))
	warnMetaWrite(warn, "py-all-exports", writePythonAllExports(ctx, idx, h.pyAllExports))
	warnMetaWrite(warn, "langspec-annotated", writeLangspecAnnotated(ctx, idx, h.lsAnnotated))
	// A pre-feature index carried single bare union keys (no language suffix).
	// The per-language reader globs `*:<lang>` and ignores them, but delete them
	// on every full scan so an in-place upgrade leaves no stale meta to mislead a
	// human inspecting sense_meta. Idempotent: absent keys are a no-op.
	_ = idx.DeleteMeta(ctx, dispatchNamesMetaKey)
	_ = idx.DeleteMeta(ctx, mentionedNamesMetaKey)

	t0 = time.Now()
	if err := h.removeStaleFiles(); err != nil {
		return nil, err
	}
	phases.RemoveStale = time.Since(t0)

	prog.setPhase("Resolving edges...", 0)
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

	prog.setPhase("Associating tests...", 0)
	t0 = time.Now()
	if err := h.associateTests(); err != nil {
		return nil, err
	}
	phases.AssociateTests = time.Since(t0)

	t0 = time.Now()
	if err := h.namingConventionEdges(); err != nil {
		return nil, err
	}
	phases.NamingConventions = time.Since(t0)

	t0 = time.Now()
	if err := h.extractTemporalCoupling(); err != nil {
		return nil, err
	}
	phases.Temporal = time.Since(t0)

	var modelMigrated bool
	if opts.EmbeddingsEnabled {
		if changed, merr := h.migrateEmbeddingModel(); merr != nil {
			_, _ = fmt.Fprintf(warn, "warn: embedding model migration: %v\n", merr)
		} else if changed {
			modelMigrated = true
			_, _ = fmt.Fprintf(out, "embedding model changed to %s — re-embedding all symbols\n", embed.ModelID)
		}
	}

	if opts.EmbeddingsEnabled && opts.Embed {
		t0 = time.Now()
		if err := h.embedSymbols(); err != nil {
			return nil, err
		}

		// Backfill embeddings for symbols indexed in prior scans
		// that were never embedded (changedFileIDs was empty).
		if pending, perr := idx.EmbeddingDebtCount(ctx); perr == nil && pending > 0 {
			n, eerr := EmbedPending(ctx, idx, root)
			if eerr != nil {
				return nil, fmt.Errorf("embed pending symbols: %w", eerr)
			}
			h.embedded += n
		}
		phases.Embed = time.Since(t0)

		if derr := idx.DeleteMeta(ctx, "embedding_watermark"); derr != nil {
			_, _ = fmt.Fprintf(warn, "warn: clear embedding watermark: %v\n", derr)
		}
	}

	var embeddingDebt int
	if opts.EmbeddingsEnabled && !opts.Embed && (h.changed > 0 || modelMigrated) {
		ts := time.Now().UTC().Format(time.RFC3339)
		if err := idx.WriteMeta(ctx, "embedding_watermark", ts); err != nil {
			_, _ = fmt.Fprintf(warn, "warn: write embedding watermark: %v\n", err)
		}
		if debt, derr := idx.EmbeddingDebtCount(ctx); derr == nil {
			embeddingDebt = debt
		}
	}

	if prof, perr := profile.Compute(ctx, idx.DB()); perr == nil {
		if serr := profile.Store(ctx, idx.DB(), prof); serr != nil {
			_, _ = fmt.Fprintf(warn, "warn: store profile: %v\n", serr)
		}
	}

	if serr := summary.Generate(ctx, idx, senseDir, root); serr != nil {
		_, _ = fmt.Fprintf(warn, "warn: generate summary: %v\n", serr)
	}

	if err := idx.WriteMeta(ctx, "last_scan_at", time.Now().UTC().Format(time.RFC3339)); err != nil {
		_, _ = fmt.Fprintf(warn, "warn: write last_scan_at: %v\n", err)
	}

	if err := idx.StampSchemaVersion(ctx); err != nil {
		return nil, err
	}
	prog.stop()

	if err := wc.writeLog(senseDir); err != nil {
		_, _ = fmt.Fprintf(warn, "warn: write warnings log: %v\n", err)
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
		Warnings:       wc.count(),
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
	if res.Warnings > 0 && !opts.Quiet {
		_, _ = fmt.Fprintf(out, "%d warnings — see .sense/warnings.log\n", res.Warnings)
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
	ctx       context.Context
	idx       indexStore
	out       io.Writer // summary-line sink
	warn      io.Writer // infrastructure warning sink (not per-file)
	root      string    // repository root directory
	progress  *progress
	collector *warningCollector
	parsers   map[string]*sitter.Parser

	matcher        *ignore.Matcher
	defaultMatcher *ignore.Matcher
	maxFileSizeKB  int

	// newEmbedder constructs the embedders for pass 3. Defaulted from
	// defaultEmbedderFactory (the bundled ONNX model) so production is
	// unchanged; a test swaps the package default via SetEmbedderFactory to
	// drive embedding without ONNX. The field threads that choice from Run
	// into embedSymbols without touching the exported Options.
	newEmbedder embedderFactory

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

	// dispatchNames accumulates each language's set of reflective
	// dispatch-target names streamed by extractors during the walk, keyed by
	// language. Written to per-language sense_meta keys after the walk so the
	// dead-code arbiter keeps a reflectively-reachable symbol open-world only
	// against its OWN language's literals. Only populated for changed files this
	// scan; merged with the persisted per-language set so an unchanged file's
	// names are not lost.
	dispatchNames map[string]map[string]struct{}

	// mentionedNames accumulates each language's broad set of bare names that
	// language's code mentions (every identifier/symbol token except definition
	// names), keyed by language. Written to per-language sense_meta keys after
	// the walk so the dead-code arbiter's soundness gate earns `dead` for a
	// symbol only when its name is mentioned nowhere in its OWN language a hidden
	// caller could be. Merged with the persisted per-language set so an unchanged
	// file's mentions survive.
	mentionedNames map[string]map[string]struct{}

	// cgoExports is the project-wide set of Go function names marked with a cgo
	// `//export` directive. Written to the cgo_exports sense_meta key so the
	// dead-code Go voice keeps a C-callable function open-world (go_cgo) rather
	// than earning it `dead` off its absent Go caller. Flat (not per-language):
	// cgo is Go-only. Only populated for changed files this scan; merged with the
	// persisted set so an unchanged file's exports are not lost.
	cgoExports map[string]struct{}

	// rustExports is the project-wide set of Rust function/static names whose
	// reachability the edge graph cannot see (`#[no_mangle]` / `#[export_name]`
	// functions, `#[no_mangle]` / `#[used]` statics). rustTestSymbols is the set
	// of Rust test-only symbol names (`#[test]` / `#[bench]`, or nested under a
	// `#[cfg(test)]` module). Both are written to flat sense_meta keys so the
	// dead-code Rust voice keeps such a symbol open-world (rust_ffi / rust_used /
	// rust_test). Flat (not per-language): these are Rust-only attributes, like
	// cgo. Merged with the persisted set so an unchanged file's names survive.
	// rustTraitMethods is the set of method names defined in `impl Trait for Type`
	// blocks (the sound trait-impl signal, including external traits).
	// rustAllowDead is the set of item names annotated `#[allow(dead_code)]` /
	// `#[allow(unused)]` (intentionally retained; never in the cargo oracle).
	rustExports      map[string]struct{}
	rustTestSymbols  map[string]struct{}
	rustTraitMethods map[string]struct{}
	rustAllowDead    map[string]struct{}

	// tsDecorated is the set of TS/JS class/method names carrying a decorator
	// (`@Component` / `@Injectable` / route-method decorators); tsDefaultExports
	// is the set of names bound by an `export default` form. Both are written to
	// flat sense_meta keys so the dead-code TS voice keeps a decorated symbol
	// open-world (ts_decorator) and labels a default export ts_default_export.
	// Flat (not per-language): these concepts span the .ts/.tsx/.js family.
	tsDecorated      map[string]struct{}
	tsDefaultExports map[string]struct{}

	// pyDecorated / pyRoutes / pyDjango are the Python decorator-reach sets
	// (any decorator; the route-decorator subset; the Django-dispatch subset),
	// and pyAllExports is the set of names modules declare public via `__all__`.
	// All four are written to flat sense_meta keys so the dead-code Python voice
	// keeps a decorated / routed / Django-dispatched / declared-public symbol
	// open-world (py_decorator / py_route / py_django / py_all_export).
	pyDecorated  map[string]struct{}
	pyRoutes     map[string]struct{}
	pyDjango     map[string]struct{}
	pyAllExports map[string]struct{}

	// lsAnnotated is the set of langspec (Java/Kotlin/C#/Scala/C++/PHP/C)
	// class/method/function names carrying any annotation or attribute. Written to
	// a flat sense_meta key so the dead-code langspec voice keeps an annotated
	// symbol open-world (ls_annotated): with no per-framework voice, a DI
	// container, test runner, or router may dispatch it with no source caller.
	lsAnnotated map[string]struct{}

	// harvestedLangs is the set of languages whose mention harvest RAN this
	// scan — every indexed file whose extractor is an extract.MentionHarvester
	// marks its language here, regardless of how many names it produced. Written
	// to the harvested_langs sense_meta key so the dead-code gate can refuse
	// `dead` for a language that never harvested while still allowing it for a
	// language that harvested an empty set. Distinct from the mentionedNames
	// keyset precisely so the two can differ.
	harvestedLangs map[string]struct{}

	// changedFileIDs collects file IDs that were re-indexed this scan
	// (new or hash-changed). Used by pass 3 to scope embedding work.
	changedFileIDs []int64

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

func (h *harness) addWarning(kind warningKind, format string, args ...any) {
	h.collector.add(kind, fmt.Sprintf(format, args...))
	h.progress.incWarnings()
}

// markHarvested records ex's language as one whose mention harvest ran, when ex
// is an extract.MentionHarvester. Called for every indexed file (fresh or
// cached) so the harvested_langs set reflects the index, not just this scan's
// freshly-parsed files. A non-harvesting extractor (no MentionHarvester) is a
// no-op, so its language stays absent and its symbols fail closed.
func (h *harness) markHarvested(ex extract.Extractor) {
	mh, ok := ex.(extract.MentionHarvester)
	if !ok || !mh.HarvestsMentions() {
		return
	}
	if h.harvestedLangs == nil {
		h.harvestedLangs = map[string]struct{}{}
	}
	h.harvestedLangs[ex.Language()] = struct{}{}
}

type walkEntry struct {
	path string
	rel  string
}

// walkTree walks root depth-first. Dot-prefixed directories (.git,
// .vscode) and the .sense directory are always skipped. Paths matched
// by the ignore matcher are skipped. Symlinks are not followed.
//
//nolint:gocyclo,gocognit // 27-06: retired by the scan-pipeline split
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

	h.progress.setPhase("Scanning...", int64(len(entries)))

	// Phase 2: pre-load file hashes for incremental skip.
	hashMap, err := h.idx.FileHashMap(h.ctx)
	if err != nil {
		return fmt.Errorf("load file hashes: %w", err)
	}

	// Phase 3: parallel parse+extract.
	results := make([]*fileResult, len(entries))
	g, gctx := errgroup.WithContext(h.ctx)
	g.SetLimit(runtime.NumCPU())

	for i, entry := range entries {
		g.Go(func() error {
			fr := parseFileStandalone(gctx, entry.path, entry.rel, hashMap, h.maxFileSizeKB, h.collector, h.progress)
			results[i] = fr
			h.progress.inc()
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
					// A cached file is still indexed, so its language's harvest
					// is still represented in this index.
					h.markHarvested(ex)
				}
			}
			continue
		}
		h.markHarvested(ex)
		h.partitionHarvestedNames(fr)

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

		h.addWarning(warnWriteFailed, "%s (%v)", batch[failedIdx].Rel, err)
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
	Rel               string
	Language          string
	Source            []byte
	Hash              string
	Symbols           []extract.EmittedSymbol
	Edges             []extract.EmittedEdge
	DispatchNames     []string
	MentionedNames    []string
	CgoExports        []string
	RustExports       []string
	RustTestSymbols   []string
	RustTraitMethods  []string
	RustAllowDead     []string
	TSDecorated       []string
	TSDefaultExports  []string
	PyDecorated       []string
	PyRoutes          []string
	PyDjango          []string
	PyAllExports      []string
	LangspecAnnotated []string
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
		h.addWarning(warnWriteFailed, "%s (%v)", rel, err)
		return
	}
	h.indexed++
	h.changed++
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
			Receiver:   s.Receiver,
			ParentID:   parentID,
			LineStart:  s.LineStart,
			LineEnd:    s.LineEnd,
			Docstring:  capDocstring(s.Docstring),
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
