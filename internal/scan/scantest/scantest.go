// Package scantest drives the real scan pipeline against a throwaway temp
// repository and index, so a test can assert on what a scan derives without a
// checked-in fixture repo or ONNX. It is deliberately small — a temp-repo
// builder and a scan driver, nothing more. If it grows modes, options structs,
// or a fluent builder it has become the framework this cycle exists to avoid;
// keep it near 100 lines.
package scantest

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// Repo is a temp working tree under t.TempDir(). Build one with NewRepo, scan
// it with Scan; the temp dir and any opened index close via t.Cleanup.
type Repo struct {
	Root string
	t    *testing.T
}

// NewRepo writes files (relative path → content) into a fresh t.TempDir and
// returns the repo. Parent directories are created as needed.
func NewRepo(t *testing.T, files map[string]string) *Repo {
	t.Helper()
	r := &Repo{Root: t.TempDir(), t: t}
	for rel, content := range files {
		r.Write(rel, content)
	}
	return r
}

// Write adds or replaces a file in the repo. Use between scans to drive the
// incremental path.
func (r *Repo) Write(rel, content string) {
	r.t.Helper()
	full := filepath.Join(r.Root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatalf("mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", rel, err)
	}
}

// Scan runs the real pipeline against the repo and returns the result plus an
// adapter opened on the resulting index. Root, Output, and Warnings default to
// the repo root and discarded sinks; any other field the caller sets on opts
// (EmbeddingsEnabled, Embed, Rebuild) is honored. The adapter is registered for
// cleanup, so the caller may query it directly and need not close it.
//
// To exercise the embedding path without ONNX, override the embedder seam with
// scan.SetEmbedderFactory before calling Scan (only the scan_test package can,
// which is the point — the production embedder stays the default).
func (r *Repo) Scan(opts scan.Options) (*scan.Result, *sqlite.Adapter) {
	r.t.Helper()
	opts.Root = r.Root
	if opts.Output == nil {
		opts.Output = &bytes.Buffer{}
	}
	if opts.Warnings == nil {
		opts.Warnings = io.Discard
	}

	ctx := context.Background()
	res, err := scan.Run(ctx, opts)
	if err != nil {
		r.t.Fatalf("scan.Run: %v", err)
	}

	adapter, err := sqlite.Open(ctx, filepath.Join(r.Root, ".sense", "index.db"))
	if err != nil {
		r.t.Fatalf("open index: %v", err)
	}
	r.t.Cleanup(func() { _ = adapter.Close() })
	return res, adapter
}
