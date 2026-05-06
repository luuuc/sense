package search_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luuuc/sense/internal/search"
)

func TestTextFallbackSearchFindsMatches(t *testing.T) {
	tf := search.NewTextFallback()
	if !tf.Available() {
		t.Skip("rg not installed")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "schema.sql"), []byte("CREATE TABLE sense_edges (\n  id INTEGER PRIMARY KEY\n);\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644)

	results := tf.Search(context.Background(), "CREATE TABLE", dir, []string{"schema.sql", "main.go"}, 10)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}
	if results[0].File != "schema.sql" {
		t.Errorf("File = %q, want %q", results[0].File, "schema.sql")
	}
	if results[0].Line != 1 {
		t.Errorf("Line = %d, want 1", results[0].Line)
	}
}

func TestTextFallbackMultiWordOR(t *testing.T) {
	tf := search.NewTextFallback()
	if !tf.Available() {
		t.Skip("rg not installed")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("CASCADE\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("REFERENCES\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "c.go"), []byte("nothing here\n"), 0644)

	results := tf.Search(context.Background(), "CASCADE REFERENCES", dir, []string{"a.go", "b.go", "c.go"}, 10)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestTextFallbackMultiWordRanking(t *testing.T) {
	tf := search.NewTextFallback()
	if !tf.Available() {
		t.Skip("rg not installed")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "both.sql"), []byte("CASCADE\nREFERENCES\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "one.sql"), []byte("CASCADE\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "none.sql"), []byte("nothing here\n"), 0644)

	results := tf.Search(context.Background(), "CASCADE REFERENCES", dir, []string{"both.sql", "one.sql", "none.sql"}, 10)
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}
	if results[0].File != "both.sql" {
		t.Errorf("first result file = %q, want %q (file matching more terms should rank first)", results[0].File, "both.sql")
	}

	hasNone := false
	for _, r := range results {
		if r.File == "none.sql" {
			hasNone = true
		}
	}
	if hasNone {
		t.Error("none.sql should not appear in results")
	}
}

func TestTextFallbackScopesToProvidedFiles(t *testing.T) {
	tf := search.NewTextFallback()
	if !tf.Available() {
		t.Skip("rg not installed")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "included.txt"), []byte("SECRET_TOKEN\n"), 0644)
	_ = os.WriteFile(filepath.Join(dir, "excluded.txt"), []byte("SECRET_TOKEN\n"), 0644)

	results := tf.Search(context.Background(), "SECRET_TOKEN", dir, []string{"included.txt"}, 10)
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1 (scoped to included.txt only)", len(results))
	}
	if results[0].File != "included.txt" {
		t.Errorf("File = %q, want %q", results[0].File, "included.txt")
	}
}
