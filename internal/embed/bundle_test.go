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
