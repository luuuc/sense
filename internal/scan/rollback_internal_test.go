package scan

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// errInjectedWrite is the failure the fault store returns; a sentinel so a
// test can tell the injected failure apart from a real index error.
var errInjectedWrite = errors.New("injected WriteFile failure")

// faultWriteStore is the seam substitution that drives flushBatch's rollback
// branch. It embeds the real adapter — so it satisfies indexStore unchanged —
// and overrides exactly one named method, WriteFile, to fail once for a named
// path before delegating. One path, one shot: no counter map, no failure
// schedule, nothing that would turn it into the mock framework the cycle's
// no-gos forbid. A single forced mid-transaction failure is all flushBatch's
// snapshot/undo path needs.
type faultWriteStore struct {
	*sqlite.Adapter
	failPath string
	fired    bool
}

func (f *faultWriteStore) WriteFile(ctx context.Context, file *model.File) (int64, error) {
	if !f.fired && file.Path == f.failPath {
		f.fired = true
		return 0, errInjectedWrite
	}
	return f.Adapter.WriteFile(ctx, file)
}

// openIndex opens a real index under dir/.sense. The caller owns closing it,
// so a test can release the handle before a rescan opens its own.
func openIndex(t *testing.T, dir string) *sqlite.Adapter {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	a, err := sqlite.Open(context.Background(), filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return a
}

// writeSource writes content to dir/rel, creating parents.
func writeSource(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// classFile builds a one-symbol fileResult for a Ruby class, the minimum
// flushBatch needs to write a file row plus a symbol row.
func classFile(rel, class string) *fileResult {
	return &fileResult{
		Rel:      rel,
		Language: "ruby",
		Source:   []byte("class " + class + "\nend\n"),
		Hash:     "hash-" + rel,
		Symbols: []extract.EmittedSymbol{
			{Name: class, Qualified: class, Kind: "class", LineStart: 1, LineEnd: 2},
		},
	}
}

// TestFlushBatchPartialFailureRollsBackAndRetries forces a partial-batch
// failure inside flushBatch's transaction and asserts the result through the
// observable index, never through flushBatch's internal snapshot slices — so
// the test survives the 27-05 split that reshapes flushBatch.
//
// The batch holds two files; the fault store fails the second file's WriteFile
// once. The whole transaction must roll back (undoing the first file's writes
// and the in-memory appends), the failing file must be dropped with a warning,
// and the retry must re-commit the surviving file. After that a clean rescan —
// the real production path, no fault — must converge on the full, correct set.
func TestFlushBatchPartialFailureRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writeSource(t, dir, "a.rb", "class A\nend\n")
	writeSource(t, dir, "b.rb", "class B\nend\n")

	adapter := openIndex(t, dir)
	store := &faultWriteStore{Adapter: adapter, failPath: "b.rb"}
	h := &harness{
		ctx:       ctx,
		idx:       store,
		out:       io.Discard,
		warn:      io.Discard,
		collector: newWarningCollector(),
		progress:  &progress{},
		seenPaths: map[string]bool{},
	}

	// a.rb writes cleanly; b.rb's WriteFile fails mid-transaction.
	if err := h.flushBatch([]*fileResult{classFile("a.rb", "A"), classFile("b.rb", "B")}); err != nil {
		t.Fatalf("flushBatch should recover by retrying without the failing file, got %v", err)
	}
	if !store.fired {
		t.Fatal("fault was never triggered — fixture no longer exercises the rollback path")
	}

	// Tallies reflect one survivor, not a double-count from a botched undo.
	if h.indexed != 1 || h.changed != 1 || h.symbols != 1 {
		t.Errorf("after rollback: indexed=%d changed=%d symbols=%d, want 1/1/1", h.indexed, h.changed, h.symbols)
	}
	// The dropped file is surfaced as a warning, not swallowed.
	if got := h.collector.count(); got != 1 {
		t.Errorf("warnings = %d, want 1 (the dropped b.rb)", got)
	}

	// Observable index: A survived the rollback+retry, B did not.
	assertQualified(t, adapter, map[string]bool{"A": true, "B": false})

	// Release the faulted handle, then rescan through the real pipeline (its
	// own adapter, no fault). The dropped file must now converge into the index.
	if err := adapter.Close(); err != nil {
		t.Fatalf("close faulted adapter: %v", err)
	}
	if _, err := Run(ctx, Options{Root: dir, Output: io.Discard, Warnings: io.Discard}); err != nil {
		t.Fatalf("clean rescan: %v", err)
	}
	fresh := openIndex(t, dir)
	t.Cleanup(func() { _ = fresh.Close() })
	assertQualified(t, fresh, map[string]bool{"A": true, "B": true})
}

// TestFlushBatchWholeBatchFailsDropsAllCleanly covers flushBatch's
// retry-exhausted branch: when the only file in a batch fails, the retry list
// is empty, so flushBatch returns cleanly with nothing indexed and a warning
// for the dropped file.
func TestFlushBatchWholeBatchFailsDropsAllCleanly(t *testing.T) {
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })
	store := &faultWriteStore{Adapter: adapter, failPath: "only.rb"}
	h := &harness{
		ctx:       context.Background(),
		idx:       store,
		out:       io.Discard,
		warn:      io.Discard,
		collector: newWarningCollector(),
		progress:  &progress{},
		seenPaths: map[string]bool{},
	}

	if err := h.flushBatch([]*fileResult{classFile("only.rb", "Only")}); err != nil {
		t.Fatalf("flushBatch should return cleanly after the whole batch is dropped, got %v", err)
	}
	if h.indexed != 0 || h.symbols != 0 {
		t.Errorf("nothing should be indexed, got indexed=%d symbols=%d", h.indexed, h.symbols)
	}
	if got := h.collector.count(); got != 1 {
		t.Errorf("warnings = %d, want 1", got)
	}
	assertQualified(t, adapter, map[string]bool{"Only": false})
}

// TestFlushBatchBeginErrorPropagates covers flushBatch's non-rollback error
// path: a closed index fails the transaction's BEGIN before any file is
// written, so failedIdx is never set and the error propagates instead of
// triggering the (inapplicable) undo.
func TestFlushBatchBeginErrorPropagates(t *testing.T) {
	h := &harness{
		ctx:       context.Background(),
		idx:       newClosedAdapter(t),
		out:       io.Discard,
		warn:      io.Discard,
		collector: newWarningCollector(),
		progress:  &progress{},
		seenPaths: map[string]bool{},
	}
	if err := h.flushBatch([]*fileResult{classFile("a.rb", "A")}); err == nil {
		t.Fatal("expected error when the batch transaction cannot begin")
	}
}

// TestWriteFileResultInnerWriteFailsRollsBack covers writeFileResult's inner
// error path: the transaction begins, then the file write fails inside it, so
// writeFileInner's error surfaces and nothing is committed.
func TestWriteFileResultInnerWriteFailsRollsBack(t *testing.T) {
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })
	store := &faultWriteStore{Adapter: adapter, failPath: "a.rb"}
	h := &harness{ctx: context.Background(), idx: store, out: io.Discard, warn: io.Discard}

	if err := h.writeFileResult(classFile("a.rb", "A")); err == nil {
		t.Fatal("expected error when the file write fails inside the transaction")
	}
	assertQualified(t, adapter, map[string]bool{"A": false})
}

// assertQualified checks which qualified names the index holds, reading the
// committed database rather than any in-memory state.
func assertQualified(t *testing.T, adapter *sqlite.Adapter, want map[string]bool) {
	t.Helper()
	syms, err := adapter.Query(context.Background(), index.Filter{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	got := map[string]bool{}
	for _, s := range syms {
		got[s.Qualified] = true
	}
	for name, present := range want {
		if got[name] != present {
			t.Errorf("symbol %q present=%v, want %v (index has %v)", name, got[name], present, got)
		}
	}
}

// TestWriteFileResultErrorReturnsToCaller covers writeFileResult's error
// return: when the per-file transaction cannot even begin (closed index), the
// error propagates so processFile can warn rather than silently dropping work.
func TestWriteFileResultErrorReturnsToCaller(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	if err := h.writeFileResult(classFile("a.rb", "A")); err == nil {
		t.Fatal("expected error from writeFileResult on a closed index")
	}
}

// faultPrepareEdgeStore fails PrepareEdgeStmt once the resolve transaction has
// begun, so the failure lands inside InTx rather than before it. One named
// method, one shot — the same fault-once discipline as faultWriteStore.
type faultPrepareEdgeStore struct {
	*sqlite.Adapter
}

func (f *faultPrepareEdgeStore) PrepareEdgeStmt(context.Context) (*sql.Stmt, error) {
	return nil, errors.New("injected PrepareEdgeStmt failure")
}

// TestResolveAndWriteEdgesPrepareStmtFails covers resolveAndWriteEdges'
// in-transaction error path: SymbolRefs loads fine and the transaction begins,
// then PrepareEdgeStmt fails, so the closure's error rolls the transaction back
// and surfaces to the caller. Exercising it needs at least one pending edge so
// the function does not short-circuit on an empty buffer.
func TestResolveAndWriteEdgesPrepareStmtFails(t *testing.T) {
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })
	h := &harness{
		ctx:          context.Background(),
		idx:          &faultPrepareEdgeStore{Adapter: adapter},
		warn:         io.Discard,
		pendingEdges: []pendingEdge{{SourceID: 1, TargetName: "X", Kind: model.EdgeCalls}},
	}
	if err := h.resolveAndWriteEdges(); err == nil {
		t.Fatal("expected error when PrepareEdgeStmt fails inside the transaction")
	}
}

// faultWriteEdgeStore fails WriteEdge once, the path resolveAndWriteEdges takes
// for a file-level edge (one with no source symbol). Single named method,
// fail-once.
type faultWriteEdgeStore struct {
	*sqlite.Adapter
	fired bool
}

func (f *faultWriteEdgeStore) WriteEdge(ctx context.Context, e *model.Edge) (int64, error) {
	if !f.fired {
		f.fired = true
		return 0, errors.New("injected WriteEdge failure")
	}
	return f.Adapter.WriteEdge(ctx, e)
}

// TestResolveAndWriteEdgesFileLevelEdgeWriteFails covers the file-level edge
// write error: a pending edge with no source symbol resolves to a seeded
// target, takes the WriteEdge branch (not the prepared-statement branch), and
// the write fails — the error must surface from the transaction.
func TestResolveAndWriteEdgesFileLevelEdgeWriteFails(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })

	// Seed a target the file-level edge can resolve to.
	fileID, err := adapter.WriteFile(ctx, &model.File{Path: "x.rb", Language: "ruby", Hash: "h", IndexedAt: time.Now().UTC()})
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := adapter.WriteSymbol(ctx, &model.Symbol{FileID: fileID, Name: "X", Qualified: "X", Kind: "class", LineStart: 1, LineEnd: 1}); err != nil {
		t.Fatalf("seed symbol: %v", err)
	}

	h := &harness{
		ctx:  ctx,
		idx:  &faultWriteEdgeStore{Adapter: adapter},
		warn: io.Discard,
		// SourceID 0 ⇒ a file-level edge ⇒ the WriteEdge branch. A high base
		// confidence keeps "X" out of the bare-guess path so the exact
		// qualified lookup resolves it.
		pendingEdges: []pendingEdge{{SourceID: 0, TargetName: "X", Kind: model.EdgeInherits, FileID: fileID, Confidence: 1.0}},
	}
	if err := h.resolveAndWriteEdges(); err == nil {
		t.Fatal("expected error when the file-level edge write fails")
	}
}

// TestResolveAndWriteEdgesErrorReturnsToCaller covers resolveAndWriteEdges'
// load-symbols error return: with a pending edge queued but the index closed,
// SymbolRefs fails and the error surfaces instead of writing a partial graph.
func TestResolveAndWriteEdgesErrorReturnsToCaller(t *testing.T) {
	h := &harness{
		ctx:          context.Background(),
		idx:          newClosedAdapter(t),
		warn:         io.Discard,
		pendingEdges: []pendingEdge{{SourceID: 1, TargetName: "X", Kind: model.EdgeCalls}},
	}
	if err := h.resolveAndWriteEdges(); err == nil {
		t.Fatal("expected error from resolveAndWriteEdges on a closed index")
	}
}
