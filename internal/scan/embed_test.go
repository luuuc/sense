package scan_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/index"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func skipWithoutORT(t *testing.T) {
	t.Helper()
	// Check model is bundled (via fetch-deps.sh)
	_, thisFile, _, _ := runtime.Caller(0)
	bundleDir := filepath.Join(filepath.Dir(thisFile), "..", "embed", "bundle")
	if _, err := os.Stat(filepath.Join(bundleDir, "model.onnx")); err != nil {
		t.Skip("model not bundled; run scripts/fetch-deps.sh --local")
	}
	libName := "libonnxruntime.dylib"
	if runtime.GOOS == "linux" {
		libName = "libonnxruntime.so"
	}
	arch := runtime.GOARCH
	platformDir := filepath.Join(bundleDir, runtime.GOOS+"_"+arch)
	if _, err := os.Stat(filepath.Join(platformDir, libName)); err != nil {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}
}

func TestScanEmbeddings(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error {
	return nil
}

func Logout(token string) {
}
`)

	ctx := context.Background()
	opts := scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             true,
	}

	res, err := scan.Run(ctx, opts)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	if res.Embedded == 0 {
		t.Fatal("expected embeddings to be generated on first scan")
	}

	// Verify embeddings exist in the database
	dbPath := filepath.Join(root, ".sense", "index.db")
	count := countEmbeddings(t, dbPath)
	if count == 0 {
		t.Fatal("no embeddings in database after scan")
	}
	if count != res.Embedded {
		t.Errorf("embedding count mismatch: db=%d, result=%d", count, res.Embedded)
	}

	// Verify embedding blob is the correct size (384 dims × 4 bytes)
	blobSize := firstEmbeddingBlobSize(t, dbPath)
	expectedSize := 384 * 4
	if blobSize != expectedSize {
		t.Errorf("embedding blob size = %d, want %d", blobSize, expectedSize)
	}
}

func TestScanEmbeddingsIncremental(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error {
	return nil
}
`)
	writeFile(t, filepath.Join(root, "parser.go"), `package auth

func Parse(input string) []string {
	return nil
}

func Tokenize(input string) []string {
	return nil
}

func Format(tokens []string) string {
	return ""
}
`)

	ctx := context.Background()
	opts := scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             true,
	}

	// First scan: both files
	res1, err := scan.Run(ctx, opts)
	if err != nil {
		t.Fatalf("first scan: %v", err)
	}
	firstEmbedded := res1.Embedded

	// Second scan: no changes → no new embeddings
	res2, err := scan.Run(ctx, opts)
	if err != nil {
		t.Fatalf("second scan: %v", err)
	}
	if res2.Embedded != 0 {
		t.Errorf("second scan should embed 0 symbols (unchanged), got %d", res2.Embedded)
	}

	// Third scan: change only auth.go → only auth.go symbols re-embedded
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error {
	return nil
}

func Verify(token string) bool {
	return true
}
`)

	res3, err := scan.Run(ctx, opts)
	if err != nil {
		t.Fatalf("third scan: %v", err)
	}
	if res3.Embedded == 0 {
		t.Error("third scan should have re-embedded changed file's symbols")
	}
	if res3.Embedded >= firstEmbedded {
		t.Errorf("third scan should embed fewer symbols than first (only changed file); got %d >= %d",
			res3.Embedded, firstEmbedded)
	}

	// Total embeddings in DB should cover all symbols from both files.
	// auth.go has 2 symbols (Login, Verify), parser.go has 3 (Parse, Tokenize, Format) = 5
	dbPath := filepath.Join(root, ".sense", "index.db")
	total := countEmbeddings(t, dbPath)
	if total != 5 {
		t.Errorf("total embeddings = %d, want 5 (2 from auth + 3 from parser)", total)
	}
}

func TestEmbedPending(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error {
	return nil
}

func Logout(token string) {
}
`)

	ctx := context.Background()

	// Phase 1: scan without embedding (deferred)
	res, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("deferred scan: %v", err)
	}
	if res.EmbeddingDebt == 0 {
		t.Fatal("expected embedding debt after deferred scan")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	senseDir := filepath.Join(root, ".sense")

	// Phase 2: EmbedPending picks up the debt
	adapter, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	sqliteAdapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open sqlite adapter: %v", err)
	}
	defer func() { _ = sqliteAdapter.Close() }()

	n, err := scan.EmbedPending(ctx, sqliteAdapter, root, senseDir)
	if err != nil {
		t.Fatalf("EmbedPending: %v", err)
	}
	if n == 0 {
		t.Fatal("EmbedPending should have embedded symbols")
	}

	// Verify embeddings now exist
	if count := countEmbeddings(t, dbPath); count == 0 {
		t.Fatal("no embeddings after EmbedPending")
	}

	// Verify watermark is cleared
	watermark := readMeta(t, dbPath, "embedding_watermark")
	if watermark != "" {
		t.Errorf("embedding_watermark should be cleared after EmbedPending, got %q", watermark)
	}

	// Verify HNSW index was written
	hnswPath := filepath.Join(senseDir, "hnsw.bin")
	if _, err := os.Stat(hnswPath); err != nil {
		t.Errorf("hnsw.bin should exist after EmbedPending: %v", err)
	}
}

func TestScanEmbeddingsDeferred(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "auth.go"), `package auth

func Login(email, password string) error {
	return nil
}

func Logout(token string) {
}
`)

	ctx := context.Background()
	res, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.Embedded != 0 {
		t.Errorf("deferred scan should embed 0 symbols, got %d", res.Embedded)
	}
	if res.EmbeddingDebt == 0 {
		t.Fatal("deferred scan should report embedding debt > 0")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	if count := countEmbeddings(t, dbPath); count != 0 {
		t.Errorf("no embeddings should exist after deferred scan, got %d", count)
	}

	watermark := readMeta(t, dbPath, "embedding_watermark")
	if watermark == "" {
		t.Fatal("embedding_watermark should be set after deferred scan")
	}
}

func TestEmbedPendingCancelSafe(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	// Create enough symbols to make embedding take non-trivial time.
	var src strings.Builder
	src.WriteString("package bulk\n\n")
	for i := range 30 {
		fmt.Fprintf(&src, "func Fn%d() {}\n\n", i)
	}
	writeFile(t, filepath.Join(root, "bulk.go"), src.String())

	ctx := context.Background()

	res, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.EmbeddingDebt == 0 {
		t.Fatal("expected embedding debt")
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	senseDir := filepath.Join(root, ".sense")

	// Cancel immediately — EmbedPending should exit without corrupting the index.
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()

	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	_, _ = scan.EmbedPending(cancelCtx, adapter, root, senseDir)
	// EmbedPending may fail (cancelled) or succeed (race) — either is fine.
	// What matters: the index is not corrupted.

	syms, qerr := adapter.Query(ctx, index.Filter{})
	if qerr != nil {
		t.Fatalf("query after cancel: %v", qerr)
	}
	if len(syms) == 0 {
		t.Fatal("symbols should survive cancelled embed")
	}
}

func TestDeferredEmbedTransition(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "svc.go"), `package svc

func Process() error { return nil }
func Validate() bool { return true }
`)

	ctx := context.Background()
	dbPath := filepath.Join(root, ".sense", "index.db")
	senseDir := filepath.Join(root, ".sense")

	// State 1: Deferred scan — structural index exists, no embeddings.
	res, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("deferred scan: %v", err)
	}
	if res.Symbols == 0 {
		t.Fatal("expected symbols from scan")
	}
	if res.EmbeddingDebt == 0 {
		t.Fatal("state 1: expected embedding debt")
	}
	if countEmbeddings(t, dbPath) != 0 {
		t.Fatal("state 1: expected 0 embeddings")
	}
	if wm := readMeta(t, dbPath, "embedding_watermark"); wm == "" {
		t.Fatal("state 1: expected watermark set")
	}

	// State 2: EmbedPending completes — embeddings exist, watermark cleared.
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	n, err := scan.EmbedPending(ctx, adapter, root, senseDir)
	if err != nil {
		t.Fatalf("EmbedPending: %v", err)
	}
	if n == 0 {
		t.Fatal("state 2: EmbedPending should embed symbols")
	}

	if count := countEmbeddings(t, dbPath); count != n {
		t.Errorf("state 2: embeddings count %d != embedded %d", count, n)
	}
	if wm := readMeta(t, dbPath, "embedding_watermark"); wm != "" {
		t.Errorf("state 2: watermark should be cleared, got %q", wm)
	}

	// State 3: No more debt — EmbedPending is a no-op.
	n2, err := scan.EmbedPending(ctx, adapter, root, senseDir)
	if err != nil {
		t.Fatalf("second EmbedPending: %v", err)
	}
	if n2 != 0 {
		t.Errorf("state 3: expected 0 symbols embedded on re-run, got %d", n2)
	}
}

func TestScanEmbedBackfillsOnNoChange(t *testing.T) {
	skipWithoutORT(t)

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "svc.go"), `package svc

func Process() error { return nil }
func Validate() bool { return true }
`)

	ctx := context.Background()
	dbPath := filepath.Join(root, ".sense", "index.db")

	// Scan 1: deferred — symbols indexed, no embeddings
	res1, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             false,
	})
	if err != nil {
		t.Fatalf("deferred scan: %v", err)
	}
	if res1.EmbeddingDebt == 0 {
		t.Fatal("expected embedding debt after deferred scan")
	}
	if countEmbeddings(t, dbPath) != 0 {
		t.Fatal("expected 0 embeddings after deferred scan")
	}

	// Scan 2: -embed with no file changes — should backfill embeddings
	res2, err := scan.Run(ctx, scan.Options{
		Root:              root,
		Output:            &bytes.Buffer{},
		Warnings:          io.Discard,
		EmbeddingsEnabled: true,
		Embed:             true,
	})
	if err != nil {
		t.Fatalf("embed scan: %v", err)
	}
	if res2.Changed != 0 {
		t.Errorf("expected 0 changed files, got %d", res2.Changed)
	}
	if res2.Embedded == 0 {
		t.Fatal("expected embeddings to be backfilled on second scan with -embed")
	}
	if count := countEmbeddings(t, dbPath); count != res2.Embedded {
		t.Errorf("embeddings in db (%d) != result.Embedded (%d)", count, res2.Embedded)
	}
}

func countEmbeddings(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sense_embeddings").Scan(&count); err != nil {
		t.Fatalf("count embeddings: %v", err)
	}
	return count
}

func firstEmbeddingBlobSize(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var blob []byte
	if err := db.QueryRow("SELECT vector FROM sense_embeddings LIMIT 1").Scan(&blob); err != nil {
		t.Fatalf("read embedding: %v", err)
	}
	return len(blob)
}

func readMeta(t *testing.T, dbPath, key string) string {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var value string
	err = db.QueryRow("SELECT value FROM sense_meta WHERE key = ?", key).Scan(&value)
	if err != nil {
		return ""
	}
	return value
}
