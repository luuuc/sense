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
