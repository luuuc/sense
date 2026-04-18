package ruby

import (
	"testing"

	sitter "github.com/tree-sitter/go-tree-sitter"

	"github.com/luuuc/sense/internal/extract"
)

// TestSmokeExtract is a minimal independent check per extractor package:
// proves the extractor parses something trivial and emits at least one
// symbol, without going through the fixture harness. Catches regressions
// in the extract package or grammar binding that the fixture harness
// might mask (e.g. if the harness itself breaks).
func TestSmokeExtract(t *testing.T) {
	ex := Extractor{}
	p := sitter.NewParser()
	defer p.Close()
	if err := p.SetLanguage(ex.Grammar()); err != nil {
		t.Fatalf("SetLanguage: %v", err)
	}
	tree := p.Parse([]byte("class Foo\nend\n"), nil)
	if tree == nil {
		t.Fatal("Parse returned nil tree")
	}
	defer tree.Close()

	c := &counter{}
	if err := ex.Extract(tree, []byte("class Foo\nend\n"), "smoke.rb", c); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if c.symbols == 0 {
		t.Error("emitted 0 symbols; expected at least the Foo class")
	}
}

type counter struct {
	symbols int
	edges   int
}

func (c *counter) Symbol(extract.EmittedSymbol) error { c.symbols++; return nil }
func (c *counter) Edge(extract.EmittedEdge) error     { c.edges++; return nil }
