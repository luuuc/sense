package tsjs

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// TestSmokeExtract proves each of the three registered extractors
// (TypeScript / TSX / JavaScript) parses a trivial snippet and emits at
// least one symbol without going through the fixture harness.
func TestSmokeExtract(t *testing.T) {
	cases := []struct {
		name string
		ex   extract.Extractor
		src  string
	}{
		{"typescript", TypeScript{}, "export class Foo {}\n"},
		{"tsx", TSX{}, "export const X = <div/>;\n"},
		{"javascript", JavaScript{}, "export class Foo {}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sitter.NewParser()
			defer p.Close()
			if err := p.SetLanguage(tc.ex.Grammar()); err != nil {
				t.Fatalf("SetLanguage: %v", err)
			}
			src := []byte(tc.src)
			tree := p.Parse(src, nil)
			if tree == nil {
				t.Fatal("Parse returned nil tree")
			}
			defer tree.Close()

			c := &counter{}
			if err := tc.ex.Extract(tree, src, "smoke", c); err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if c.symbols == 0 {
				t.Errorf("%s emitted 0 symbols; expected ≥1", tc.name)
			}
		})
	}
}

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }
