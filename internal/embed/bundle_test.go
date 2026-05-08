package embed

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/luuuc/sense/internal/version"
)

func TestNewBundledEmbedder(t *testing.T) {
	if len(modelBytes) == 0 {
		t.Skip("model not bundled; run scripts/fetch-deps.sh --local")
	}
	if len(vocabBytes) == 0 {
		t.Skip("vocab not bundled; run scripts/fetch-deps.sh --local")
	}
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	emb, err := NewBundledEmbedder(0)
	if err != nil {
		t.Fatalf("NewBundledEmbedder: %v", err)
	}
	defer func() { _ = emb.Close() }()

	vecs, err := emb.Embed(context.Background(), []EmbedInput{
		{QualifiedName: "Foo#bar", Kind: "method", Snippet: "def bar"},
	})
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != Dimensions {
		t.Fatalf("unexpected result: %d vectors, dims=%d", len(vecs), len(vecs[0]))
	}
}

func TestNewBundledReranker(t *testing.T) {
	if len(rerankerBytes) == 0 {
		t.Skip("reranker model not bundled; run scripts/fetch-deps.sh --local")
	}
	if len(vocabBytes) == 0 {
		t.Skip("vocab not bundled; run scripts/fetch-deps.sh --local")
	}
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	r, err := NewBundledReranker(0)
	if err != nil {
		t.Fatalf("NewBundledReranker: %v", err)
	}
	defer func() { _ = r.Close() }()

	scores, err := r.Score("user authentication", []string{
		"method Auth::Login#call\ndef call(email, password)",
		"method CSV::Parser#parse\ndef parse(input)",
	})
	if err != nil {
		t.Fatalf("score: %v", err)
	}
	if len(scores) != 2 {
		t.Fatalf("expected 2 scores, got %d", len(scores))
	}
	if scores[0] <= scores[1] {
		t.Errorf("auth doc (%.4f) should score higher than csv doc (%.4f)", scores[0], scores[1])
	}
}

func TestEnsureORTLibCacheInvalidation(t *testing.T) {
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	tmp := t.TempDir()
	t.Setenv("SENSE_CACHE_DIR", tmp)

	libName := "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		libName = "libonnxruntime.dylib"
	}

	// First extraction
	libPath, err := ensureORTLib()
	if err != nil {
		t.Fatalf("first ensureORTLib: %v", err)
	}

	// Write a stale version marker to simulate a version bump
	versionPath := filepath.Join(tmp, "lib", libName+".version")
	if err := os.WriteFile(versionPath, []byte("old-version"), 0o644); err != nil {
		t.Fatalf("write stale version: %v", err)
	}

	// Second extraction should overwrite due to version mismatch
	libPath2, err := ensureORTLib()
	if err != nil {
		t.Fatalf("second ensureORTLib: %v", err)
	}
	if libPath2 != libPath {
		t.Fatalf("path changed: %s vs %s", libPath, libPath2)
	}

	// Version file should now contain the current version
	got, err := os.ReadFile(versionPath)
	if err != nil {
		t.Fatalf("read version file: %v", err)
	}
	if string(got) != version.Version {
		t.Errorf("version file = %q, want %q", string(got), version.Version)
	}
}

func TestORTCacheDirWithEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("SENSE_CACHE_DIR", tmp)

	got, err := ortCacheDir()
	if err != nil {
		t.Fatalf("ortCacheDir: %v", err)
	}
	want := filepath.Join(tmp, "lib")
	if got != want {
		t.Errorf("ortCacheDir with env = %q, want %q", got, want)
	}
}

func TestORTCacheDirDefault(t *testing.T) {
	t.Setenv("SENSE_CACHE_DIR", "")

	got, err := ortCacheDir()
	if err != nil {
		t.Fatalf("ortCacheDir: %v", err)
	}

	// Should be under home dir
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	want := filepath.Join(home, ".cache", "sense", "lib")
	if got != want {
		t.Errorf("ortCacheDir default = %q, want %q", got, want)
	}
}

func TestEnsureORTLibMkdirFails(t *testing.T) {
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	// Create a file and point SENSE_CACHE_DIR at it so MkdirAll fails
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSE_CACHE_DIR", filePath)

	_, err := ensureORTLib()
	if err == nil {
		t.Fatal("expected error when cache dir parent is a file")
	}
}

func TestEnsureORTLibCreateTempFails(t *testing.T) {
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	// Create a read-only directory so CreateTemp fails
	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "lib")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(cacheDir, 0o755) }()

	t.Setenv("SENSE_CACHE_DIR", tmp)

	_, err := ensureORTLib()
	if err == nil {
		t.Fatal("expected error when cache dir is read-only")
	}
}

func TestEnsureORTLibRenameFails(t *testing.T) {
	if len(ortLibData) == 0 {
		t.Skip("ORT library not bundled; run scripts/fetch-deps.sh --local")
	}

	tmp := t.TempDir()
	t.Setenv("SENSE_CACHE_DIR", tmp)

	libName := "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		libName = "libonnxruntime.dylib"
	}

	// First, extract successfully
	_, err := ensureORTLib()
	if err != nil {
		t.Fatalf("first ensureORTLib: %v", err)
	}

	// Now make the version stale to force re-extraction
	versionPath := filepath.Join(tmp, "lib", libName+".version")
	if err := os.WriteFile(versionPath, []byte("old-version"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a directory at the target lib path so rename fails
	libPath := filepath.Join(tmp, "lib", libName)
	if err := os.Remove(libPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(libPath, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err = ensureORTLib()
	if err == nil {
		t.Fatal("expected error when target path is a directory")
	}

	// Clean up so subsequent tests aren't affected
	_ = os.RemoveAll(libPath)
}

func TestNewBundledEmbedderEnsureORTLibError(t *testing.T) {
	// Point cache dir at a file so ensureORTLib fails
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSE_CACHE_DIR", filePath)

	_, err := NewBundledEmbedder(0)
	if err == nil {
		t.Fatal("expected error when ensureORTLib fails")
	}
}

func TestNewBundledRerankerEnsureORTLibError(t *testing.T) {
	// Point cache dir at a file so ensureORTLib fails
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSE_CACHE_DIR", filePath)

	_, err := NewBundledReranker(0)
	if err == nil {
		t.Fatal("expected error when ensureORTLib fails")
	}
}
