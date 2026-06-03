package scan

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// newWalkHarness builds a harness with the per-file walk machinery wired up
// (parser cache, warning sinks, seen-path set) around the given store.
func newWalkHarness(idx indexStore) *harness {
	return &harness{
		ctx:       context.Background(),
		idx:       idx,
		out:       io.Discard,
		warn:      io.Discard,
		collector: newWarningCollector(),
		progress:  &progress{},
		parsers:   map[string]*sitter.Parser{},
		seenPaths: map[string]bool{},
	}
}

// TestParserForCachesByLanguage covers parserFor's cache-hit return: a second
// request for the same language must hand back the same parser, not build a
// fresh one. The incremental path relies on this to avoid re-binding a grammar
// per file.
func TestParserForCachesByLanguage(t *testing.T) {
	h := &harness{parsers: map[string]*sitter.Parser{}}
	t.Cleanup(h.closeParsers)

	ex := extract.ForExtension(".rb")
	if ex == nil {
		t.Fatal("no extractor for .rb")
	}
	first, err := h.parserFor(ex)
	if err != nil {
		t.Fatalf("first parserFor: %v", err)
	}
	second, err := h.parserFor(ex)
	if err != nil {
		t.Fatalf("second parserFor: %v", err)
	}
	if first != second {
		t.Error("parserFor should reuse the cached parser for the same language")
	}
}

// TestProcessFileWriteErrorWarns covers processFile's write-failure branch: the
// file parses, but the per-file write fails, so processFile records a warning
// and indexes nothing rather than crashing the incremental scan.
func TestProcessFileWriteErrorWarns(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "a.rb", "class A\nend\n")
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })

	h := newWalkHarness(&faultWriteStore{Adapter: adapter, failPath: "a.rb"})
	h.processFile(filepath.Join(dir, "a.rb"), "a.rb")
	t.Cleanup(h.closeParsers)

	if h.indexed != 0 {
		t.Errorf("indexed = %d, want 0 (the write failed)", h.indexed)
	}
	if got := h.collector.count(); got != 1 {
		t.Errorf("warnings = %d, want 1 (the failed write)", got)
	}
}

// TestParseFileMetaErrorWarnsButStillParses covers parseFile's FileMeta error
// path: the incremental hash lookup fails, so the file cannot be skipped — the
// scan logs a warning and parses it anyway rather than dropping it silently.
func TestParseFileMetaErrorWarnsButStillParses(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "a.rb", "class A\nend\n")

	h := newWalkHarness(newClosedAdapter(t)) // closed ⇒ FileMeta fails
	t.Cleanup(h.closeParsers)

	fr := h.parseFile(filepath.Join(dir, "a.rb"), "a.rb")
	if fr == nil {
		t.Fatal("parseFile should still return a result when the hash lookup fails")
	}
	if got := h.collector.count(); got != 1 {
		t.Errorf("warnings = %d, want 1 (the FileMeta error)", got)
	}
}

// TestRemoveStaleFilesListErrorPropagates covers removeStaleFiles' first error
// return: listing tracked files fails against a closed index.
func TestRemoveStaleFilesListErrorPropagates(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	if err := h.removeStaleFiles(); err == nil {
		t.Fatal("expected error when listing tracked files fails")
	}
}

// faultDeleteFileStore fails DeleteFile, the write removeStaleFiles issues
// inside its transaction. Single named method, fail-always — the stale set in
// the test has one entry, so one failure is enough.
type faultDeleteFileStore struct {
	*sqlite.Adapter
}

func (f *faultDeleteFileStore) DeleteFile(context.Context, string) error {
	return errors.New("injected DeleteFile failure")
}

// TestRemoveStaleFilesDeleteErrorPropagates covers removeStaleFiles' in-tx
// delete-error path: a tracked file is absent from this walk's seen set (so it
// is stale), but deleting it fails, and the error surfaces from the transaction
// rather than leaving a half-applied cleanup.
func TestRemoveStaleFilesDeleteErrorPropagates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	adapter := openIndex(t, dir)
	t.Cleanup(func() { _ = adapter.Close() })

	// Seed a tracked file the walk never sees, so it is classified stale.
	if _, err := adapter.WriteFile(ctx, &model.File{Path: "gone.rb", Language: "ruby", Hash: "h", IndexedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	h := &harness{
		ctx:       ctx,
		idx:       &faultDeleteFileStore{Adapter: adapter},
		warn:      io.Discard,
		seenPaths: map[string]bool{}, // nothing seen ⇒ gone.rb is stale
	}
	if err := h.removeStaleFiles(); err == nil {
		t.Fatal("expected error when deleting a stale file fails")
	}
}
