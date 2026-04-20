package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBasicPatterns(t *testing.T) {
	m := New("*.log", "build/", "tmp")
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"app.log", false, true},
		{"sub/app.log", false, true},
		{"build", true, true},
		{"build", false, false}, // trailing / means dir-only
		{"sub/build", true, true},
		{"tmp", false, true},
		{"tmp", true, true},
		{"tmp/foo", false, true},
		{"readme.md", false, false},
	}
	for _, tt := range tests {
		if got := m.Match(tt.path, tt.isDir); got != tt.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestNegation(t *testing.T) {
	m := New("*.log", "!important.log")
	if m.Match("debug.log", false) != true {
		t.Error("debug.log should be ignored")
	}
	if m.Match("important.log", false) != false {
		t.Error("important.log should NOT be ignored (negated)")
	}
}

func TestAnchoredPattern(t *testing.T) {
	m := New("/root-only.txt")
	if !m.Match("root-only.txt", false) {
		t.Error("root-only.txt should match anchored pattern")
	}
	if m.Match("sub/root-only.txt", false) {
		t.Error("sub/root-only.txt should NOT match anchored pattern")
	}
}

func TestInteriorSlashAnchors(t *testing.T) {
	m := New("doc/frotz")
	if !m.Match("doc/frotz", false) {
		t.Error("doc/frotz should match")
	}
	if m.Match("sub/doc/frotz", false) {
		t.Error("sub/doc/frotz should NOT match (interior slash anchors)")
	}
}

func TestDoublestar(t *testing.T) {
	m := New("**/foo", "bar/**", "a/**/b")
	tests := []struct {
		path string
		want bool
	}{
		{"foo", true},
		{"sub/foo", true},
		{"a/b/c/foo", true},
		{"bar/x", true},
		{"bar/x/y", true},
		{"a/b", true},
		{"a/x/b", true},
		{"a/x/y/b", true},
	}
	for _, tt := range tests {
		if got := m.Match(tt.path, false); got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestAddFromFile(t *testing.T) {
	dir := t.TempDir()
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("*.o\nbuild/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.AddFromFile(gi, ""); err != nil {
		t.Fatal(err)
	}
	if !m.Match("foo.o", false) {
		t.Error("foo.o should be ignored")
	}
	if !m.Match("build", true) {
		t.Error("build/ should be ignored")
	}
}

func TestAddFromFileMissing(t *testing.T) {
	m := New()
	if err := m.AddFromFile("/nonexistent/.gitignore", ""); err != nil {
		t.Errorf("missing file should not be an error: %v", err)
	}
}

func TestAddFromFileWithPrefix(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gi := filepath.Join(sub, ".gitignore")
	if err := os.WriteFile(gi, []byte("/local-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.AddFromFile(gi, "sub"); err != nil {
		t.Fatal(err)
	}
	if !m.Match("sub/local-only", false) {
		t.Error("sub/local-only should match with prefix")
	}
	if m.Match("local-only", false) {
		t.Error("local-only without prefix should NOT match")
	}
}

func TestEmptyMatcher(t *testing.T) {
	m := New()
	if m.Match("anything", false) {
		t.Error("empty matcher should not match anything")
	}
}

func TestComments(t *testing.T) {
	m := New("# this is a comment", "*.log")
	if m.Match("# this is a comment", false) {
		t.Error("comment lines should not become patterns")
	}
	if !m.Match("app.log", false) {
		t.Error("*.log should still match")
	}
}

func TestCharacterClass(t *testing.T) {
	m := New("*.[oa]")
	if !m.Match("foo.o", false) {
		t.Error("foo.o should match *.[oa]")
	}
	if !m.Match("bar.a", false) {
		t.Error("bar.a should match *.[oa]")
	}
	if m.Match("baz.c", false) {
		t.Error("baz.c should NOT match *.[oa]")
	}
}

func TestDirOnlyExcludesChildren(t *testing.T) {
	m := New("vendor/")
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"vendor", true, true},
		{"vendor", false, false},
		{"vendor/gems/foo.rb", false, true},
		{"vendor/bundle", true, true},
		{"sub/vendor/gems/foo.rb", false, true},
		{"not-vendor/foo.rb", false, false},
	}
	for _, tt := range tests {
		if got := m.Match(tt.path, tt.isDir); got != tt.want {
			t.Errorf("Match(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestEscapedTrailingSpace(t *testing.T) {
	m := New("foo\\ ")
	if !m.Match("foo ", false) {
		t.Error(`"foo\ " should match "foo "`)
	}
	if m.Match("foo", false) {
		t.Error(`"foo\ " should NOT match "foo"`)
	}
}

func TestQuestionMark(t *testing.T) {
	m := New("?.txt")
	if !m.Match("a.txt", false) {
		t.Error("a.txt should match ?.txt")
	}
	if m.Match("ab.txt", false) {
		t.Error("ab.txt should NOT match ?.txt")
	}
}
