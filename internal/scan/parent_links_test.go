package scan_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

// parentOf returns the qualified name of a symbol's parent, or "" when the
// parent_id is NULL.
func parentOf(t *testing.T, root, qualified string) string {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = a.Close() }()

	var parent string
	err = a.DB().QueryRowContext(ctx, `
		SELECT COALESCE(p.qualified, '')
		FROM sense_symbols s
		LEFT JOIN sense_symbols p ON p.id = s.parent_id
		WHERE s.qualified = ?`, qualified).Scan(&parent)
	if err != nil {
		t.Fatalf("query parent of %s: %v", qualified, err)
	}
	return parent
}

func parentFileOf(t *testing.T, root, qualified string) string {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = a.Close() }()

	var path string
	err = a.DB().QueryRowContext(ctx, `
		SELECT COALESCE(f.path, '')
		FROM sense_symbols s
		LEFT JOIN sense_symbols p ON p.id = s.parent_id
		LEFT JOIN sense_files f ON f.id = p.file_id
		WHERE s.qualified = ?`, qualified).Scan(&path)
	if err != nil {
		t.Fatalf("query parent file of %s: %v", qualified, err)
	}
	return path
}

func readMetaKey(t *testing.T, root, key string) string {
	t.Helper()
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = a.Close() }()
	v, _ := a.ReadMeta(ctx, key)
	return v
}

func runIncrementalOn(t *testing.T, root string, changed, removed []string) {
	t.Helper()
	ctx := context.Background()
	adapter, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}
	_, err = scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &bytes.Buffer{},
		Changed:       changed,
		Removed:       removed,
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
}

const storeTypeSrc = `package mvcc

type Store struct {
	size int
}

func (s *Store) Size() int {
	return s.size
}
`

const storeTxnSrc = `package mvcc

func (s *Store) Read() int {
	return 1
}

func (s *Store) Write(v int) {
	s.size = v
}
`

// TestCrossFileMethodParentResolves is the G-6 repro: a method declared in
// a sibling file of its receiver type must carry a parent link after a
// full scan (kvstore.go vs kvstore_txn.go — the core Go idiom).
func TestCrossFileMethodParentResolves(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, m := range []string{"mvcc.Store.Read", "mvcc.Store.Write"} {
		if got := parentOf(t, root, m); got != "mvcc.Store" {
			t.Errorf("%s parent = %q, want mvcc.Store", m, got)
		}
	}
	// Same-file binding unchanged.
	if got := parentOf(t, root, "mvcc.Store.Size"); got != "mvcc.Store" {
		t.Errorf("mvcc.Store.Size parent = %q, want mvcc.Store", got)
	}
}

// TestCrossFileParentSurvivesIncrementalRescan pins the clobber-heal
// invariant: WriteSymbol's upsert resets parent_id on every rescan of the
// method's file, and deriveIncremental's parent pass must restore it in
// the same run.
func TestCrossFileParentSurvivesIncrementalRescan(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Touch only the method file: add a method, rescan incrementally.
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc+`
func (s *Store) Clear() {
	s.size = 0
}
`)
	runIncrementalOn(t, root, []string{"kvstore_txn.go"}, nil)

	for _, m := range []string{"mvcc.Store.Read", "mvcc.Store.Write", "mvcc.Store.Clear"} {
		if got := parentOf(t, root, m); got != "mvcc.Store" {
			t.Errorf("%s parent = %q after incremental rescan, want mvcc.Store", m, got)
		}
	}
}

// TestParentFileAddedIncrementallyHealsOnlyOnRebuild pins the accepted
// limitation: when the parent's file arrives later, an untouched child
// stays orphaned (its ParentQualified lives only in memory during its own
// file's scan) until a rebuild rewrites every file.
func TestParentFileAddedIncrementallyHealsOnlyOnRebuild(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "" {
		t.Fatalf("mvcc.Store.Read parent = %q with no type declared, want none", got)
	}

	// The type arrives in a new file; only that file is scanned.
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	runIncrementalOn(t, root, []string{"kvstore.go"}, nil)
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "" {
		t.Errorf("mvcc.Store.Read parent = %q after parent-only incremental, want still none (pinned limitation)", got)
	}

	// A rebuild rewrites every file and heals.
	opts := quietOpts(root)
	opts.Rebuild = true
	if _, err := scan.Run(context.Background(), opts); err != nil {
		t.Fatalf("Run -rebuild: %v", err)
	}
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "mvcc.Store" {
		t.Errorf("mvcc.Store.Read parent = %q after rebuild, want mvcc.Store", got)
	}
}

// TestRemoveStaleParentFileDetachesChildren drives the removeStaleFiles
// path: deleting the type's file must not trip the parent_id foreign key,
// and surviving children end up detached.
func TestRemoveStaleParentFileDetachesChildren(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "mvcc.Store" {
		t.Fatalf("precondition: cross-file link missing (parent = %q)", got)
	}

	if err := os.Remove(filepath.Join(root, "kvstore.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("rescan after parent file removal: %v", err)
	}
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "" {
		t.Errorf("mvcc.Store.Read parent = %q after parent file removal, want detached", got)
	}
}

// TestRemovedFileDetachesChildrenIncremental drives the removeDeleted
// path with the same shape.
func TestRemovedFileDetachesChildrenIncremental(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "kvstore.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	runIncrementalOn(t, root, nil, []string{"kvstore.go"})
	if got := parentOf(t, root, "mvcc.Store.Read"); got != "" {
		t.Errorf("mvcc.Store.Read parent = %q after incremental removal, want detached", got)
	}
}

// TestParentChoiceDeterministicAcrossDuplicateDeclarations pins the
// tiebreak on the build-tag-pair shape: two sibling files declare the same
// type; the method binds to the lexicographically smallest path in every
// fresh scan.
func TestParentChoiceDeterministicAcrossDuplicateDeclarations(t *testing.T) {
	const twinA = `package git

type Blob struct {
	idA int
}
`
	const twinB = `package git

type Blob struct {
	idB int
}
`
	const method = `package git

func (b *Blob) Name() string {
	return ""
}
`
	for run := 0; run < 2; run++ {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "blob_gogit.go"), twinA)
		writeFile(t, filepath.Join(root, "blob_nogogit.go"), twinB)
		writeFile(t, filepath.Join(root, "blob.go"), method)

		if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := parentFileOf(t, root, "git.Blob.Name"); got != "blob_gogit.go" {
			t.Errorf("run %d: git.Blob.Name parent file = %q, want blob_gogit.go (path-smallest twin)", run, got)
		}
	}
}

// TestSatisfyCountsMethodsAcrossFiles is the mvcc.KV shape: satisfaction
// must see a method set that spans files, in the same scan that links it.
func TestSatisfyCountsMethodsAcrossFiles(t *testing.T) {
	const ifaceSrc = `package mvcc

type KV interface {
	Read() int
	Write(v int)
}
`
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kv.go"), ifaceSrc)
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)
	writeFile(t, filepath.Join(root, "kvstore_txn.go"), storeTxnSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = a.Close() }()

	var n int
	err = a.DB().QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sense_edges e
		JOIN sense_symbols src ON src.id = e.source_id
		JOIN sense_symbols dst ON dst.id = e.target_id
		WHERE e.kind = 'inherits' AND src.qualified = 'mvcc.Store' AND dst.qualified = 'mvcc.KV'`).Scan(&n)
	if err != nil {
		t.Fatalf("query satisfaction edge: %v", err)
	}
	if n != 1 {
		t.Errorf("mvcc.Store -> mvcc.KV satisfaction edges = %d, want 1 (method set spans kvstore.go + kvstore_txn.go)", n)
	}
}

// TestScanStampsParentLinkageMeta pins the staleness signal: a fresh scan
// stamps parent_linkage, a plain rescan preserves it, and an index
// stripped of the stamp is only re-stamped by a rebuild.
func TestScanStampsParentLinkageMeta(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "kvstore.go"), storeTypeSrc)

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := readMetaKey(t, root, "parent_linkage"); got != "1" {
		t.Fatalf("parent_linkage = %q after fresh scan, want 1", got)
	}

	// A plain rescan (nothing changed) must not clear the stamp.
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan: %v", err)
	}
	if got := readMetaKey(t, root, "parent_linkage"); got != "1" {
		t.Errorf("parent_linkage = %q after plain rescan, want preserved 1", got)
	}

	// Strip the stamp (a pre-fix index); a plain rescan of unchanged
	// files heals nothing and must NOT stamp; a rebuild must.
	ctx := context.Background()
	a, err := sqlite.Open(ctx, filepath.Join(root, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	if err := a.DeleteMeta(ctx, "parent_linkage"); err != nil {
		t.Fatalf("delete meta: %v", err)
	}
	_ = a.Close()

	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("plain rescan on stripped index: %v", err)
	}
	if got := readMetaKey(t, root, "parent_linkage"); got != "" {
		t.Errorf("parent_linkage = %q after plain rescan of unchanged files, want unstamped", got)
	}

	opts := quietOpts(root)
	opts.Rebuild = true
	if _, err := scan.Run(context.Background(), opts); err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if got := readMetaKey(t, root, "parent_linkage"); got != "1" {
		t.Errorf("parent_linkage = %q after rebuild, want 1", got)
	}
}
