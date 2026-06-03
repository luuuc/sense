package embed

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/luuuc/sense/internal/version"
)

//go:embed bundle/model.onnx
var modelBytes []byte

//go:embed bundle/vocab.txt
var vocabBytes []byte

// NewBundledEmbedder creates an ONNXEmbedder using the model, vocabulary,
// and ONNX Runtime library embedded in the binary. It extracts the
// platform-specific shared library to a cache directory on first use.
// intraOpThreads controls per-session parallelism; 0 means ONNX Runtime
// default (all cores). Use a smaller value when running multiple sessions
// in parallel to avoid thread over-subscription.
func NewBundledEmbedder(intraOpThreads int) (*ONNXEmbedder, error) {
	libPath, err := ensureORTLib()
	if err != nil {
		return nil, fmt.Errorf("extract ONNX Runtime library: %w", err)
	}

	if err := InitORTLibrary(libPath); err != nil {
		return nil, fmt.Errorf("init ONNX Runtime: %w", err)
	}

	return NewONNXEmbedder(modelBytes, vocabBytes, intraOpThreads)
}

// ensureORTLib extracts the bundled ONNX Runtime shared library to the
// per-user cache directory, keyed by the binary version. It is thin wiring
// over extractLib: resolve the cache dir, then extract. The bug-prone logic
// (atomic write, version invalidation) lives in extractLib, which takes its
// bytes and directory as parameters so it can be tested without the bundled
// library or the real cache location.
func ensureORTLib() (string, error) {
	cacheDir, err := ortCacheDir()
	if err != nil {
		return "", err
	}
	return extractLib(loadBundledORTLib(), cacheDir, version.Version)
}

// extractLib writes libData to cacheDir as the platform's ONNX Runtime shared
// library, returning its path. It skips the write when an extraction tagged
// wantVersion is already present (the cache hit), and otherwise writes
// atomically — temp file, chmod, rename — so a crash mid-write never leaves a
// partial library a later run would load. The version is recorded in a sidecar
// file so a binary upgrade re-extracts. Pure in the sense that matters for
// testing: every input (bytes, directory, version) is a parameter, so the
// atomic-write and stale-version paths are reachable with a temp dir and a few
// bytes, no bundled library required.
func extractLib(libData []byte, cacheDir, wantVersion string) (string, error) {
	libName := "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		libName = "libonnxruntime.dylib"
	}

	libPath := filepath.Join(cacheDir, libName)
	versionPath := libPath + ".version"

	if existing, err := os.ReadFile(versionPath); err == nil {
		if string(existing) == wantVersion {
			if _, err := os.Stat(libPath); err == nil {
				return libPath, nil
			}
		}
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}

	// Atomic write: temp file then rename to prevent partial reads.
	tmp, err := os.CreateTemp(cacheDir, libName+".tmp.*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(libData); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, libPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	if err := os.WriteFile(versionPath, []byte(wantVersion), 0o644); err != nil {
		return "", err
	}

	return libPath, nil
}

func ortCacheDir() (string, error) {
	if dir := os.Getenv("SENSE_CACHE_DIR"); dir != "" {
		return filepath.Join(dir, "lib"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "sense", "lib"), nil
}
