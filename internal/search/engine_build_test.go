package search_test

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/model"
	"github.com/luuuc/sense/internal/search"
	"github.com/luuuc/sense/internal/sqlite"
)

func openEngineTestDB(t *testing.T) (*sqlite.Adapter, string) {
	t.Helper()
	dir := t.TempDir()
	a, err := sqlite.Open(context.Background(), filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, dir
}

func seedSymbol(t *testing.T, a *sqlite.Adapter, name string) int64 {
	t.Helper()
	ctx := context.Background()
	fid, err := a.WriteFile(ctx, &model.File{
		Path: "app/" + name + ".go", Language: "go",
		Hash: "h-" + name, Symbols: 1, IndexedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("write file: %v", err)
	}
	sid, err := a.WriteSymbol(ctx, &model.Symbol{
		FileID: fid, Name: name, Qualified: "pkg." + name,
		Kind: "function", LineStart: 1, LineEnd: 2,
	})
	if err != nil {
		t.Fatalf("write symbol: %v", err)
	}
	return sid
}

func writeFileByte(path string) error {
	return os.WriteFile(path, []byte("x"), 0o600)
}

func vecBlob(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(x))
	}
	return buf
}

func TestBuildEngineEmbeddingsDisabled(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "false")
	a, dir := openEngineTestDB(t)

	engine, embedder, err := search.BuildEngine(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine even when embeddings disabled")
	}
	if embedder != nil {
		t.Error("expected nil embedder when embeddings disabled")
	}
}

func TestBuildEngineNoVectorsNoDebt(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "true")
	a, dir := openEngineTestDB(t)
	// Empty index: no symbols → no embeddings, zero debt → no embedder.

	engine, embedder, err := search.BuildEngine(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if embedder != nil {
		t.Error("expected nil embedder with no vectors and no debt")
	}
}

func TestBuildEngineLoadEmbeddingsError(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "true")
	a, dir := openEngineTestDB(t)
	_ = a.Close() // closed adapter → LoadEmbeddings fails

	_, _, err := search.BuildEngine(context.Background(), a, dir)
	if err == nil {
		t.Fatal("expected error from BuildEngine on closed adapter")
	}
}

func TestBuildEngineWithVectorsBuildsFlatIndex(t *testing.T) {
	probe, perr := embed.NewBundledEmbedder(0)
	if perr != nil {
		t.Skipf("bundled embedder unavailable: %v", perr)
	}
	_ = probe.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	a, dir := openEngineTestDB(t)
	ctx := context.Background()

	sid := seedSymbol(t, a, "ProcessPayment")
	if err := a.WriteEmbedding(ctx, sid, vecBlob([]float32{1, 0, 0})); err != nil {
		t.Fatalf("write embedding: %v", err)
	}

	engine, embedder, err := search.BuildEngine(ctx, a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if embedder == nil {
		t.Fatal("expected embedder when vectors are present")
	}
	defer func() { _ = embedder.Close() }()

	// A semantic search must run the vector leg now (mode hybrid).
	_, meta, err := engine.Search(ctx, search.Options{Query: "process the payment", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if meta.Mode != search.ModeHybrid {
		t.Errorf("mode = %q, want hybrid (vectors should be live)", meta.Mode)
	}
}

func TestBuildEngineEmbedderInitError(t *testing.T) {
	t.Setenv("SENSE_EMBEDDINGS", "true")
	a, dir := openEngineTestDB(t)
	ctx := context.Background()

	// Vectors present → BuildEngine tries to create the embedder. Point the
	// ORT cache at a regular file so library extraction (MkdirAll) fails,
	// forcing the embedder-init error path.
	sid := seedSymbol(t, a, "Thing")
	if err := a.WriteEmbedding(ctx, sid, vecBlob([]float32{1, 0, 0})); err != nil {
		t.Fatalf("write embedding: %v", err)
	}
	badCache := filepath.Join(t.TempDir(), "not-a-dir")
	if err := writeFileByte(badCache); err != nil {
		t.Fatalf("seed bad cache file: %v", err)
	}
	t.Setenv("SENSE_CACHE_DIR", badCache)

	_, _, err := search.BuildEngine(ctx, a, dir)
	if err == nil {
		t.Fatal("expected embedder-init error when ORT cache dir is unwritable")
	}
}

func TestBuildEngineDebtCreatesEmbedder(t *testing.T) {
	probe, perr := embed.NewBundledEmbedder(0)
	if perr != nil {
		t.Skipf("bundled embedder unavailable: %v", perr)
	}
	_ = probe.Close()

	t.Setenv("SENSE_EMBEDDINGS", "true")
	a, dir := openEngineTestDB(t)

	// Symbol with no embedding → embedding debt > 0, but no vectors yet.
	seedSymbol(t, a, "PendingThing")

	engine, embedder, err := search.BuildEngine(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("BuildEngine: %v", err)
	}
	if engine == nil {
		t.Fatal("expected non-nil engine")
	}
	if embedder == nil {
		t.Fatal("expected embedder created for outstanding embedding debt")
	}
	_ = embedder.Close()
}
