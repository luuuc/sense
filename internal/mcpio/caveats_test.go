package mcpio

import (
	"strings"
	"testing"
)

func TestIndexCaveat(t *testing.T) {
	cases := []struct {
		file    string
		wantSub string // substring expected in the caveat; "" means empty result
	}{
		{"main.go", "method-on-field dispatch"},
		{"app/models/user.rb", "DiscoursePluginRegistry"},
		{"lib/tasks/cleanup.rake", "DiscoursePluginRegistry"},
		{"src/index.js", "edge-runtime mirror"},
		{"src/index.jsx", "edge-runtime mirror"},
		{"src/index.mjs", "edge-runtime mirror"},
		{"src/index.cjs", "edge-runtime mirror"},
		{"src/index.ts", "edge-runtime mirror"},
		{"src/index.tsx", "edge-runtime mirror"},
		{"app/server.py", "decorator-registered handlers"},
		{"src/Main.java", "ServiceLoader"},
		{"src/Main.kt", "ServiceLoader"},
		{"build.kts", "ServiceLoader"},
		// Case-insensitive extension match.
		{"src/Main.JAVA", "ServiceLoader"},
		{"src/index.TS", "edge-runtime mirror"},
		// Unknown extension → empty.
		{"README.md", ""},
		{"data.txt", ""},
		// No extension → empty.
		{"Makefile", ""},
		// Empty filename → empty.
		{"", ""},
	}

	for _, c := range cases {
		got := IndexCaveat(c.file)
		if c.wantSub == "" {
			if got != "" {
				t.Errorf("IndexCaveat(%q) = %q, want empty", c.file, got)
			}
			continue
		}
		if !strings.Contains(got, c.wantSub) {
			t.Errorf("IndexCaveat(%q) = %q, want substring %q", c.file, got, c.wantSub)
		}
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"a.go", "go"},
		{"a.rb", "ruby"},
		{"a.rake", "ruby"},
		{"a.js", "javascript"},
		{"a.jsx", "javascript"},
		{"a.mjs", "javascript"},
		{"a.cjs", "javascript"},
		{"a.ts", "typescript"},
		{"a.tsx", "typescript"},
		{"a.py", "python"},
		{"a.java", "java"},
		{"a.kt", "kotlin"},
		{"a.kts", "kotlin"},
		{"a.GO", "go"},
		{"weird.xyz", ""},
		{"no_extension", ""},
		{"", ""},
		// Dot in directory, no extension on filename.
		{"some.dir/file", ""},
	}

	for _, c := range cases {
		if got := detectLanguage(c.file); got != c.want {
			t.Errorf("detectLanguage(%q) = %q, want %q", c.file, got, c.want)
		}
	}
}
