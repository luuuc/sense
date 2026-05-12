package mcpio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSnippetReaderDefault(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "main.go", "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n")

	r := NewSnippetReader(dir, 2)
	cs := r.Read(context.Background(), "main.go", 5)
	if cs == nil {
		t.Fatal("expected call site")
	}
	if cs.Line != 5 {
		t.Errorf("line = %d, want 5", cs.Line)
	}
	lines := splitLines(cs.Snippet)
	if len(lines) != 5 {
		t.Errorf("snippet lines = %d, want 5; snippet:\n%s", len(lines), cs.Snippet)
	}
}

func TestSnippetReaderStartOfFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "line1\nline2\nline3\nline4\nline5\n")

	r := NewSnippetReader(dir, 2)
	cs := r.Read(context.Background(), "a.go", 1)
	if cs == nil {
		t.Fatal("expected call site")
	}
	lines := splitLines(cs.Snippet)
	if len(lines) != 3 {
		t.Errorf("snippet lines = %d, want 3 (line 1 + 2 after); snippet:\n%s", len(lines), cs.Snippet)
	}
	if lines[0] != "line1" {
		t.Errorf("first line = %q, want %q", lines[0], "line1")
	}
}

func TestSnippetReaderEndOfFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "line1\nline2\nline3\nline4\nline5")

	r := NewSnippetReader(dir, 2)
	cs := r.Read(context.Background(), "a.go", 5)
	if cs == nil {
		t.Fatal("expected call site")
	}
	lines := splitLines(cs.Snippet)
	if len(lines) != 3 {
		t.Errorf("snippet lines = %d, want 3 (2 before + line 5); snippet:\n%s", len(lines), cs.Snippet)
	}
	if lines[len(lines)-1] != "line5" {
		t.Errorf("last line = %q, want %q", lines[len(lines)-1], "line5")
	}
}

func TestSnippetReaderMissingFile(t *testing.T) {
	r := NewSnippetReader(t.TempDir(), 2)
	cs := r.Read(context.Background(), "nonexistent.go", 5)
	if cs != nil {
		t.Error("expected nil for missing file")
	}
}

func TestSnippetReaderZeroContextLines(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "line1\nline2\nline3\n")

	r := NewSnippetReader(dir, 0)
	if r.Enabled() {
		t.Error("expected Enabled() == false for context_lines=0")
	}
	cs := r.Read(context.Background(), "a.go", 2)
	if cs != nil {
		t.Error("expected nil when context_lines=0")
	}
}

func TestSnippetReaderNegativeContextLines(t *testing.T) {
	r := NewSnippetReader(t.TempDir(), -1)
	if r.Enabled() {
		t.Error("expected Enabled() == false for negative context_lines")
	}
}

func TestSnippetReaderZeroLine(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "line1\nline2\n")

	r := NewSnippetReader(dir, 2)
	cs := r.Read(context.Background(), "a.go", 0)
	if cs != nil {
		t.Error("expected nil for line=0")
	}
}

func TestSnippetReaderEmptyPath(t *testing.T) {
	r := NewSnippetReader(t.TempDir(), 2)
	cs := r.Read(context.Background(), "", 5)
	if cs != nil {
		t.Error("expected nil for empty path")
	}
}

func TestSnippetReaderCustomContextLines(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	r := NewSnippetReader(dir, 3)
	cs := r.Read(context.Background(), "a.go", 5)
	if cs == nil {
		t.Fatal("expected call site")
	}
	lines := splitLines(cs.Snippet)
	if len(lines) != 7 {
		t.Errorf("snippet lines = %d, want 7 (3+1+3); snippet:\n%s", len(lines), cs.Snippet)
	}
}

func TestSnippetReaderLineBeyondFile(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "line1\nline2\n")

	r := NewSnippetReader(dir, 2)
	cs := r.Read(context.Background(), "a.go", 100)
	if cs != nil {
		t.Error("expected nil for line beyond file length")
	}
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
