package ignore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildNestedGitignore(t *testing.T) {
	root := t.TempDir()

	// Root .gitignore ignores *.log
	writeTestFile(t, filepath.Join(root, ".gitignore"), "*.log\n")

	// sub/.gitignore ignores /local-only (anchored to sub/)
	sub := filepath.Join(root, "sub")
	_ = os.MkdirAll(sub, 0o755)
	writeTestFile(t, filepath.Join(sub, ".gitignore"), "/local-only\n*.tmp\n")

	// deeper nested: sub/deep/.gitignore
	deep := filepath.Join(sub, "deep")
	_ = os.MkdirAll(deep, 0o755)
	writeTestFile(t, filepath.Join(deep, ".gitignore"), "secret.txt\n")

	m, err := Build(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path string
		want bool
	}{
		{"app.log", true},               // root .gitignore
		{"sub/app.log", true},            // root .gitignore applies everywhere
		{"sub/local-only", true},         // sub/.gitignore anchored with prefix
		{"local-only", false},            // not at root
		{"sub/foo.tmp", true},            // sub/.gitignore unanchored
		{"foo.tmp", false},               // sub/.gitignore doesn't apply at root
		{"sub/deep/secret.txt", true},    // deep/.gitignore
		{"secret.txt", false},            // deep/.gitignore doesn't apply at root
		{"sub/deep/other.txt", false},    // not ignored
	}
	for _, tt := range tests {
		if got := m.Match(tt.path, false); got != tt.want {
			t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestBuildSenseignore(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, ".senseignore"), "vendor/\nfixtures/\n")

	m, err := Build(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	if !m.Match("vendor", true) {
		t.Error("vendor dir should be ignored via .senseignore")
	}
	if !m.Match("vendor/gems/foo.rb", false) {
		t.Error("vendor children should be ignored via .senseignore")
	}
	if !m.Match("fixtures", true) {
		t.Error("fixtures dir should be ignored via .senseignore")
	}
	if m.Match("src/main.go", false) {
		t.Error("src/main.go should not be ignored")
	}
}

func TestBuildExtraPatterns(t *testing.T) {
	root := t.TempDir()
	m, err := Build(root, []string{"node_modules/", "*.min.js"})
	if err != nil {
		t.Fatal(err)
	}

	if !m.Match("node_modules", true) {
		t.Error("node_modules should be ignored via extra patterns")
	}
	if !m.Match("node_modules/express/index.js", false) {
		t.Error("node_modules children should be ignored")
	}
	if !m.Match("dist/app.min.js", false) {
		t.Error("*.min.js should be ignored via extra patterns")
	}
}

func TestBuildSkipsSenseDir(t *testing.T) {
	root := t.TempDir()
	senseDir := filepath.Join(root, ".sense")
	_ = os.MkdirAll(senseDir, 0o755)
	writeTestFile(t, filepath.Join(senseDir, ".gitignore"), "*.go\n")

	m, err := Build(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	// .sense/.gitignore should NOT have been loaded
	if m.Match("main.go", false) {
		t.Error(".sense/.gitignore should not have been loaded")
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
