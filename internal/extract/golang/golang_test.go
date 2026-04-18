package golang

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// TestSmokeExtract proves the extractor parses a trivial snippet and
// emits at least one symbol without going through the fixture harness.
func TestSmokeExtract(t *testing.T) {
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	src := []byte("package p\n\nfunc F() {}\n")
	tree := p.Parse(src, nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, src, "smoke.go", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the F function")
	}
}

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }
