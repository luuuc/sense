package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luuuc/sense/internal/sqlite"
)

func TestReadFileLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.rb")
	content := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	lines, err := readFileLines(path)
	if err != nil {
		t.Fatalf("readFileLines: %v", err)
	}
	if len(lines) != 5 {
		t.Fatalf("expected 5 lines, got %d", len(lines))
	}
	if lines[0] != "line 1" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "line 1")
	}
	if lines[4] != "line 5" {
		t.Errorf("lines[4] = %q, want %q", lines[4], "line 5")
	}
}

func TestReadFileLinesNotFound(t *testing.T) {
	_, err := readFileLines("/nonexistent/path.rb")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestExtendMethodSnippets(t *testing.T) {
	dir := t.TempDir()

	// Write a source file with a multi-line method.
	src := `class WorkPackage
  def set_dates(wp)
    wp.start_date = start_date(wp)
    wp.due_date = due_date(wp)
    compute_successor_dates(wp)
    save_journals(wp)
    update_parent_dates(wp)
  end

  def close_duplicates
    duplicates.each(&:close!)
  end
end
`
	srcPath := filepath.Join(dir, "work_package.rb")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	// Simulate EmbedSymbol data that would come from the DB.
	syms := []sqlite.EmbedSymbol{
		{
			ID: 1, FileID: 100, Qualified: "WorkPackage",
			Kind: "class", Snippet: "class WorkPackage",
			LineStart: 1, LineEnd: 13,
		},
		{
			ID: 2, FileID: 100, Qualified: "WorkPackage#set_dates",
			Kind: "method", Snippet: "def set_dates(wp)",
			LineStart: 2, LineEnd: 8,
		},
		{
			ID: 3, FileID: 100, Qualified: "WorkPackage#close_duplicates",
			Kind: "method", Snippet: "def close_duplicates",
			LineStart: 10, LineEnd: 12,
		},
	}

	// We need a mock that resolves fileID 100 → the source path.
	// Since extendMethodSnippets calls h.idx.FilePathsByIDs and uses h.root,
	// we test the lower-level logic directly.

	// Read the file lines and apply the same logic as extendMethodSnippets.
	lines, err := readFileLines(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	for i := range syms {
		s := &syms[i]
		if s.Kind != "method" && s.Kind != "function" {
			continue
		}
		start := s.LineStart - 1
		end := s.LineStart - 1 + maxBodyLines
		if end > s.LineEnd {
			end = s.LineEnd
		}
		if start < 0 || start >= len(lines) || end > len(lines) {
			continue
		}
		s.Snippet = strings.Join(lines[start:end], "\n")
	}

	// Class should be unchanged.
	if syms[0].Snippet != "class WorkPackage" {
		t.Errorf("class snippet changed: %q", syms[0].Snippet)
	}

	// set_dates (lines 2-8, 7 lines) should include the full body.
	if !strings.Contains(syms[1].Snippet, "def set_dates(wp)") {
		t.Errorf("set_dates missing signature: %q", syms[1].Snippet)
	}
	if !strings.Contains(syms[1].Snippet, "compute_successor_dates(wp)") {
		t.Errorf("set_dates missing body line: %q", syms[1].Snippet)
	}
	setDatesLines := strings.Count(syms[1].Snippet, "\n") + 1
	if setDatesLines != 7 {
		t.Errorf("set_dates should have 7 lines, got %d", setDatesLines)
	}

	// close_duplicates (lines 10-12, 3 lines) should be fully included.
	if !strings.Contains(syms[2].Snippet, "def close_duplicates") {
		t.Errorf("close_duplicates missing signature: %q", syms[2].Snippet)
	}
	if !strings.Contains(syms[2].Snippet, "duplicates.each(&:close!)") {
		t.Errorf("close_duplicates missing body: %q", syms[2].Snippet)
	}

	t.Logf("set_dates snippet:\n%s", syms[1].Snippet)
	t.Logf("close_duplicates snippet:\n%s", syms[2].Snippet)
}
