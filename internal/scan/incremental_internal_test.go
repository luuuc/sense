package scan

import (
	"context"
	"io"
	"testing"

	"github.com/luuuc/sense/internal/model"
)

// TestRemoveDeletedTxError covers the transaction-failure path of removeDeleted:
// when the index can't open a transaction the wrapped error surfaces rather than
// a partial delete. Driven through a closed adapter, the same real-condition
// lever the temporal error tests use.
func TestRemoveDeletedTxError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), out: io.Discard, warn: io.Discard}
	if err := h.removeDeleted([]string{"gone.go"}); err == nil {
		t.Fatal("expected error removing files from a closed index")
	}
}

// TestRemoveDeletedDeleteFileError covers removeDeleted's in-transaction failure
// (incremental.go:111-113): the transaction opens, then deleting a file row
// fails, so the closure's error rolls back and surfaces wrapped to the caller.
// Reuses faultDeleteFileStore (defined alongside the stale-removal tests), which
// fails DeleteFile inside the transaction.
func TestRemoveDeletedDeleteFileError(t *testing.T) {
	h := &harness{
		ctx:  context.Background(),
		idx:  &faultDeleteFileStore{Adapter: newOpenAdapter(t)},
		out:  io.Discard,
		warn: io.Discard,
	}
	if err := h.removeDeleted([]string{"gone.go"}); err == nil {
		t.Fatal("expected error when DeleteFile fails inside the transaction")
	}
}

// TestDeriveIncrementalResolveError covers deriveIncremental's first error
// guard: resolveAndWriteEdges fails on a closed index and the wrapped error is
// returned before the later passes run. A queued pending edge keeps
// resolveAndWriteEdges from short-circuiting on an empty buffer, so the closed
// index actually fails the symbol load.
func TestDeriveIncrementalResolveError(t *testing.T) {
	h := &harness{
		ctx:          context.Background(),
		idx:          newClosedAdapter(t),
		out:          io.Discard,
		warn:         io.Discard,
		pendingEdges: []pendingEdge{{SourceID: 1, TargetName: "X", Kind: model.EdgeCalls}},
	}
	var phases PhaseTiming
	if err := h.deriveIncremental(false, &phases); err == nil {
		t.Fatal("expected error deriving edges on a closed index")
	}
}

// TestDeriveIncrementalAssociateTestsError covers deriveIncremental's
// associate-tests error guard (incremental.go:134-136): with no pending edges
// the resolve pass short-circuits cleanly, but a test/impl file pair drives
// associateTests into a real DB write that fails on the closed index, so the
// wrapped error returns before the naming pass.
func TestDeriveIncrementalAssociateTestsError(t *testing.T) {
	h := &harness{
		ctx:  context.Background(),
		idx:  newClosedAdapter(t),
		out:  io.Discard,
		warn: io.Discard,
		indexedFiles: []indexedFile{
			{ID: 1, Path: "foo.go", Language: "go"},
			{ID: 2, Path: "foo_test.go", Language: "go"},
		},
	}
	var phases PhaseTiming
	if err := h.deriveIncremental(false, &phases); err == nil {
		t.Fatal("expected error associating tests on a closed index")
	}
}
