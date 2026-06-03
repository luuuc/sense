package summary

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNamespacePrefixFromPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"internal/summary/summary.go", "internal/summary"},
		{"main.go", "."},
		{"cmd/sense/main.go", "cmd/sense"},
		{"a/b/c/d.go", "a/b"},
		{"root.go", "."},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := namespacePrefixFromPath(tt.input)
			if got != tt.want {
				t.Errorf("namespacePrefixFromPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestCommonPrefixCoverage(t *testing.T) {
	tests := []struct {
		a, b string
		want string
	}{
		{"internal/summary/a.go", "internal/summary/b.go", "internal/summary"},
		{"internal/a.go", "internal/b.go", "internal"},
		{"a.go", "b.go", ""},
		{"same.go", "same.go", "same.go"},
		{"a/b/c.go", "a/b/d.go", "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.a+"+"+tt.b, func(t *testing.T) {
			got := commonPrefix(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("commonPrefix(%q, %q) = %q, want %q", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCapitalize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "Hello"},
		{"Hello", "Hello"},
		{"", ""},
		{"a", "A"},
		{"123", "123"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := capitalize(tt.input)
			if got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReadStructuredDescription(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name: "package.json",
			files: map[string]string{
				"package.json": `{"name":"test","description":"A test package"}`,
			},
			want: "A test package",
		},
		{
			name: "Cargo.toml",
			files: map[string]string{
				"Cargo.toml": "[package]\nname = \"test\"\ndescription = \"A Rust project\"\nversion = \"1.0.0\"\n",
			},
			want: "A Rust project",
		},
		{
			name: "pyproject.toml",
			files: map[string]string{
				"pyproject.toml": "[project]\nname = \"test\"\ndescription = \"A Python project\"\n",
			},
			want: "A Python project",
		},
		{
			name: "setup.cfg",
			files: map[string]string{
				"setup.cfg": "[metadata]\nname = test\ndescription = A setuptools project\n",
			},
			want: "A setuptools project",
		},
		{
			name: "gemspec",
			files: map[string]string{
				"test.gemspec": "Gem::Specification.new do |s|\n  s.name = 'test'\n  s.summary = 'A Ruby gem'\nend\n",
			},
			want: "A Ruby gem",
		},
		{
			name: "pom.xml",
			files: map[string]string{
				"pom.xml": "<project>\n<description>A Java project</description>\n</project>\n",
			},
			want: "A Java project",
		},
		{
			name: "gemspec description",
			files: map[string]string{
				"test.gemspec": "Gem::Specification.new do |s|\n  s.name = 'test'\n  s.description = 'A Ruby project'\nend\n",
			},
			want: "A Ruby project",
		},
		{
			name: "no metadata",
			files: map[string]string{
				"README.md": "# Test\n\nA project.\n",
			},
			want: "",
		},
		{
			name: "package.json priority",
			files: map[string]string{
				"package.json": `{"description":"From package"}`,
				"Cargo.toml":   "[package]\ndescription = \"From cargo\"\n",
			},
			want: "From package",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for name, content := range tt.files {
				path := filepath.Join(root, name)
				if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
					t.Fatalf("write %s: %v", name, err)
				}
			}
			got := readStructuredDescription(root)
			if got != tt.want {
				t.Errorf("readStructuredDescription() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanTOMLValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.toml")
	content := "[package]\nname = \"test\"\ndescription = \"hello world\"\nversion = \"1.0\"\n\n[dependencies]\nfoo = \"1.0\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := scanTOMLValue(path, "[package]", "description"); got != "hello world" {
		t.Errorf("scanTOMLValue() = %q, want %q", got, "hello world")
	}
	if got := scanTOMLValue(path, "[package]", "name"); got != "test" {
		t.Errorf("scanTOMLValue(name) = %q, want %q", got, "test")
	}
	if got := scanTOMLValue(path, "[package]", "foo"); got != "" {
		t.Errorf("scanTOMLValue(package.foo) = %q, want empty", got)
	}
}

func TestScanINIValue(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "test.cfg")
	content := "[metadata]\nname = test\ndescription = hello world\n\n[options]\ninstall_requires = foo\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if got := scanINIValue(path, "metadata", "description"); got != "hello world" {
		t.Errorf("scanINIValue() = %q, want %q", got, "hello world")
	}
	if got := scanINIValue(path, "metadata", "name"); got != "test" {
		t.Errorf("scanINIValue(name) = %q, want %q", got, "test")
	}
	if got := scanINIValue(path, "metadata", "install_requires"); got != "" {
		t.Errorf("scanINIValue(metadata.install_requires) = %q, want empty", got)
	}
}

func TestRenderProjectEmptyRoot(t *testing.T) {
	if got := renderProject(""); got != "" {
		t.Errorf("renderProject(\"\") = %q, want empty", got)
	}
}

func TestRenderProjectMissingREADME(t *testing.T) {
	root := t.TempDir()
	if got := renderProject(root); got != "" {
		t.Errorf("renderProject(missing README) = %q, want empty", got)
	}
}

func TestRenderProjectSkipsBadges(t *testing.T) {
	root := t.TempDir()
	content := "# Title\n\n[![build](https://img.shields.io)]\n\nActual description here.\n"
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := renderProject(root); got != "Actual description here." {
		t.Errorf("renderProject() = %q, want %q", got, "Actual description here.")
	}
}

func TestTokenBudget(t *testing.T) {
	t.Setenv("SENSE_SUMMARY_TOKENS", "")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "5000")
	if got := TokenBudget(); got != 5000 {
		t.Errorf("TokenBudget() = %d, want 5000", got)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "invalid")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}

	t.Setenv("SENSE_SUMMARY_TOKENS", "-1")
	if got := TokenBudget(); got != DefaultTokenBudget {
		t.Errorf("TokenBudget() = %d, want %d", got, DefaultTokenBudget)
	}
}
