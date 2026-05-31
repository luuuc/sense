package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeMkdirError(t *testing.T) {
	root := t.TempDir()
	// A regular file where Materialize needs a directory makes MkdirAll fail.
	if err := os.WriteFile(filepath.Join(root, "sub"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	err := Materialize(root, map[string]string{"sub/a.rb": "class A; end\n"})
	if err == nil {
		t.Fatal("expected mkdir error when a file blocks the directory path")
	}
}

func TestMaterializeWriteFileError(t *testing.T) {
	root := t.TempDir()
	// A directory where Materialize needs a file makes WriteFile fail.
	if err := os.Mkdir(filepath.Join(root, "a.rb"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	err := Materialize(root, map[string]string{"a.rb": "class A; end\n"})
	if err == nil {
		t.Fatal("expected write error when a directory blocks the file path")
	}
}

func TestClassifyScanError(t *testing.T) {
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := Classify(ctx, missing, filepath.Join(t.TempDir(), ".sense"))
	if err == nil {
		t.Fatal("expected scan error for a nonexistent root")
	}
}

func TestClassifyDBFindDeadError(t *testing.T) {
	ctx := context.Background()
	// An empty file is a valid but schema-less sqlite database; the decision
	// layer's first query hits a missing table and errors.
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	if err := os.WriteFile(dbPath, nil, 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, err := classifyDB(ctx, dbPath)
	if err == nil {
		t.Fatal("expected FindDead error against a schema-less index")
	}
}

func TestRunCorpusClassifyError(t *testing.T) {
	ctx := context.Background()
	workdir := t.TempDir()
	// Pre-create the first fixture's .sense path as a FILE. Materialize
	// (which writes a.rb at the root) succeeds, but the subsequent scan's
	// MkdirAll of the .sense directory fails — exercising RunCorpus's
	// classify-error branch.
	root := filepath.Join(workdir, "fixture-00")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".sense"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, _, err := RunCorpus(ctx, workdir, []Fixture{
		{Name: "blocked-sense", Files: map[string]string{"a.rb": "class A; end\n"}},
	})
	if err == nil {
		t.Fatal("expected RunCorpus to surface a classify error")
	}
}

func TestRunCorpusMaterializeError(t *testing.T) {
	ctx := context.Background()
	workdir := t.TempDir()
	// Block the first fixture's root directory with a file.
	if err := os.WriteFile(filepath.Join(workdir, "fixture-00"), []byte("x"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	_, _, err := RunCorpus(ctx, workdir, []Fixture{
		{Name: "blocked", Files: map[string]string{"a.rb": "class A; end\n"}},
	})
	if err == nil {
		t.Fatal("expected RunCorpus to surface a materialize error")
	}
}
