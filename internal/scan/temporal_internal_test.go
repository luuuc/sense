package scan

import (
	"context"
	"io"
	"path/filepath"
	"testing"

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
