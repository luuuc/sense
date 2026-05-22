package scan_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/ignore"
	"github.com/luuuc/sense/internal/scan"
	"github.com/luuuc/sense/internal/sqlite"
)

func TestScan_EmptyProject(t *testing.T) {
	root := t.TempDir()
	// No files at all — walkTree should handle len(entries)==0 gracefully.
	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 0 {
		t.Errorf("Files = %d, want 0", res.Files)
	}
	if res.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0", res.Indexed)
	}
	if res.Symbols != 0 {
		t.Errorf("Symbols = %d, want 0", res.Symbols)
	}
}

func TestScan_EmptyProjectNoSupportedFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "readme.txt"), "just a text file\n")
	writeFile(t, filepath.Join(root, "data.csv"), "a,b,c\n1,2,3\n")

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 2 {
		t.Errorf("Files = %d, want 2 (counted but not indexed)", res.Files)
	}
	if res.Indexed != 0 {
		t.Errorf("Indexed = %d, want 0 (no supported languages)", res.Indexed)
	}
}

func TestScan_ContextCancelled(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10; i++ {
		writeFile(t, filepath.Join(root, "pkg", fmt.Sprintf("f%d.go", i)),
			"package pkg\n\nfunc F() {}\n")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := scan.Run(ctx, scan.Options{
		Root:     root,
		Output:   &bytes.Buffer{},
		Warnings: io.Discard,
	})
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestRunIncremental_ChangedFileDeletedFromDisk(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Keep() {}\n")
	writeFile(t, filepath.Join(root, "b.go"), "package a\n\nfunc Gone() {}\n")

	ctx := context.Background()

	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	// Delete b.go from disk but report it as changed (simulating a race
	// where the watcher saw a modify event but the file was then deleted).
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	var warnBuf bytes.Buffer
	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      &warnBuf,
		Changed:       []string{"b.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	// File was deleted — should produce a warning, not a crash.
	if res.Warnings == 0 {
		t.Error("expected warning for file that was deleted between event and parse")
	}
	if res.Changed != 0 {
		t.Errorf("Changed = %d, want 0 (file was unreadable)", res.Changed)
	}
}

func TestRunIncremental_NilParsersCreatesTemporary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\n")

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("initial scan: %v", err)
	}

	writeFile(t, filepath.Join(root, "a.go"), "package a\n\nfunc Hello() {}\nfunc World() {}\n")

	dbPath := filepath.Join(root, ".sense", "index.db")
	adapter, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("open adapter: %v", err)
	}
	defer func() { _ = adapter.Close() }()

	matcher, err := ignore.Build(root, nil)
	if err != nil {
		t.Fatalf("build matcher: %v", err)
	}

	// Parsers: nil — should create a temporary ParserCache internally.
	res, err := scan.RunIncremental(ctx, scan.IncrementalOptions{
		Root:          root,
		Idx:           adapter,
		Matcher:       matcher,
		MaxFileSizeKB: 512,
		Output:        io.Discard,
		Warnings:      io.Discard,
		Parsers:       nil,
		Changed:       []string{"a.go"},
	})
	if err != nil {
		t.Fatalf("RunIncremental: %v", err)
	}
	if res.Changed != 1 {
		t.Errorf("Changed = %d, want 1", res.Changed)
	}
}

func TestScan_QuietSuppressesWarningHint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "good.go"), "package a\n\nfunc Hello() {}\n")
	writeFile(t, filepath.Join(root, "broken.go"), "package a\n\nfunc incompl")

	var out bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &out,
		Warnings: io.Discard,
		Quiet:    true,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Warnings == 0 {
		t.Fatal("expected warnings from broken.go")
	}
	if bytes.Contains(out.Bytes(), []byte("warnings — see")) {
		t.Error("quiet mode should suppress warning hint in output")
	}
}

func TestScan_SatisfyInterfaces(t *testing.T) {
	root := t.TempDir()

	// Interface with two methods.
	writeFile(t, filepath.Join(root, "iface.go"), `package mylib

type Greeter interface {
	Hello() string
	Goodbye() string
}
`)

	// Struct satisfying the interface.
	writeFile(t, filepath.Join(root, "impl.go"), `package mylib

type EnglishGreeter struct{}

func (e *EnglishGreeter) Hello() string {
	return "hello"
}

func (e *EnglishGreeter) Goodbye() string {
	return "goodbye"
}
`)

	ctx := context.Background()
	_, err := scan.Run(ctx, quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Look for an inherits edge from EnglishGreeter → Greeter.
	var greeterID int64
	var structID int64
	err = a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Greeter' AND kind = 'interface'`).Scan(&greeterID)
	if err != nil {
		t.Fatalf("query Greeter: %v", err)
	}
	err = a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'EnglishGreeter' AND kind = 'class'`).Scan(&structID)
	if err != nil {
		t.Fatalf("query EnglishGreeter: %v", err)
	}

	var count int
	err = a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE source_id = ? AND target_id = ? AND kind = 'inherits'`,
		structID, greeterID).Scan(&count)
	if err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if count == 0 {
		t.Error("expected inherits edge from EnglishGreeter → Greeter (interface satisfaction)")
	}
}

func TestScan_SatisfyInterfacesSkipsEmptyMethodInterface(t *testing.T) {
	// A marker interface (no methods) must not produce an inherits edge
	// from arbitrary structs — methodSetSatisfies returns true for the
	// empty required set, so the empty-methods skip at satisfy.go:139 is
	// what protects against universal "everyone implements Marker" noise.
	root := t.TempDir()

	writeFile(t, filepath.Join(root, "marker.go"), `package mylib

type Marker interface{}

type Foo struct{}
func (f *Foo) Hello() string { return "hi" }
`)

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var markerID, fooID int64
	if err := a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Marker' AND kind = 'interface'`).Scan(&markerID); err != nil {
		t.Fatalf("query Marker: %v", err)
	}
	if err := a.DB().QueryRowContext(ctx,
		`SELECT id FROM sense_symbols WHERE name = 'Foo' AND kind = 'class'`).Scan(&fooID); err != nil {
		t.Fatalf("query Foo: %v", err)
	}

	var count int
	if err := a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE source_id = ? AND target_id = ? AND kind = 'inherits'`,
		fooID, markerID).Scan(&count); err != nil {
		t.Fatalf("query edge: %v", err)
	}
	if count != 0 {
		t.Errorf("Foo should not inherit empty-method Marker (got %d edges)", count)
	}
}

func TestScan_RunDefaultsRootAndIOWriters(t *testing.T) {
	// Cover the zero-value defaults in Run: Root="" → ".", Output=nil →
	// os.Stderr, Warnings=nil → os.Stderr (scan.go:109-111, 121-127).
	// Run from a tempdir so it doesn't touch the developer's tree.
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	// Redirect stderr so the default-output write doesn't pollute test output.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = origStderr
		_ = r.Close()
	})
	go func() { _, _ = io.Copy(io.Discard, r) }()

	if _, err := scan.Run(context.Background(), scan.Options{}); err != nil {
		t.Fatalf("Run with zero options: %v", err)
	}
	_ = w.Close()
}

func TestScan_RunHonorsSENSEDIR(t *testing.T) {
	// Cover the SENSE_DIR env-override branch (scan.go:114-116) when
	// opts.Sense is empty.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	senseDir := filepath.Join(t.TempDir(), "sense-override")
	t.Setenv("SENSE_DIR", senseDir)
	if _, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   io.Discard,
		Warnings: io.Discard,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(senseDir, "index.db")); err != nil {
		t.Errorf("expected index at SENSE_DIR override: %v", err)
	}
}

func TestScan_RunSkipsSymlinks(t *testing.T) {
	// Cover the symlink-skip branch in walkTree (scan.go:584-586).
	root := t.TempDir()
	target := filepath.Join(root, "real.go")
	writeFile(t, target, "package p\n")
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink not supported here: %v", err)
	}

	res, err := scan.Run(context.Background(), quietOpts(root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("expected 1 indexed file (symlink skipped), got %d", res.Files)
	}
}

func TestScan_RunHonorsSENSEMaxFileSize(t *testing.T) {
	// Cover the SENSE_MAX_FILE_SIZE env-override branch (scan.go:141-145).
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main\n")
	t.Setenv("SENSE_MAX_FILE_SIZE", "256")
	if _, err := scan.Run(context.Background(), quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestScan_TemporalCouplingInGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	root := t.TempDir()

	// Initialize a git repo with enough co-changing files.
	gitCmd := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	gitCmd("init")
	gitCmd("config", "user.email", "test@test.com")
	gitCmd("config", "user.name", "test")

	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg\n\nfunc A() {}\n")
	writeFile(t, filepath.Join(root, "lib", "b.go"), "package lib\n\nfunc B() {}\n")

	// Create minCoChanges (3) commits where both files change together.
	for i := 0; i < 4; i++ {
		writeFile(t, filepath.Join(root, "pkg", "a.go"),
			fmt.Sprintf("package pkg\n\n// v%d\nfunc A() {}\n", i))
		writeFile(t, filepath.Join(root, "lib", "b.go"),
			fmt.Sprintf("package lib\n\n// v%d\nfunc B() {}\n", i))
		gitCmd("add", "-A")
		gitCmd("commit", "-m", fmt.Sprintf("co-change %d", i))
	}

	ctx := context.Background()
	if _, err := scan.Run(ctx, quietOpts(root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Should have temporal edges for the co-changing files.
	dbPath := filepath.Join(root, ".sense", "index.db")
	a, err := sqlite.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	var edgeCount int
	err = a.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sense_edges WHERE kind = 'temporal'`).Scan(&edgeCount)
	if err != nil {
		t.Fatalf("query temporal edges: %v", err)
	}
	if edgeCount == 0 {
		t.Error("expected temporal edges from co-changing files")
	}
}

func TestScan_DefaultIgnoredCount(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "app.rb"), "class App; end\n")
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "index.js"), "export default {}\n")

	var out bytes.Buffer
	res, err := scan.Run(context.Background(), scan.Options{
		Root:     root,
		Output:   &out,
		Warnings: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.DefaultIgnored == 0 {
		t.Error("expected node_modules to be counted as default-ignored")
	}
	if !bytes.Contains(out.Bytes(), []byte("skipped")) {
		t.Errorf("output should mention skipped directories: %q", out.String())
	}
}
