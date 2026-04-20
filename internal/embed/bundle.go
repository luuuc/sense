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
func NewBundledEmbedder() (*ONNXEmbedder, error) {
	libPath, err := ensureORTLib()
	if err != nil {
		return nil, fmt.Errorf("extract ONNX Runtime library: %w", err)
	}

	if err := InitORTLibrary(libPath); err != nil {
		return nil, fmt.Errorf("init ONNX Runtime: %w", err)
	}

	return NewONNXEmbedder(modelBytes, vocabBytes)
}

// ensureORTLib extracts the bundled ONNX Runtime shared library to a
// cache directory if it doesn't already exist (or if the binary version
// changed). Returns the path to the extracted library. Uses atomic
// writes (temp file + rename) to prevent partial extraction.
func ensureORTLib() (string, error) {
	libData := loadBundledORTLib()

	cacheDir, err := ortCacheDir()
	if err != nil {
		return "", err
	}

	libName := "libonnxruntime.so"
	if runtime.GOOS == "darwin" {
		libName = "libonnxruntime.dylib"
	}

	libPath := filepath.Join(cacheDir, libName)
	versionPath := libPath + ".version"

	wantVersion := version.Version
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
