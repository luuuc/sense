package scan

import (
	"testing"

	"github.com/luuuc/sense/internal/embed"
)

// SetEmbedderFactory swaps the package-default embedder constructor for the
// duration of a test, restoring it on cleanup. It is the test-only seam that
// lets scan_test drive the embedding pipeline with a fake (e.g.
// embedtest.FakeEmbedder) instead of the bundled ONNX model — without adding
// any test knob to the exported scan.Run / scan.Options surface.
//
// Both Run (via the harness field) and the package-level EmbedPending read the
// default at the moment they build their pool, so a call here before either
// takes effect for that scan.
func SetEmbedderFactory(t *testing.T, f func(threads int) (embed.Embedder, error)) {
	t.Helper()
	old := defaultEmbedderFactory
	defaultEmbedderFactory = f
	t.Cleanup(func() { defaultEmbedderFactory = old })
}
