package scan_test

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/embed"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// TestScan_EmbeddingModelMigration proves the model-migration path: when the
// index records an embedding model that differs from the binary's current
// model, the next embed scan clears the stale embeddings, deletes the stored
// model meta, announces the change on the output, and re-embeds everything
// with the new model. The plain embed tests never differ the stored model, so
// migrateEmbeddingModel's clear/delete body and embedAndDefer's "changed"
// branch only run here.
func TestScan_EmbeddingModelMigration(t *testing.T) {
	useFakeEmbedder(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error { return nil }
func Logout(token string) {}
`)

	ctx := context.Background()
	embedOpts := func(out io.Writer) scan.Options {
		return scan.Options{
			Root:              root,
			Output:            out,
			Warnings:          io.Discard,
			EmbeddingsEnabled: true,
			Embed:             true,
		}
	}

	// First embed scan stamps the current model on the index.
	if _, err := scan.Run(ctx, embedOpts(io.Discard)); err != nil {
		t.Fatalf("first embed scan: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	stamp := func(value string) {
		adapter, err := sqlite.Open(ctx, dbPath)
		if err != nil {
			t.Fatalf("open adapter: %v", err)
		}
		defer func() { _ = adapter.Close() }()
		if err := adapter.WriteMeta(ctx, "embedding_model", value); err != nil {
			t.Fatalf("stamp model: %v", err)
		}
	}

	// Pretend the index was embedded by an older model.
	stamp("ancient-model-v0")

	var out bytes.Buffer
	if _, err := scan.Run(ctx, embedOpts(&out)); err != nil {
		t.Fatalf("migration embed scan: %v", err)
	}

	if !bytes.Contains(out.Bytes(), []byte("embedding model changed")) {
		t.Errorf("expected migration notice in output, got %q", out.String())
	}

	// After migration the stored model is the binary's current model again.
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("reopen adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()
	stored, err := adapter.ReadMeta(ctx, "embedding_model")
	if err != nil {
		t.Fatalf("read model meta: %v", err)
	}
	if stored != embed.ModelID {
		t.Errorf("stored model = %q, want %q after re-embed", stored, embed.ModelID)
	}

	// Everything was re-embedded with the new model.
	if count := countEmbeddings(t, dbPath); count == 0 {
		t.Error("expected embeddings to be regenerated after model migration")
	}
}

// TestScan_EmbeddingModelMigrationDeferred covers the deferred side of a model
// change: when the model differs but embeddings are deferred (no -embed), the
// migration still clears stale embeddings and the watermark is written even
// though no file changed, because modelMigrated forces the debt to be recorded.
func TestScan_EmbeddingModelMigrationDeferred(t *testing.T) {
	useFakeEmbedder(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "svc.go"), `package svc

func Process() error { return nil }
`)

	ctx := context.Background()
	dbPath := filepath.Join(root, ".sense", "index.db")

	// Embed once so there are embeddings and a stored model to migrate from.
	if _, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            io.Discard,
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             true,
	}); err != nil {
		t.Fatalf("first embed scan: %v", err)
	}

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	if err := adapter.WriteMeta(ctx, "embedding_model", "ancient-model-v0"); err != nil {
		t.Fatalf("stamp model: %v", err)
	}
	_ = adapter.Close()

	// Deferred scan with no file change — migration clears embeddings and,
	// because modelMigrated is true, a watermark/debt is still recorded.
	res, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            io.Discard,
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("deferred migration scan: %v", err)
	}
	if res.EmbeddingDebt == 0 {
		t.Error("expected embedding debt after model migration cleared embeddings")
	}
	if count := countEmbeddings(t, dbPath); count != 0 {
		t.Errorf("expected embeddings cleared by migration, got %d", count)
	}
	if wm := readMeta(t, dbPath, "embedding_watermark"); wm == "" {
		t.Error("expected watermark set after deferred model migration")
	}
}
