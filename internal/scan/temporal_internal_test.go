package scan

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// newClosedAdapter opens an index and immediately closes it, so every
// subsequent query or exec fails. It is the in-package lever for the
// temporal coupling error returns, which only fire when the index round
// trip fails — a state the happy-path tests never reach.
func newClosedAdapter(t *testing.T) *sqlite.Adapter {
	t.Helper()
	a, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return a
}

// TestIndexedFilePathsQueryError covers indexedFilePaths' error return
// (temporal.go:245-247): FilePaths fails against a closed index.
func TestIndexedFilePathsQueryError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	if _, err := h.indexedFilePaths(); err == nil {
		t.Fatal("expected error from indexedFilePaths on closed adapter")
	}
}

// TestRepresentativeSymbolsQueryError covers representativeSymbols' first
// error return (temporal.go:266-267): the outbound-connectivity query
// fails against a closed index before any row is read.
func TestRepresentativeSymbolsQueryError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	if _, err := h.representativeSymbols(map[string]bool{"pkg/a.rb": true}); err == nil {
		t.Fatal("expected error from representativeSymbols on closed adapter")
	}
}

// TestClearTemporalEdgesExecError covers clearTemporalEdges' error return
// (temporal.go:370-372): the DELETE fails against a closed index.
func TestClearTemporalEdgesExecError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	if err := h.clearTemporalEdges(); err == nil {
		t.Fatal("expected error from clearTemporalEdges on closed adapter")
	}
}

// faultPrepareEdgeTemporalStore fails PrepareEdgeStmt once the temporal write
// transaction has begun. Single named method, fail-once.
type faultPrepareEdgeTemporalStore struct {
	*sqlite.Adapter
}

func (f *faultPrepareEdgeTemporalStore) PrepareEdgeStmt(context.Context) (*sql.Stmt, error) {
	return nil, errors.New("injected PrepareEdgeStmt failure")
}

// TestWriteTemporalEdgesPrepareStmtError covers writeTemporalEdges'
// in-transaction failure (temporal.go:105-107): the transaction begins, then
// preparing the edge statement fails, so the closure rolls back and the error
// surfaces to the caller. A significant pair with both representatives present
// is queued so the function does not short-circuit before the prepare.
func TestWriteTemporalEdgesPrepareStmtError(t *testing.T) {
	h := &harness{
		ctx:  context.Background(),
		idx:  &faultPrepareEdgeTemporalStore{Adapter: newOpenAdapter(t)},
		warn: io.Discard,
	}
	significant := []pairKey{{a: "pkg/a.go", b: "lib/b.go"}}
	repSymbols := map[string]model.Symbol{
		"pkg/a.go": {ID: 1, FileID: 1},
		"lib/b.go": {ID: 2, FileID: 2},
	}
	pairs := map[pairKey]int{{a: "pkg/a.go", b: "lib/b.go"}: 3}
	fileCounts := map[string]int{"pkg/a.go": 3, "lib/b.go": 3}

	if _, err := h.writeTemporalEdges(significant, repSymbols, pairs, fileCounts); err == nil {
		t.Fatal("expected error when PrepareEdgeStmt fails inside the temporal write")
	}
}

// TestWriteTemporalPairExecError covers writeTemporalPair's edge-exec failure
// (temporal.go:154-156): the prepared statement's adapter is closed, so the
// first directed edge fails to execute and the wrapped error returns with the
// partial written count.
func TestWriteTemporalPairExecError(t *testing.T) {
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	stmt, err := adapter.PrepareEdgeStmt(ctx)
	if err != nil {
		t.Fatalf("prepare edge stmt: %v", err)
	}
	// Closing the adapter closes the prepared statement, so the next Exec fails.
	_ = adapter.Close()

	symA := model.Symbol{ID: 1, FileID: 1}
	symB := model.Symbol{ID: 2, FileID: 2}
	if _, err := writeTemporalPair(ctx, stmt, symA, symB, 3, 1.0); err == nil {
		t.Fatal("expected error executing a temporal edge on a closed statement")
	}
}
