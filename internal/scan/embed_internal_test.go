package scan

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/sqlite"
)

// newOpenAdapter opens a fresh, empty index that stays open for the test. The
// caller drives error branches by wrapping it in a fault store; the index
// itself is healthy so only the injected method fails.
func newOpenAdapter(t *testing.T) *sqlite.Adapter {
	t.Helper()
	a, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

// TestWriteEmbeddingChunkPrepareStmtError covers writeEmbeddingChunk's
// statement-preparation failure (embed.go:257-259): inside the transaction the
// prepared-statement build fails on a closed index, so the chunk write returns
// the error instead of persisting a partial set of vectors.
func TestWriteEmbeddingChunkPrepareStmtError(t *testing.T) {
	ctx := context.Background()
	chunk := []sqlite.EmbedSymbol{{ID: 1, FileID: 1}}
	vecs := [][]float32{make([]float32, embed.Dimensions)}
	if err := writeEmbeddingChunk(ctx, newClosedAdapter(t), chunk, vecs); err == nil {
		t.Fatal("expected error preparing the embedding stmt on a closed index")
	}
}

// TestBuildContextMapSkipsFileOnError covers buildContextMap's per-file error
// fallback (embed.go:355-356): when ContextForFile fails for a file the loop
// continues to the next rather than aborting, so a single bad file does not
// drop every symbol's context. A closed index makes ContextForFile fail for
// real.
func TestBuildContextMapSkipsFileOnError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	syms := []sqlite.EmbedSymbol{
		{ID: 1, FileID: 10},
		{ID: 2, FileID: 20},
	}
	got := h.buildContextMap(syms)
	if len(got) != 0 {
		t.Errorf("expected empty context map when every file errors, got %d entries", len(got))
	}
}

// TestExtendMethodSnippetsFilePathsError covers extendMethodSnippets' early
// return when the file-path lookup fails (embed.go:386-388): a closed index
// makes FilePathsByIDs error, so the snippets are left as-is and the function
// returns without touching source files.
func TestExtendMethodSnippetsFilePathsError(t *testing.T) {
	h := &harness{ctx: context.Background(), idx: newClosedAdapter(t), warn: io.Discard}
	syms := []sqlite.EmbedSymbol{
		{ID: 1, FileID: 10, Kind: "function", Snippet: "func F()", LineStart: 1, LineEnd: 3},
	}
	h.extendMethodSnippets(syms)
	if syms[0].Snippet != "func F()" {
		t.Errorf("snippet should be untouched when FilePathsByIDs fails, got %q", syms[0].Snippet)
	}
}

// TestExtendMethodSnippetsReadsBodyAndSkipsUnknownFile drives extendMethodSnippets
// over a real index: one function symbol whose source is on disk (its snippet is
// replaced with body lines, exercising the read-join path and the end-of-file
// clamp when LineEnd runs past the file), and a second symbol pointing at a file
// id the path map does not resolve (the !ok continue). Both branches the
// FilePaths-error early-return test cannot reach.
func TestExtendMethodSnippetsReadsBodyAndSkipsUnknownFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	adapter := newOpenAdapter(t)

	// Write a real source file and index it so FilePathsByIDs resolves it.
	src := "package p\n\nfunc F() {\n\tx := 1\n\t_ = x\n}\n"
	writeFile(t, filepath.Join(root, "f.go"), src)
	fileID, err := adapter.WriteFile(ctx, &model.File{Path: "f.go", Language: "go", Hash: "h"})
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}

	h := &harness{ctx: ctx, idx: adapter, root: root, warn: io.Discard}
	syms := []sqlite.EmbedSymbol{
		// Real file; LineEnd deliberately past the file end so the clamp runs.
		{ID: 1, FileID: fileID, Kind: "function", Snippet: "func F()", LineStart: 3, LineEnd: 99},
		// Unresolvable file id ⇒ the paths[fid] !ok continue.
		{ID: 2, FileID: 999999, Kind: "method", Snippet: "keep-me", LineStart: 1, LineEnd: 1},
	}
	h.extendMethodSnippets(syms)

	if syms[0].Snippet == "func F()" {
		t.Error("expected the function snippet to be extended from source")
	}
	if syms[1].Snippet != "keep-me" {
		t.Errorf("unknown-file symbol snippet should be untouched, got %q", syms[1].Snippet)
	}
}

// faultDeleteMetaStore fails DeleteMeta once, the second step of a model
// migration (after ClearEmbeddings succeeds). Fail-once, single named method.
type faultDeleteMetaStore struct {
	*sqlite.Adapter
}

func (f *faultDeleteMetaStore) DeleteMeta(context.Context, string) error {
	return errors.New("injected DeleteMeta failure")
}

// TestMigrateEmbeddingModelDeleteMetaError covers migrateEmbeddingModel's
// delete-meta failure (embed.go:169-171): the stale embeddings clear, then
// deleting the old model meta fails, so the wrapped error surfaces.
func TestMigrateEmbeddingModelDeleteMetaError(t *testing.T) {
	ctx := context.Background()
	adapter := newOpenAdapter(t)
	if err := adapter.WriteMeta(ctx, "embedding_model", "ancient-model-v0"); err != nil {
		t.Fatalf("seed stale model: %v", err)
	}
	h := &harness{ctx: ctx, idx: &faultDeleteMetaStore{Adapter: adapter}, out: io.Discard, warn: io.Discard}
	if _, err := h.migrateEmbeddingModel(); err == nil {
		t.Fatal("expected error when deleting the old model meta fails")
	}
}

// faultSymbolsForFilesStore fails SymbolsForFiles, the first query embedSymbols
// makes once it has a non-empty changed-file set.
type faultSymbolsForFilesStore struct {
	*sqlite.Adapter
}

func (f *faultSymbolsForFilesStore) SymbolsForFiles(context.Context, []int64) ([]sqlite.EmbedSymbol, error) {
	return nil, errors.New("injected SymbolsForFiles failure")
}

// TestEmbedSymbolsQueryError covers embedSymbols' symbol-query failure
// (embed.go:280-282): with a changed-file id set, loading the symbols for those
// files fails and the wrapped error returns before any embedding work.
func TestEmbedSymbolsQueryError(t *testing.T) {
	h := &harness{
		ctx:            context.Background(),
		idx:            &faultSymbolsForFilesStore{Adapter: newOpenAdapter(t)},
		out:            io.Discard,
		warn:           io.Discard,
		changedFileIDs: []int64{1},
	}
	if err := h.embedSymbols(); err == nil {
		t.Fatal("expected error when SymbolsForFiles fails")
	}
}

// faultClearEmbeddingsStore fails ClearEmbeddings once, the first thing
// migrateEmbeddingModel does after detecting a model mismatch. Single named
// method, fail-once — the same discipline as the rollback fault stores.
type faultClearEmbeddingsStore struct {
	*sqlite.Adapter
}

func (f *faultClearEmbeddingsStore) ClearEmbeddings(context.Context) error {
	return errors.New("injected ClearEmbeddings failure")
}

// TestMigrateEmbeddingModelClearError covers migrateEmbeddingModel's
// clear-embeddings failure (embed.go:166-168): a stored model that differs from
// the binary's triggers the migration, but clearing the stale embeddings fails,
// so the wrapped error surfaces and no migration is reported.
func TestMigrateEmbeddingModelClearError(t *testing.T) {
	ctx := context.Background()
	adapter := newOpenAdapter(t)
	if err := adapter.WriteMeta(ctx, "embedding_model", "ancient-model-v0"); err != nil {
		t.Fatalf("seed stale model: %v", err)
	}
	h := &harness{ctx: ctx, idx: &faultClearEmbeddingsStore{Adapter: adapter}, out: io.Discard, warn: io.Discard}
	migrated, err := h.migrateEmbeddingModel()
	if err == nil {
		t.Fatal("expected error when clearing stale embeddings fails")
	}
	if migrated {
		t.Error("migration must not be reported when the clear failed")
	}
}
