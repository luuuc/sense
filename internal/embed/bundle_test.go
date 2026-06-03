package embed

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeLib is stand-in shared-library bytes. extractLib never interprets the
// content, so a few bytes exercise the atomic-write and version-invalidation
// logic without the bundled ONNX Runtime — the seam card 27-02 opened.
var fakeLib = []byte{0x7f, 'E', 'L', 'F', 0x01, 0x02, 0x03}

// libFileName mirrors extractLib's platform branch so tests can locate the
// extracted library.
func libFileName() string {
	if runtime.GOOS == "darwin" {
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so"
}

func TestExtractLibWritesAndReturnsPath(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "lib")

	libPath, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("extractLib: %v", err)
	}
	if want := filepath.Join(cacheDir, libFileName()); libPath != want {
		t.Errorf("libPath = %q, want %q", libPath, want)
	}

	got, err := os.ReadFile(libPath)
	if err != nil {
		t.Fatalf("read extracted lib: %v", err)
	}
	if string(got) != string(fakeLib) {
		t.Errorf("extracted bytes = %v, want %v", got, fakeLib)
	}

	version, err := os.ReadFile(libPath + ".version")
	if err != nil {
		t.Fatalf("read version sidecar: %v", err)
	}
	if string(version) != "v1" {
		t.Errorf("version sidecar = %q, want %q", string(version), "v1")
	}
}

func TestExtractLibCacheHit(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "lib")

	first, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("first extractLib: %v", err)
	}
	// Mark the file so we can prove a cache hit did not rewrite it.
	if err := os.Chtimes(first, time.Time{}, time.Unix(1, 0)); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	before, err := os.Stat(first)
	if err != nil {
		t.Fatal(err)
	}

	second, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("second extractLib: %v", err)
	}
	if second != first {
		t.Errorf("path changed: %q vs %q", first, second)
	}
	after, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Error("cache hit rewrote the library (mod time changed)")
	}
}

func TestExtractLibStaleVersionReextracts(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "lib")

	libPath, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("first extractLib: %v", err)
	}

	// A binary upgrade: same dir, newer version → re-extract with new bytes.
	newBytes := []byte("newer library payload")
	libPath2, err := extractLib(newBytes, cacheDir, "v2")
	if err != nil {
		t.Fatalf("second extractLib: %v", err)
	}
	if libPath2 != libPath {
		t.Errorf("path changed: %q vs %q", libPath, libPath2)
	}

	got, err := os.ReadFile(libPath2)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBytes) {
		t.Errorf("stale version not re-extracted: bytes = %q", string(got))
	}
	version, _ := os.ReadFile(libPath2 + ".version")
	if string(version) != "v2" {
		t.Errorf("version sidecar = %q, want v2", string(version))
	}
}

func TestExtractLibMissingLibReextracts(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "lib")

	libPath, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("first extractLib: %v", err)
	}
	// Remove the library but keep the matching version sidecar: the version
	// check passes, the os.Stat fails, so it must re-extract.
	if err := os.Remove(libPath); err != nil {
		t.Fatalf("remove lib: %v", err)
	}

	libPath2, err := extractLib(fakeLib, cacheDir, "v1")
	if err != nil {
		t.Fatalf("re-extract: %v", err)
	}
	if libPath2 != libPath {
		t.Errorf("path changed: %q vs %q", libPath, libPath2)
	}
	if _, err := os.Stat(libPath2); err != nil {
		t.Fatalf("lib not recreated: %v", err)
	}
}

func TestExtractLibMkdirFails(t *testing.T) {
	// Point the cache dir's parent at a regular file so MkdirAll fails.
	tmp := t.TempDir()
	notADir := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := extractLib(fakeLib, filepath.Join(notADir, "lib"), "v1"); err == nil {
		t.Fatal("expected error when cache dir parent is a file")
	}
}

func TestExtractLibCreateTempFails(t *testing.T) {
	// A read-only cache dir: the version check and MkdirAll pass (dir exists),
	// but CreateTemp cannot write into it.
	cacheDir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cacheDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(cacheDir, 0o755) }()

	if _, err := extractLib(fakeLib, cacheDir, "v1"); err == nil {
		t.Fatal("expected error when cache dir is read-only")
	}
}

func TestExtractLibRenameFails(t *testing.T) {
	// Force the rename to fail with a real OS condition: a directory already
	// sits at the target library path, so rename(tmp → libPath) cannot replace
	// it. No fault-injecting filesystem — just a directory where a file goes.
	cacheDir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libPath := filepath.Join(cacheDir, libFileName())
	if err := os.Mkdir(libPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := extractLib(fakeLib, cacheDir, "v1"); err == nil {
		t.Fatal("expected error when target path is a directory")
	}
}

func TestExtractLibWriteVersionFails(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "lib")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-create the version sidecar as a directory so WriteFile fails after a
	// successful library rename, exercising the final error branch.
	if err := os.Mkdir(filepath.Join(cacheDir, libFileName()+".version"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := extractLib(fakeLib, cacheDir, "v1"); err == nil {
		t.Fatal("expected error when version sidecar cannot be written")
	}
}

func TestEnsureORTLibWiring(t *testing.T) {
	// ensureORTLib is thin wiring over extractLib; here we exercise the wiring
	// (cache-dir resolution + extraction) using whatever bytes are bundled. The
	// bytes' validity does not matter — only that the path resolves and the file
	// lands under the resolved cache dir.
	tmp := t.TempDir()
	t.Setenv("SENSE_CACHE_DIR", tmp)

	libPath, err := ensureORTLib()
	if err != nil {
		t.Fatalf("ensureORTLib: %v", err)
	}
	if want := filepath.Join(tmp, "lib", libFileName()); libPath != want {
		t.Errorf("libPath = %q, want %q", libPath, want)
	}
	if _, err := os.Stat(libPath); err != nil {
		t.Errorf("extracted lib missing: %v", err)
	}
}

func TestEnsureORTLibCacheDirError(t *testing.T) {
	// SENSE_CACHE_DIR empty and HOME unavailable → ortCacheDir fails, and
	// ensureORTLib must surface that before touching the filesystem.
	t.Setenv("SENSE_CACHE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if _, err := ensureORTLib(); err == nil {
		t.Fatal("expected error when the cache dir cannot be resolved")
	}
}

func TestNewBundledEmbedderExtractError(t *testing.T) {
	// Point the cache dir at a regular file so extraction fails inside
	// ensureORTLib; NewBundledEmbedder must wrap and surface it rather than
	// proceeding to ONNX init. Hermetic: no real library is touched.
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "notadir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSE_CACHE_DIR", filePath)

	if _, err := NewBundledEmbedder(0); err == nil {
		t.Fatal("expected error when ORT library extraction fails")
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

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get home dir")
	}
	want := filepath.Join(home, ".cache", "sense", "lib")
	if got != want {
		t.Errorf("ortCacheDir default = %q, want %q", got, want)
	}
}

func TestORTCacheDirHomeDirFails(t *testing.T) {
	t.Setenv("SENSE_CACHE_DIR", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")

	if _, err := ortCacheDir(); err == nil {
		t.Fatal("ortCacheDir: expected error when HOME unavailable")
	}
}
