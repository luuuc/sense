package scan

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
)

// TestFinalizeScanSchemaStampFatal covers finalizeScan against a closed index:
// the profile store, summary regeneration, and last-scan stamp each fail and are
// downgraded to warnings, but the schema-version stamp is fatal, so finalizeScan
// returns its error. This walks every warn-and-continue guard plus the one fatal
// return in a single call (scan.go:400-415).
func TestFinalizeScanSchemaStampFatal(t *testing.T) {
	ctx := context.Background()
	adapter := newClosedAdapter(t)
	h := &harness{
		ctx:       ctx,
		idx:       adapter,
		out:       io.Discard,
		warn:      io.Discard,
		root:      t.TempDir(),
		progress:  newProgress(io.Discard, true),
		collector: newWarningCollector(),
	}
	if err := finalizeScan(ctx, adapter, h, t.TempDir()); err == nil {
		t.Fatal("expected finalizeScan to fail on the schema stamp against a closed index")
	}
}

// TestWalkTreePrepareStmtError covers walkTree's first failure guard
// (scan.go:701-703): preparing the per-symbol statement fails on a closed
// index, so the walk aborts before collecting any path.
func TestWalkTreePrepareStmtError(t *testing.T) {
	h := &harness{
		ctx:       context.Background(),
		idx:       newClosedAdapter(t),
		out:       io.Discard,
		warn:      io.Discard,
		progress:  newProgress(io.Discard, true),
		collector: newWarningCollector(),
		seenPaths: map[string]bool{},
	}
	if err := h.walkTree(t.TempDir()); err == nil {
		t.Fatal("expected walkTree to fail preparing the symbol stmt on a closed index")
	}
}

// TestCollectPathsSkipsIgnoredFile covers collectPaths' file-level ignore branch
// (scan.go:771-773): a regular file matched by the ignore matcher is dropped
// from the walk entries and not counted, while an unmatched sibling is kept.
func TestCollectPathsSkipsIgnoredFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "keep.go"), "package p\n")
	writeFile(t, filepath.Join(root, "skip.go"), "package p\n")

	matcher, err := ignore.Build(root, []string{"skip.go"})
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	h := &harness{
		ctx:            context.Background(),
		matcher:        matcher,
		defaultMatcher: ignore.New(ignore.DefaultPatterns()...),
		seenPaths:      map[string]bool{},
	}

	entries, err := h.collectPaths(root)
	if err != nil {
		t.Fatalf("collectPaths: %v", err)
	}
	for _, e := range entries {
		if e.rel == "skip.go" {
			t.Errorf("ignored file skip.go should not appear in walk entries")
		}
	}
	if !h.seenPaths["keep.go"] {
		t.Error("keep.go should be recorded in seenPaths")
	}
	if h.seenPaths["skip.go"] {
		t.Error("skip.go should not be recorded in seenPaths")
	}
}

// TestAccountAndWriteFlushesMidBatch covers accountAndWrite's mid-loop flush
// (scan.go:845-849): a batch that reaches batchSize is flushed before the loop
// ends, so a scan of more than batchSize files exercises the in-loop flush as
// well as the trailing one.
func TestAccountAndWriteFlushesMidBatch(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < batchSize+5; i++ {
		writeSource(t, root, filepath.Join("pkg", fmt.Sprintf("f%d.go", i)), "package pkg\n\nfunc F() {}\n")
	}

	res, err := Run(context.Background(), Options{Root: root, Output: io.Discard, Warnings: io.Discard})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Indexed < batchSize+5 {
		t.Errorf("indexed = %d, want >= %d (mid-batch flush must not drop files)", res.Indexed, batchSize+5)
	}
}
