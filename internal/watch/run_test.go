package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunOptionsDefaults(t *testing.T) {
	var opts RunOptions
	if opts.Root != "" {
		t.Error("RunOptions.Root zero value should be empty")
	}
	if opts.EmbeddingsEnabled {
		t.Error("RunOptions.EmbeddingsEnabled zero value should be false")
	}
	if opts.MCP {
		t.Error("RunOptions.MCP zero value should be false")
	}
}

// TestRunInitializesAndExits verifies that Run performs its initialization
// (initial scan, service start) and then exits cleanly when the context is
// cancelled.
func TestRunInitializesAndExits(t *testing.T) {
	dir := t.TempDir()

	// Create a minimal project structure.
	if err := os.MkdirAll(filepath.Join(dir, ".sense"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run should initialize and then exit when the context times out.
	err := Run(ctx, RunOptions{Root: dir})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
}
