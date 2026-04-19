package scan

import "testing"

// TestImplSibling pins the test-association naming conventions for
// every supported language. Internal test because `implSibling` is
// unexported and its per-language branches are the kind of thing
// that can silently rot when someone adds a new suffix or language
// without updating the matcher.
func TestImplSibling(t *testing.T) {
	cases := []struct {
		path     string
		language string
		wantImpl string
		wantOK   bool
	}{
		// Ruby: _test.rb and _spec.rb both map to the .rb sibling.
		{"app/user_test.rb", "ruby", "app/user.rb", true},
		{"app/user_spec.rb", "ruby", "app/user.rb", true},
		{"app/user.rb", "ruby", "", false},

		// Python: test_ prefix maps to the stripped .py.
		{"pkg/test_user.py", "python", "pkg/user.py", true},
		{"pkg/user_test.py", "python", "", false}, // pytest uses prefix, not suffix
		{"pkg/user.py", "python", "", false},

		// Go: _test.go is the sole convention.
		{"widget/widget_test.go", "go", "widget/widget.go", true},
		{"widget/widget.go", "go", "", false},

		// TypeScript: .test.ts and .spec.ts map to .ts. Note the
		// typescript extractor's scope — .tsx files arrive as
		// language "tsx", not "typescript".
		{"src/foo.test.ts", "typescript", "src/foo.ts", true},
		{"src/foo.spec.ts", "typescript", "src/foo.ts", true},
		{"src/foo.ts", "typescript", "", false},

		// TSX: language "tsx" has its own branch.
		{"src/foo.test.tsx", "tsx", "src/foo.tsx", true},
		{"src/foo.spec.tsx", "tsx", "src/foo.tsx", true},

		// JavaScript: .test.js / .spec.js → .js, plus jsx variants.
		{"src/foo.test.js", "javascript", "src/foo.js", true},
		{"src/foo.spec.js", "javascript", "src/foo.js", true},
		{"src/foo.test.jsx", "javascript", "src/foo.jsx", true},

		// Negative: unknown language, untracked file kind.
		{"notes.txt", "plaintext", "", false},
		{"app/user_test.rb", "python", "", false}, // convention-matches-suffix but language mismatches
	}

	for _, c := range cases {
		gotImpl, gotOK := implSibling(c.path, c.language)
		if gotOK != c.wantOK {
			t.Errorf("implSibling(%q, %q) ok = %v, want %v", c.path, c.language, gotOK, c.wantOK)
			continue
		}
		if gotImpl != c.wantImpl {
			t.Errorf("implSibling(%q, %q) impl = %q, want %q", c.path, c.language, gotImpl, c.wantImpl)
		}
	}
}
