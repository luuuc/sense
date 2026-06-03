// Package freshen keeps a project's .sense index current in the
// background. It owns a single write adapter and serializes every index
// write behind one mutex, so a debounced re-index batch and a per-query
// read-repair never contend on the SQLite connection. Both `sense scan
// --watch` (the headless CLI path) and the `sense mcp` server are thin
// clients of a Service: they perform the initial scan and own process
// lifecycle, while the Service owns watching, incremental re-index, and
// background embedding.
package freshen

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/luuuc/sense/internal/config"
	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/mcpio"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// Config configures a Service. Root is the only required field.
type Config struct {
	Root              string
	EmbeddingsEnabled bool
	DebounceMs        int                  // 0 → DefaultDebounceMs (or config.yml)
	WatchState        *mcpio.WatchState    // updated by the Service; may be nil
	Log               func(string, ...any) // nil → discard
	// OnEmbedded, if set, is invoked after a background embed writes n>0
	// embeddings, so a co-hosted reader can refresh its in-memory vectors.
	OnEmbedded func(ctx context.Context, n int)
}

// Service watches Root and keeps its .sense index fresh. It is created
// with New (which opens the write adapter), started with Start, and torn
// down with Stop. It assumes the index already exists — the caller runs
// the initial scan.
type Service struct {
	root              string
	embeddingsEnabled bool
	debounceMs        int
	maxFileSizeKB     int
	log               func(string, ...any)
	watchState        *mcpio.WatchState
	onEmbedded        func(ctx context.Context, n int)

	adapter  *sqlite.Adapter
	matcher  *ignore.Matcher
	parsers  *scan.ParserCache
	embedCtl *embedController
	watcher  *Watcher
	lock     *writerLock
	pOpts    processOptions

	// Git fast path: a watch on .git/HEAD so a branch switch re-indexes
	// once from a git diff instead of from an fsnotify storm. lastHead is
	// owned solely by the git goroutine.
	gitFsw   *fsnotify.Watcher
	lastHead string

	// writeMu serializes all index writes (batch re-index, read-repair,
	// and the git fast path) so two write sequences never interleave on
	// the single shared connection.
	writeMu sync.Mutex

	mu      sync.Mutex // guards started/writing/cancel
	started bool
	writing bool // true when this process holds the single-writer lock
	cancel  context.CancelFunc
	wg      sync.WaitGroup // background goroutines: batch loop + git head
}

// NewService opens the write adapter and builds the ignore matcher for
// Root. It does not start watching; call Start for that.
func NewService(cfg Config) (*Service, error) {
	root := cfg.Root
	if root == "" {
		root = "."
	}
	root, _ = filepath.Abs(root)

	log := cfg.Log
	if log == nil {
		log = func(string, ...any) {}
	}

	fcfg, err := config.Load(root)
	if err != nil {
		return nil, fmt.Errorf("freshen: load config: %w", err)
	}

	matcher, err := ignore.Build(root, fcfg.Ignore)
	if err != nil {
		return nil, fmt.Errorf("freshen: build ignore matcher: %w", err)
	}

	debounceMs := cfg.DebounceMs
	if debounceMs <= 0 {
		debounceMs = fcfg.Scan.WatchDebounceMs
	}
	if debounceMs <= 0 {
		debounceMs = DefaultDebounceMs
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(context.Background(), dbPath)
	if err != nil {
		return nil, fmt.Errorf("freshen: open index: %w", err)
	}

	return &Service{
		root:              root,
		embeddingsEnabled: cfg.EmbeddingsEnabled,
		debounceMs:        debounceMs,
		maxFileSizeKB:     fcfg.Scan.MaxFileSizeKB,
		log:               log,
		watchState:        cfg.WatchState,
		onEmbedded:        cfg.OnEmbedded,
		adapter:           adapter,
		matcher:           matcher,
	}, nil
}

// Start launches the file watcher, the debounce loop, and the background
// embed controller. Background work runs until Stop (or ctx cancellation).
// If the watcher cannot be created, Start closes the adapter and returns
// the error so the caller can degrade to read-only.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}

	// Arbitrate the single-writer role across processes. If another live
	// process already owns indexing, run read-only: serve queries (the MCP
	// server uses a separate read adapter), but start no watcher or embed.
	senseDir := filepath.Join(s.root, ".sense")
	lock, acquired, lerr := acquireWriterLock(senseDir)
	if lerr != nil {
		s.log("sense: writer-lock check failed, running read-only: %v", lerr)
	}
	if !acquired {
		s.started = true
		s.writing = false
		s.log("sense: another process owns indexing for this repo; running read-only")
		return nil
	}
	s.lock = lock

	w, err := New(s.root, s.matcher)
	if err != nil {
		lock.release()
		_ = s.adapter.Close()
		return fmt.Errorf("freshen: create watcher: %w", err)
	}
	s.watcher = w

	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	s.parsers = scan.NewParserCache()

	s.embedCtl = &embedController{
		enabled:    s.embeddingsEnabled,
		ctx:        runCtx,
		log:        s.log,
		onEmbedded: s.afterEmbed,
		checkDebt: func(ctx context.Context) (int, error) {
			return s.adapter.EmbeddingDebtCount(ctx)
		},
		runEmbed: func(ctx context.Context) (int, error) {
			return scan.EmbedPending(ctx, s.adapter, s.root)
		},
	}

	if s.watchState != nil {
		s.watchState.Set(true, time.Now().UTC())
	}
	s.embedCtl.Start()
	s.markIndexed(runCtx) // initial last-indexed + pending for status

	batches := Loop(runCtx, w, s.debounceMs)
	s.pOpts = processOptions{
		root:           s.root,
		matcher:        s.matcher,
		maxFileSizeKB:  s.maxFileSizeKB,
		parsers:        s.parsers,
		idx:            s.adapter,
		log:            s.log,
		runIncremental: scan.RunIncremental,
		cancelEmbed:    s.embedCtl.Cancel,
		startEmbed:     s.embedCtl.Start,
	}

	s.wg.Add(1)
	go s.run(runCtx, batches)

	// Git fast path: when the working tree is a git repo, watch .git/HEAD
	// so a branch switch is re-indexed once from a git diff. Failure to set
	// it up is non-fatal — the general watcher still catches file changes.
	s.lastHead = gitHead(s.root)
	if s.lastHead != "" {
		if gw, gerr := newGitHeadWatcher(s.root); gerr == nil {
			s.gitFsw = gw
			s.wg.Add(1)
			go s.runGitHead(runCtx)
		}
	}

	s.started = true
	s.writing = true
	s.log("sense: watching for changes (debounce=%dms)", s.debounceMs)
	return nil
}

// Writing reports whether this Service holds the single-writer role. A
// read-only Service (another process owns the lock) serves queries but
// performs no background re-index, embed, or read-repair.
func (s *Service) Writing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writing
}

// RepairFiles re-indexes the given relative paths inline, structural-only,
// for the per-query read-repair backstop: a query whose touched files are
// stale refreshes just those before answering. It shares the single write
// mutex with the background batch loop, so the two never interleave on the
// connection, and it briefly pauses the embed controller exactly as a
// batch does. It is a no-op on a read-only Service (the writer process
// owns freshness) or for an empty path set. Embeddings are intentionally
// skipped: repair restores structure on the hot path; the watcher's
// embed controller closes embedding debt in the background.
func (s *Service) RepairFiles(ctx context.Context, rels []string) error {
	if len(rels) == 0 || !s.Writing() {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// Re-check under the write mutex: Stop closes the adapter while holding
	// writeMu, so a nil adapter here means we raced a shutdown — bail rather
	// than write through a closed handle. (RepairFiles runs on the query
	// goroutine, which Stop's wg.Wait does not drain.)
	if s.adapter == nil {
		return nil
	}

	if s.embedCtl != nil {
		s.embedCtl.Cancel()
		defer s.embedCtl.Start()
	}
	_, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:              s.root,
		Idx:               s.adapter,
		Matcher:           s.matcher,
		MaxFileSizeKB:     s.maxFileSizeKB,
		EmbeddingsEnabled: false,
		Output:            io.Discard,
		Warnings:          io.Discard,
		Changed:           rels,
		Parsers:           s.parsers,
	})
	return err
}

// run consumes debounced batches and re-indexes them under the write
// mutex until the context is cancelled or the batch channel closes.
func (s *Service) run(ctx context.Context, batches <-chan Batch) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case batch, ok := <-batches:
			if !ok {
				return
			}
			s.writeMu.Lock()
			_ = processBatch(ctx, batch, s.pOpts)
			s.writeMu.Unlock()
			s.markIndexed(ctx)
		}
	}
}

// markIndexed records the time of the latest background re-index and the
// current embedding debt into the shared watch state, so the MCP server can
// report live freshness (last_update, pending). It is a no-op without a
// watch state.
func (s *Service) markIndexed(ctx context.Context) {
	if s.watchState == nil {
		return
	}
	pending := 0
	if s.embeddingsEnabled {
		if n, err := s.adapter.EmbeddingDebtCount(ctx); err == nil {
			pending = n
		}
	}
	s.watchState.SetIndexed(time.Now().UTC(), pending)
}

// afterEmbed runs when a background embed finishes writing n>0 embeddings.
// It refreshes the watch state's pending count (the debt those embeddings
// just paid down) before forwarding to the external OnEmbedded callback, so
// status stops reporting the pre-embed backlog. The initial markIndexed at
// startup snapshots the full debt before the embed runs; without this, that
// stale count would persist until the next file-change batch.
func (s *Service) afterEmbed(ctx context.Context, n int) {
	s.markIndexed(ctx)
	if s.onEmbedded != nil {
		s.onEmbedded(ctx, n)
	}
}

// Stop cancels background work, waits for it to drain, and closes the
// write adapter. It is idempotent and safe to call even if Start failed.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		// Start never ran (or already stopped); still release the adapter.
		if s.adapter != nil {
			_ = s.adapter.Close()
			s.adapter = nil
		}
		return
	}
	s.started = false
	s.writing = false

	// Writer-mode resources only exist when this process held the lock.
	if s.cancel != nil {
		s.cancel()
		s.wg.Wait() // background goroutines have exited; no more writes start
	}
	if s.embedCtl != nil {
		s.embedCtl.Cancel()
	}
	if s.gitFsw != nil {
		_ = s.gitFsw.Close()
		s.gitFsw = nil
	}
	if s.watcher != nil {
		_ = s.watcher.Close()
	}
	if s.parsers != nil {
		s.parsers.Close()
	}
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
	if s.watchState != nil {
		s.watchState.Set(false, time.Time{})
	}
	// Close the adapter under the write mutex so an in-flight RepairFiles
	// (which runs on a query goroutine, outside s.wg) never writes through a
	// closed handle. RepairFiles re-checks s.adapter == nil under the same
	// mutex. Lock ordering is safe: Stop takes s.mu then writeMu, while a
	// repair holds writeMu without holding s.mu.
	s.writeMu.Lock()
	if s.adapter != nil {
		_ = s.adapter.Close()
		s.adapter = nil
	}
	s.writeMu.Unlock()
}
