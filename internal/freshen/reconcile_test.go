package freshen

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

func openAdapter(t *testing.T, dir string) *sqlite.Adapter {
	t.Helper()
	a, err := sqlite.Open(context.Background(), filepath.Join(dir, ".sense", "index.db"))
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	return a
}

func TestReconcileDriftChangedAndDeleted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"),
		[]byte("package p\n\nfunc Bar() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	// Drift while "closed": edit a.go (b.go untouched).
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n\nfunc Baz() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	changed, skipped, err := ReconcileDrift(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("ReconcileDrift: %v", err)
	}
	if skipped {
		t.Fatal("small drift should not be skipped")
	}
	if changed != 1 {
		t.Errorf("changed=%d, want 1", changed)
	}
	if !symbolExists(t, dir, "Baz") {
		t.Error("edited file should have been re-indexed (Baz missing)")
	}
}

func TestReconcileDriftIgnoresMissingFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.go"),
		[]byte("package p\n\nfunc Bar() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	// Delete b.go: reconcile must NOT remove its index entry (deletions are
	// the live watcher's job, not the startup catch-up's).
	if err := os.Remove(filepath.Join(dir, "b.go")); err != nil {
		t.Fatal(err)
	}

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	changed, _, err := ReconcileDrift(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("ReconcileDrift: %v", err)
	}
	if changed != 0 {
		t.Errorf("a missing file is not a modification, changed=%d", changed)
	}
	if !symbolExists(t, dir, "Bar") {
		t.Error("startup reconcile must not delete index rows for missing files")
	}
}

func TestReconcileDriftNoDrift(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	changed, skipped, err := ReconcileDrift(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("ReconcileDrift: %v", err)
	}
	if changed != 0 || skipped {
		t.Errorf("expected a clean no-op, got changed=%d skipped=%v", changed, skipped)
	}
}

func TestReconcileDriftSkipsLargeDrift(t *testing.T) {
	dir := t.TempDir()
	n := maxReconcileFiles + 1
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".go"),
			[]byte("package p\n\nfunc Orig"+strconv.Itoa(i)+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scanRepo(t, dir, false)

	// Edit every file: drift exceeds the inline cap.
	for i := 0; i < n; i++ {
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)+".go"),
			[]byte("package p\n\nfunc Orig"+strconv.Itoa(i)+"() {}\n\nfunc New"+strconv.Itoa(i)+"() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	_, skipped, err := ReconcileDrift(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("ReconcileDrift: %v", err)
	}
	if !skipped {
		t.Error("oversized drift should be skipped")
	}
	if symbolExists(t, dir, "New0") {
		t.Error("skipped reconcile should not have indexed anything")
	}
}

func TestReconcileDriftClosedAdapter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	a := openAdapter(t, dir)
	_ = a.Close() // drift sweep query will fail on the closed DB

	changed, skipped, err := ReconcileDrift(context.Background(), a, dir)
	if err != nil {
		t.Fatalf("ReconcileDrift should tolerate a query failure, got %v", err)
	}
	if changed != 0 || skipped {
		t.Errorf("closed-adapter sweep should find nothing, got changed=%d skipped=%v", changed, skipped)
	}
}

func TestReconcileDriftBadConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)
	if err := os.WriteFile(filepath.Join(dir, ".sense", "config.yml"),
		[]byte("ignore: [unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	if _, _, err := ReconcileDrift(context.Background(), a, dir); err == nil {
		t.Error("ReconcileDrift should surface a malformed config error")
	}
}

func TestDriftPathsSkipsBadTimestamp(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"),
		[]byte("package p\n\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	scanRepo(t, dir, false)

	a := openAdapter(t, dir)
	defer func() { _ = a.Close() }()

	// Corrupt the indexed_at so the row fails to parse and is skipped.
	if _, err := a.DB().ExecContext(context.Background(),
		`UPDATE sense_files SET indexed_at = 'not-a-timestamp'`); err != nil {
		t.Fatal(err)
	}

	changed := driftPaths(context.Background(), a.DB(), dir)
	if len(changed) != 0 {
		t.Errorf("rows with unparseable timestamps should be skipped, got changed=%v", changed)
	}
}

func TestAcquireWriterLockExported(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}

	release, ok := AcquireWriterLock(dir)
	if !ok {
		t.Fatal("should acquire a free lock")
	}

	// A second attempt while held fails.
	_, ok2 := AcquireWriterLock(dir)
	if ok2 {
		t.Error("second acquire should fail while held")
	}

	release()

	// Released: acquirable again.
	release3, ok3 := AcquireWriterLock(dir)
	if !ok3 {
		t.Error("should acquire after release")
	}
	release3()
}

func TestAcquireWriterLockHeldByLiveProcess(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-hold with this live process.
	if err := os.WriteFile(filepath.Join(dir, ".sense", lockFileName),
		[]byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	release, ok := AcquireWriterLock(dir)
	if ok {
		release()
		t.Fatal("should not acquire a lock held by a live process")
	}
	// release is a safe no-op when not acquired.
	release()
}
