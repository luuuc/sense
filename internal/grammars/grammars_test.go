package grammars

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

// TestAllGrammarsParse is a smoke test: every grammar parses a trivial
// valid snippet. Catches version mismatches between the runtime and a
// grammar (MIN_COMPATIBLE_LANGUAGE_VERSION drift) at test time rather
// than at first scan.
func TestAllGrammarsParse(t *testing.T) {
	cases := []struct {
		name string
		lang func() *sitter.Language
		src  string
	}{
		{"ruby", Ruby, "class Foo; end\n"},
		{"python", Python, "class Foo:\n    pass\n"},
		{"typescript", TypeScript, "export class Foo {}\n"},
		{"tsx", TSX, "export const x = <div/>;\n"},
		{"javascript", JavaScript, "export class Foo {}\n"},
		{"go", Go, "package p\nfunc F() {}\n"},
		{"rust", Rust, "fn main() {}\n"},
		{"java", Java, "public class Foo {}\n"},
		{"c", C, "int main() { return 0; }\n"},
		{"cpp", Cpp, "class Foo {};\n"},
		{"csharp", CSharp, "class Foo {}\n"},
		{"php", PHP, "<?php class Foo {}\n"},
		{"kotlin", Kotlin, "fun main() {}\n"},
		{"scala", Scala, "object Foo {}\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sitter.NewParser()
			defer p.Close()

			if err := p.SetLanguage(tc.lang()); err != nil {
				t.Fatalf("SetLanguage: %v", err)
			}
			tree := p.Parse([]byte(tc.src), nil)
			if tree == nil {
				t.Fatal("ParseCtx returned nil tree")
			}
			defer tree.Close()

			root := tree.RootNode()
			if root.HasError() {
				t.Errorf("root node reports parse error: %s", root.ToSexp())
			}
		})
	}
}
