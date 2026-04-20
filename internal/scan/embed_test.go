package scan_test

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/luuuc/sense/internal/scan"
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
